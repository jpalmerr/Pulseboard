package poller

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"github.com/google/uuid"
)

// StatusResult holds the outcome of polling a single endpoint.
//
// StatusResult contains all information about a poll attempt, including
// the determined status, timing information, and any error that occurred.
type StatusResult struct {
	// EndpointName is the display name of the polled endpoint.
	EndpointName string

	// URL is the target URL that was polled.
	URL string

	// Status is the determined health status as a string (e.g., "up", "down").
	Status string

	// Labels contains the key-value metadata associated with the endpoint.
	Labels map[string]string

	// Latency is the time taken to complete the HTTP request.
	Latency time.Duration

	// CheckedAt is the timestamp when the poll was performed.
	CheckedAt time.Time

	// Error contains any error that occurred during polling.
	Error error

	// RawResponse contains the HTTP response body for debugging.
	RawResponse []byte

	// StatusCode is the HTTP status code returned by the endpoint.
	StatusCode int
}

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

// Scheduler manages periodic polling of multiple endpoints.
//
// Scheduler implements a worker pool pattern, polling configured endpoints
// at their respective intervals with configurable concurrency. Results are
// emitted to a channel that can be consumed by the caller.
//
// The scheduler polls all endpoints immediately on start, then uses a
// tick-and-check pattern where it ticks at the GCD of all endpoint intervals
// and polls only endpoints that are due.
//
// All lifecycle methods (Start, Stop) are safe for concurrent use.
type Scheduler struct {
	endpoints      []EndpointInfo
	interval       time.Duration // global default interval
	maxConcurrency int
	client         *Client
	results        chan StatusResult
	logger         *slog.Logger
	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup

	mu        sync.Mutex
	started   bool
	stopped   bool
	closeOnce sync.Once

	// per-endpoint timing for tick-and-check pattern
	lastPolledAt map[string]time.Time
	baseInterval time.Duration
}

// NewScheduler creates a new polling [Scheduler].
//
// Parameters:
//   - endpoints: List of endpoints to poll
//   - interval: Time between polling cycles
//   - maxConcurrency: Maximum number of concurrent HTTP requests
//   - logger: Logger for scheduler events (panic recovery, etc.)
//
// The scheduler must be started with [Scheduler.Start] and stopped with
// [Scheduler.Stop]. Results are available via [Scheduler.Results].
func NewScheduler(endpoints []EndpointInfo, interval time.Duration, maxConcurrency int, logger *slog.Logger) *Scheduler {
	return &Scheduler{
		endpoints:      endpoints,
		interval:       interval,
		maxConcurrency: maxConcurrency,
		client:         NewClient(),
		results:        make(chan StatusResult, len(endpoints)),
		logger:         logger,
	}
}

// Results returns a receive-only channel that emits [StatusResult] values.
//
// The channel is closed when the scheduler stops. Consumers should read from
// this channel until it is closed to receive all poll results.
func (s *Scheduler) Results() <-chan StatusResult {
	return s.results
}

// calculateBaseInterval determines the tick interval for the scheduler.
// Uses the GCD of all endpoint intervals to ensure timely polling.
func (s *Scheduler) calculateBaseInterval() time.Duration {
	if len(s.endpoints) == 0 {
		return s.interval
	}

	intervals := make([]time.Duration, 0, len(s.endpoints))
	for _, ep := range s.endpoints {
		if ep.Interval > 0 {
			intervals = append(intervals, ep.Interval)
		} else {
			intervals = append(intervals, s.interval)
		}
	}

	result := intervals[0]
	for _, d := range intervals[1:] {
		result = gcdDuration(result, d)
	}

	// floor at 1 second to prevent CPU thrashing
	if result < time.Second {
		result = time.Second
	}

	return result
}

// gcdDuration calculates the greatest common divisor of two durations.
func gcdDuration(a, b time.Duration) time.Duration {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// Start begins the polling loop in a background goroutine.
//
// Start is non-blocking and returns immediately. The scheduler will:
//  1. Poll all endpoints immediately
//  2. Tick at the GCD of all endpoint intervals
//  3. Poll only endpoints that are due on each tick
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
	s.lastPolledAt = make(map[string]time.Time, len(s.endpoints))
	s.baseInterval = s.calculateBaseInterval()

	if ctx == nil {
		ctx = context.Background()
	}
	s.ctx, s.cancel = context.WithCancel(ctx)
	pollCtx := s.ctx // capture under lock to avoid race
	s.wg.Add(1)
	s.mu.Unlock()

	go func() {
		defer s.wg.Done()
		defer s.closeOnce.Do(func() { close(s.results) })

		s.pollDueEndpoints(pollCtx, true)

		ticker := time.NewTicker(s.baseInterval)
		defer ticker.Stop()

		for {
			select {
			case <-pollCtx.Done():
				return
			case <-ticker.C:
				s.pollDueEndpoints(pollCtx, false)
			}
		}
	}()
}

// Stop halts the scheduler and waits for all goroutines to complete.
//
// Stop cancels the scheduler's context and blocks until:
//   - The polling loop exits
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

// pollDueEndpoints polls only endpoints that are due based on their intervals.
// If immediate is true, polls all endpoints regardless of timing.
//
// TIMING SEMANTIC: lastPolledAt is updated when a poll STARTS, not when it
// completes. This prevents concurrent polls of the same endpoint but means
// effective interval = configured interval + poll duration for slow endpoints.
func (s *Scheduler) pollDueEndpoints(ctx context.Context, immediate bool) {
	now := time.Now()
	dueEndpoints := make([]EndpointInfo, 0, len(s.endpoints))

	s.mu.Lock()
	for _, ep := range s.endpoints {
		if immediate {
			dueEndpoints = append(dueEndpoints, ep)
			s.lastPolledAt[ep.Name] = now
			continue
		}

		interval := ep.Interval
		if interval == 0 {
			interval = s.interval // use global default
		}

		lastPolled, exists := s.lastPolledAt[ep.Name]
		if !exists || now.Sub(lastPolled) >= interval {
			dueEndpoints = append(dueEndpoints, ep)
			s.lastPolledAt[ep.Name] = now
		}
	}
	s.mu.Unlock()

	if len(dueEndpoints) == 0 {
		return
	}

	s.pollEndpoints(ctx, dueEndpoints)
}

// pollEndpoints polls a subset of endpoints concurrently, respecting maxConcurrency.
func (s *Scheduler) pollEndpoints(ctx context.Context, endpoints []EndpointInfo) {
	jobs := make(chan EndpointInfo, len(endpoints))

	var wg sync.WaitGroup
	for i := 0; i < s.maxConcurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ep := range jobs {
				result := s.pollEndpoint(ctx, ep)
				select {
				case s.results <- result:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	for _, ep := range endpoints {
		select {
		case jobs <- ep:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return
		}
	}
	close(jobs)

	wg.Wait()
}

// pollEndpoint polls a single endpoint and returns the result.
func (s *Scheduler) pollEndpoint(ctx context.Context, ep EndpointInfo) StatusResult {
	resp := s.client.Fetch(ctx, ep.Method, ep.URL, ep.Headers, ep.Timeout)

	result := StatusResult{
		EndpointName: ep.Name,
		URL:          ep.URL,
		Labels:       ep.Labels,
		Latency:      resp.Latency,
		CheckedAt:    time.Now(),
		RawResponse:  resp.Body,
		StatusCode:   resp.StatusCode,
		Error:        resp.Error,
	}

	if resp.Error != nil {
		result.Status = "down"
	} else if ep.Extractor != nil {
		status, err := s.safeExtract(ep.Extractor, resp.Body, resp.StatusCode)
		result.Status = status
		if err != nil {
			result.Error = err
		}
	} else {
		// default: use HTTP status code
		result.Status = httpStatusToStatus(resp.StatusCode)
	}

	return result
}

// safeExtract calls the extractor with panic recovery.
// If the extractor panics, it logs the full stack trace with a correlation ID
// and returns "down" status with a user-friendly error containing the ID.
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

			status = "down"
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
	default:
		return "down"
	}
}
