package poller

import (
	"container/heap"
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jpalmerr/pulseboard/internal/types"
)

// StatusExtractor is a function that determines status from an HTTP response.
//
// This is the poller-internal version that returns a string rather than
// the pulseboard.Status type, avoiding circular dependencies.
type StatusExtractor func(body []byte, statusCode int) string

// EndpointInfo contains the configuration needed to poll a single endpoint.
//
// This is the poller-internal representation of an endpoint, decoupled from
// the main pulseboard.Endpoint type to avoid circular dependencies.
type EndpointInfo struct {
	// Name is the display name of the endpoint.
	Name string

	// URL is the target URL to poll.
	URL string

	// Labels contains key-value metadata for the endpoint.
	Labels map[string]string

	// Headers contains custom HTTP headers to send with requests.
	Headers map[string]string

	// Timeout is the per-request timeout duration.
	Timeout time.Duration

	// Extractor determines how to interpret the response as a status.
	// If nil, the default HTTP status code mapping is used.
	Extractor StatusExtractor

	// Method is the HTTP method (GET, HEAD, POST). Empty defaults to GET.
	Method string

	// Interval is the custom polling interval for this endpoint.
	// If 0, the scheduler's global interval is used.
	Interval time.Duration
}

// pollEntry is a single entry in the priority queue, pairing an endpoint with
// its scheduled next-poll time.
type pollEntry struct {
	endpoint EndpointInfo
	nextPoll time.Time
	// index is maintained by heap.Interface for O(log n) operations.
	index int
}

// pollEntryHeap is the internal slice backing the min-heap ordered by nextPoll.
// It implements heap.Interface directly; callers must hold pollQueue.mu.
type pollEntryHeap []*pollEntry

// Len returns the number of entries in the heap.
func (h pollEntryHeap) Len() int { return len(h) }

// Less reports whether entry i should be popped before entry j.
// The heap is a min-heap on nextPoll (earliest due time at the top).
func (h pollEntryHeap) Less(i, j int) bool {
	return h[i].nextPoll.Before(h[j].nextPoll)
}

// Swap exchanges entries i and j and updates their stored indices.
func (h pollEntryHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

// Push appends a new entry to the heap slice (called by container/heap).
func (h *pollEntryHeap) Push(x any) {
	entry := x.(*pollEntry)
	entry.index = len(*h)
	*h = append(*h, entry)
}

// Pop removes and returns the last entry from the heap slice (called by container/heap).
// The heap package swaps the minimum to the end before calling Pop, so this
// removes the previously-minimum entry.
func (h *pollEntryHeap) Pop() any {
	old := *h
	n := len(old)
	entry := old[n-1]
	old[n-1] = nil  // prevent memory leak
	entry.index = -1 // mark as removed
	*h = old[:n-1]
	return entry
}

// pollQueue is a min-heap of poll entries ordered by next-due time.
// All exported methods are safe for concurrent use.
type pollQueue struct {
	entries pollEntryHeap
	mu      sync.Mutex
}

// newPollQueue creates a pollQueue pre-populated with all endpoints, each
// scheduled for immediate polling (nextPoll = now).
func newPollQueue(endpoints []EndpointInfo) *pollQueue {
	now := time.Now()
	q := &pollQueue{
		entries: make(pollEntryHeap, len(endpoints)),
	}
	for i, ep := range endpoints {
		q.entries[i] = &pollEntry{
			endpoint: ep,
			nextPoll: now,
			index:    i,
		}
	}
	heap.Init(&q.entries)
	return q
}

// push schedules ep for polling at nextPoll. Safe for concurrent use.
func (q *pollQueue) push(ep EndpointInfo, nextPoll time.Time) {
	q.mu.Lock()
	defer q.mu.Unlock()
	heap.Push(&q.entries, &pollEntry{endpoint: ep, nextPoll: nextPoll})
}

// popDue removes and returns all entries whose nextPoll is at or before now.
// Safe for concurrent use.
func (q *pollQueue) popDue(now time.Time) []EndpointInfo {
	q.mu.Lock()
	defer q.mu.Unlock()

	var due []EndpointInfo
	for q.entries.Len() > 0 && !q.entries[0].nextPoll.After(now) {
		entry := heap.Pop(&q.entries).(*pollEntry)
		due = append(due, entry.endpoint)
	}
	return due
}

// peek returns the duration until the next entry is due.
// Returns 100ms if the queue is empty (prevents scheduler from spinning).
// Never returns 0 or a negative duration. Safe for concurrent use.
func (q *pollQueue) peek() time.Duration {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.entries.Len() == 0 {
		return 100 * time.Millisecond
	}
	d := time.Until(q.entries[0].nextPoll)
	if d <= 0 {
		// Entry is already due; yield a minimal sleep so the caller can act.
		return time.Millisecond
	}
	return d
}

// empty reports whether the queue has no entries. Safe for concurrent use.
func (q *pollQueue) empty() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.entries.Len() == 0
}

// Scheduler manages periodic polling of multiple endpoints.
//
// Scheduler implements a persistent worker pool backed by a priority queue.
// Endpoints are polled at their respective intervals; the scheduler wakes only
// when the next endpoint is due, eliminating O(n) work on every tick. Results
// are emitted to a channel that can be consumed by the caller.
//
// All lifecycle methods (Start, Stop) are safe for concurrent use.
type Scheduler struct {
	endpoints      []EndpointInfo
	interval       time.Duration // global default interval
	maxConcurrency int
	client         *Client
	results        chan types.StatusResult
	logger         *slog.Logger
	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup

	mu        sync.Mutex
	started   bool
	stopped   bool
	closeOnce sync.Once

	queue    *pollQueue
	jobsChan chan EndpointInfo
}

// NewScheduler creates a new polling [Scheduler].
//
// Parameters:
//   - endpoints: List of endpoints to poll
//   - interval: Time between polling cycles
//   - maxConcurrency: Maximum number of concurrent HTTP requests. If <= 0 it is
//     silently clamped to 1.
//   - logger: Logger for scheduler events (panic recovery, etc.)
//   - clientOpts: Optional [ClientOption] values applied to the underlying HTTP client
//
// The scheduler must be started with [Scheduler.Start] and stopped with
// [Scheduler.Stop]. Results are available via [Scheduler.Results].
func NewScheduler(endpoints []EndpointInfo, interval time.Duration, maxConcurrency int, logger *slog.Logger, clientOpts ...ClientOption) *Scheduler {
	if maxConcurrency <= 0 {
		maxConcurrency = 1
	}
	return &Scheduler{
		endpoints:      endpoints,
		interval:       interval,
		maxConcurrency: maxConcurrency,
		client:         NewClient(clientOpts...),
		// Buffer sized to len(endpoints) to absorb the initial burst where all
		// endpoints are polled simultaneously. Workers back-pressure naturally if
		// the consumer falls behind. Minimum 1 to avoid blocking on zero endpoints.
		results: make(chan types.StatusResult, max(len(endpoints), 1)),
		logger:  logger,
	}
}

// Results returns a receive-only channel that emits [StatusResult] values.
//
// The channel is closed when the scheduler stops. Consumers should read from
// this channel until it is closed to receive all poll results.
func (s *Scheduler) Results() <-chan types.StatusResult {
	return s.results
}

// Start begins the polling loop in a background goroutine.
//
// Start is non-blocking and returns immediately. The scheduler will:
//  1. Poll all endpoints immediately (nextPoll initialised to time.Now())
//  2. Use a priority queue to wake only when the next endpoint is due
//  3. Dispatch due endpoints to a persistent worker pool
//  4. Continue until [Scheduler.Stop] is called or the context is cancelled
//
// If ctx is nil, context.Background() is used as the parent context.
// Start is idempotent; subsequent calls after the first are no-ops.
// If Stop was called before Start, Start is a no-op.
func (s *Scheduler) Start(ctx context.Context) {
	s.mu.Lock()
	if s.started || s.stopped {
		s.mu.Unlock()
		return
	}
	s.started = true

	if ctx == nil {
		ctx = context.Background()
	}
	s.ctx, s.cancel = context.WithCancel(ctx)
	pollCtx := s.ctx // capture under lock to avoid race

	s.queue = newPollQueue(s.endpoints)
	s.jobsChan = make(chan EndpointInfo, s.maxConcurrency)

	// Spawn persistent worker pool before releasing the lock so all goroutines
	// are counted in s.wg before Stop() can call s.wg.Wait().
	for i := 0; i < s.maxConcurrency; i++ {
		s.wg.Add(1)
		go s.worker(s.jobsChan, pollCtx)
	}
	s.wg.Add(1)
	go s.schedulerLoop(pollCtx)

	s.mu.Unlock()
}

// Stop halts the scheduler and waits for all goroutines to complete.
//
// Stop cancels the scheduler's context and blocks until:
//   - The scheduler loop exits
//   - All workers drain jobsChan and exit
//   - All in-flight requests complete
//   - The results channel is closed
//
// Stop is idempotent and safe to call multiple times. Calling Stop before
// Start is a safe no-op.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	if !s.stopped {
		s.stopped = true
		if s.cancel != nil {
			s.cancel()
		}
	}
	s.mu.Unlock()

	s.wg.Wait()

	// clean up client connections after all goroutines complete
	if s.client != nil {
		s.client.Close()
	}

	// ensure channel is closed even if Start() was never called
	s.closeOnce.Do(func() { close(s.results) })
}

// schedulerLoop is the single goroutine that drives the priority queue.
// It wakes when the next entry is due, pops all due endpoints, and sends
// them to jobsChan. It closes jobsChan on exit so workers drain and stop.
func (s *Scheduler) schedulerLoop(ctx context.Context) {
	defer s.wg.Done()
	defer close(s.jobsChan) // workers exit once jobsChan is drained

	for {
		sleep := s.queue.peek() // 100ms default if empty; never 0

		timer := time.NewTimer(sleep)
		select {
		case <-timer.C:
			timer.Stop()
			now := time.Now()
			due := s.queue.popDue(now)
			for _, ep := range due {
				interval := ep.Interval
				if interval == 0 {
					interval = s.interval
				}
				select {
				case s.jobsChan <- ep:
					// Re-schedule the endpoint for its next poll. The next
					// poll time is based on when we dispatched (now), not when
					// the HTTP response returns, matching the original semantic.
					s.queue.push(ep, now.Add(interval))
				case <-ctx.Done():
					// Remaining due entries are not re-inserted. At shutdown this is
					// intentional — the scheduler cannot be restarted, so dropped
					// entries have no effect on future operation.
					return
				}
			}
		case <-ctx.Done():
			timer.Stop()
			return
		}
	}
}

// worker reads endpoints from jobs and polls each one, forwarding results to
// the results channel. It exits when jobs is closed or ctx is cancelled.
func (s *Scheduler) worker(jobs <-chan EndpointInfo, ctx context.Context) {
	defer s.wg.Done()
	for {
		select {
		case ep, ok := <-jobs:
			if !ok {
				return
			}
			result := s.pollEndpoint(ctx, ep)
			select {
			case s.results <- result:
			case <-ctx.Done():
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

// pollEndpoint polls a single endpoint and returns the result.
func (s *Scheduler) pollEndpoint(ctx context.Context, ep EndpointInfo) types.StatusResult {
	resp := s.client.Fetch(ctx, ep.Method, ep.URL, ep.Headers, ep.Timeout)

	result := types.StatusResult{
		EndpointName:   ep.Name,
		URL:            ep.URL,
		Labels:         ep.Labels,
		Latency:        resp.Latency,
		ResponseTimeMs: resp.Latency.Milliseconds(),
		CheckedAt:      time.Now(),
		RawResponse:    resp.Body,
		StatusCode:     resp.StatusCode,
		Error:          resp.Error,
	}

	if resp.Error != nil {
		result.Status = "down"
		s := resp.Error.Error()
		result.ErrorStr = &s
	} else if ep.Extractor != nil {
		status, err := s.safeExtract(ep.Extractor, resp.Body, resp.StatusCode)
		result.Status = status
		if err != nil {
			result.Error = err
			e := err.Error()
			result.ErrorStr = &e
		}
	} else {
		// default: use HTTP status code
		result.Status = httpStatusToStatus(resp.StatusCode)
	}

	return result
}

// safeExtract calls the extractor with panic recovery.
// If the extractor panics, it logs the full stack trace with a correlation ID
// and returns "error" status with a user-friendly error containing the ID.
func (s *Scheduler) safeExtract(extractor StatusExtractor, body []byte, statusCode int) (status string, err error) {
	defer func() {
		if r := recover(); r != nil {
			correlationID := uuid.NewString()
			stack := debug.Stack()

			// log full context server-side for debugging
			s.logger.Error("extractor panic",
				"correlation_id", correlationID,
				"panic", fmt.Sprintf("%v", r),
				"stack", string(stack),
			)

			status = "error"
			err = fmt.Errorf("extractor panic (correlation_id: %s)", correlationID)
		}
	}()
	return extractor(body, statusCode), nil
}

// httpStatusToStatus maps HTTP status codes to status strings.
func httpStatusToStatus(code int) string {
	switch {
	case code >= 200 && code < 300:
		return "up"
	case code >= 400 && code < 500:
		return "degraded"
	// 1xx and 3xx (if redirect limit exceeded) are treated as down.
	// The http.Client follows redirects automatically (up to 10 per request),
	// so 3xx responses are only seen here in exceptional cases.
	default:
		return "down"
	}
}
