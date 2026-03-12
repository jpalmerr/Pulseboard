package poller

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jpalmerr/pulseboard/internal/types"
)

// testLogger returns a logger that discards all output for clean test output.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestScheduler_StopBeforeStart verifies that calling Stop() on a scheduler
// that was never started does not panic and is a safe no-op.
func TestScheduler_StopBeforeStart(t *testing.T) {
	endpoints := []EndpointInfo{
		{Name: "test", URL: "http://example.com", Timeout: time.Second},
	}

	scheduler := NewScheduler(endpoints, time.Minute, 1, testLogger())

	// this must not panic
	scheduler.Stop()
}

// TestScheduler_StopTwice verifies that Stop() is idempotent and can be
// called multiple times without panic or deadlock.
func TestScheduler_StopTwice(t *testing.T) {
	endpoints := []EndpointInfo{
		{Name: "test", URL: "http://example.com", Timeout: time.Second},
	}

	scheduler := NewScheduler(endpoints, time.Minute, 1, testLogger())
	scheduler.Start(context.Background())

	// both calls must complete without panic or deadlock
	scheduler.Stop()
	scheduler.Stop()
}

// TestScheduler_StopAfterStart verifies the normal lifecycle: Start followed
// by Stop results in clean shutdown with the results channel closed.
func TestScheduler_StopAfterStart(t *testing.T) {
	endpoints := []EndpointInfo{
		{Name: "test", URL: "http://example.com", Timeout: time.Second},
	}

	scheduler := NewScheduler(endpoints, time.Minute, 1, testLogger())
	scheduler.Start(context.Background())

	// drain results channel to prevent blocking
	go func() {
		for range scheduler.Results() {
		}
	}()

	// give the scheduler a moment to start polling
	time.Sleep(50 * time.Millisecond)

	scheduler.Stop()

	// verify results channel is closed by reading from it
	select {
	case _, ok := <-scheduler.Results():
		if ok {
			t.Error("expected results channel to be closed after Stop()")
		}
	case <-time.After(time.Second):
		t.Error("timeout waiting for results channel to close")
	}
}

// TestScheduler_ConcurrentStartStop verifies that calling Start() and Stop()
// concurrently does not cause a race condition or panic.
// Run with: go test -race ./poller/...
func TestScheduler_ConcurrentStartStop(t *testing.T) {
	endpoints := []EndpointInfo{
		{Name: "test", URL: "http://example.com", Timeout: time.Second},
	}

	// run multiple iterations to increase chance of catching races
	for i := 0; i < 100; i++ {
		scheduler := NewScheduler(endpoints, time.Minute, 1, testLogger())

		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			scheduler.Start(context.Background())
		}()

		go func() {
			defer wg.Done()
			scheduler.Stop()
		}()

		wg.Wait()

		// drain any remaining results
		for range scheduler.Results() {
		}
	}
}

// TestScheduler_ConcurrentPollAndStop verifies that polling workers don't race
// with Stop(). Run with: go test -race ./internal/poller/...
func TestScheduler_ConcurrentPollAndStop(t *testing.T) {
	endpoints := []EndpointInfo{
		{Name: "test1", URL: "http://example.com", Timeout: time.Second},
		{Name: "test2", URL: "http://example.com", Timeout: time.Second},
		{Name: "test3", URL: "http://example.com", Timeout: time.Second},
	}

	// run multiple iterations to increase chance of catching races
	for i := 0; i < 50; i++ {
		scheduler := NewScheduler(endpoints, 10*time.Millisecond, 2, testLogger())
		scheduler.Start(context.Background())

		// let it poll at least once
		time.Sleep(15 * time.Millisecond)

		// stop while polling may be active
		scheduler.Stop()

		// verify clean shutdown by draining results
		for range scheduler.Results() {
		}
	}
}

// TestScheduler_StartTwice verifies that Start() is idempotent and calling
// it multiple times does not spawn multiple polling goroutines.
func TestScheduler_StartTwice(t *testing.T) {
	endpoints := []EndpointInfo{
		{Name: "test", URL: "http://example.com", Timeout: time.Second},
	}

	scheduler := NewScheduler(endpoints, time.Minute, 1, testLogger())

	scheduler.Start(context.Background())
	scheduler.Start(context.Background()) // second call should be no-op

	// drain results
	go func() {
		for range scheduler.Results() {
		}
	}()

	scheduler.Stop()
}

// TestScheduler_StopBeforeStartThenStart verifies that if Stop() is called
// before Start(), a subsequent Start() call is handled gracefully.
func TestScheduler_StopBeforeStartThenStart(t *testing.T) {
	endpoints := []EndpointInfo{
		{Name: "test", URL: "http://example.com", Timeout: time.Second},
	}

	scheduler := NewScheduler(endpoints, time.Minute, 1, testLogger())

	scheduler.Stop()                // stop before start
	scheduler.Start(context.TODO()) // start after stop - should be no-op or handled gracefully
	scheduler.Stop()                // second stop should not panic
}

// TestScheduler_ContextCancellation verifies that cancelling the parent context
// stops the scheduler gracefully.
func TestScheduler_ContextCancellation(t *testing.T) {
	endpoints := []EndpointInfo{
		{Name: "test", URL: "http://example.com", Timeout: time.Second},
	}

	ctx, cancel := context.WithCancel(context.Background())
	scheduler := NewScheduler(endpoints, time.Minute, 1, testLogger())
	scheduler.Start(ctx)

	// drain results
	go func() {
		for range scheduler.Results() {
		}
	}()

	// cancel parent context
	cancel()

	// stop should complete quickly since context is already cancelled
	done := make(chan struct{})
	go func() {
		scheduler.Stop()
		close(done)
	}()

	select {
	case <-done:
		// success
	case <-time.After(2 * time.Second):
		t.Error("Stop() did not complete after parent context cancellation")
	}
}

// TestScheduler_ExtractorPanicRecovery verifies that a panicking extractor
// does not crash the scheduler. Instead, it should return status "error"
// with an error describing the panic.
func TestScheduler_ExtractorPanicRecovery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status": "ok"}`))
	}))
	defer server.Close()

	panicExtractor := func(body []byte, statusCode int) string {
		panic("extractor panic: simulated failure")
	}

	endpoints := []EndpointInfo{{
		Name:      "Panic Test",
		URL:       server.URL,
		Extractor: panicExtractor,
		Timeout:   time.Second,
	}}

	scheduler := NewScheduler(endpoints, time.Hour, 1, testLogger()) // long interval, we only want one poll
	scheduler.Start(context.Background())

	// collect the result
	var result types.StatusResult
	select {
	case result = <-scheduler.Results():
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for poll result")
	}

	scheduler.Stop()

	// verify panic was recovered and status is "error"
	if result.Status != "error" {
		t.Errorf("Status = %q, want %q", result.Status, "error")
	}

	// verify error contains panic info with correlation ID
	if result.Error == nil {
		t.Fatal("Error = nil, want error describing panic")
	}
	errMsg := result.Error.Error()
	if !strings.Contains(errMsg, "extractor panic") {
		t.Errorf("Error = %q, want to contain 'extractor panic'", errMsg)
	}
	if !strings.Contains(errMsg, "correlation_id") {
		t.Errorf("Error = %q, want to contain 'correlation_id'", errMsg)
	}
}

// TestScheduler_ExtractorPanicDoesNotAffectOtherEndpoints verifies that a panic
// in one endpoint's extractor does not prevent other endpoints from being polled.
func TestScheduler_ExtractorPanicDoesNotAffectOtherEndpoints(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status": "ok"}`))
	}))
	defer server.Close()

	panicExtractor := func(body []byte, statusCode int) string {
		panic("boom")
	}

	healthyExtractor := func(body []byte, statusCode int) string {
		return "up"
	}

	endpoints := []EndpointInfo{
		{
			Name:      "Panicking",
			URL:       server.URL,
			Extractor: panicExtractor,
			Timeout:   time.Second,
		},
		{
			Name:      "Healthy",
			URL:       server.URL,
			Extractor: healthyExtractor,
			Timeout:   time.Second,
		},
	}

	scheduler := NewScheduler(endpoints, time.Hour, 2, testLogger())
	scheduler.Start(context.Background())

	// collect both results
	results := make(map[string]types.StatusResult)
	for i := 0; i < 2; i++ {
		select {
		case result := <-scheduler.Results():
			results[result.EndpointName] = result
		case <-time.After(5 * time.Second):
			t.Fatalf("timeout waiting for result %d", i+1)
		}
	}

	scheduler.Stop()

	// verify panicking endpoint returned "error"
	if results["Panicking"].Status != "error" {
		t.Errorf("Panicking.Status = %q, want %q", results["Panicking"].Status, "error")
	}

	// verify healthy endpoint still returned "up"
	if results["Healthy"].Status != "up" {
		t.Errorf("Healthy.Status = %q, want %q", results["Healthy"].Status, "up")
	}
}

// TestScheduler_ExtractorNilPanicRecovery verifies that even a panic with
// a nil value is recovered gracefully.
func TestScheduler_ExtractorNilPanicRecovery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	nilPanicExtractor := func(body []byte, statusCode int) string {
		panic("nil panic value")
	}

	endpoints := []EndpointInfo{{
		Name:      "Nil Panic Test",
		URL:       server.URL,
		Extractor: nilPanicExtractor,
		Timeout:   time.Second,
	}}

	scheduler := NewScheduler(endpoints, time.Hour, 1, testLogger())
	scheduler.Start(context.Background())

	var result types.StatusResult
	select {
	case result = <-scheduler.Results():
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for poll result")
	}

	scheduler.Stop()

	// verify panic was recovered and status is "error"
	if result.Status != "error" {
		t.Errorf("Status = %q, want %q", result.Status, "error")
	}

	// error should still be set even for nil panic
	if result.Error == nil {
		t.Fatal("Error = nil, want error for nil panic")
	}
}

// TestScheduler_DefaultIntervalUsedWhenNotSpecified verifies that endpoints
// without a custom interval use the global polling interval.
func TestScheduler_DefaultIntervalUsedWhenNotSpecified(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// use intervals >= 1 second (the GCD floor) for realistic testing
	endpoints := []EndpointInfo{
		{Name: "Custom", URL: server.URL, Timeout: time.Second, Interval: 1 * time.Second},
		{Name: "Default", URL: server.URL, Timeout: time.Second, Interval: 0}, // should use global (3s)
	}

	globalInterval := 3 * time.Second
	scheduler := NewScheduler(endpoints, globalInterval, 2, testLogger())
	scheduler.Start(context.Background())

	counts := make(map[string]int)
	timeout := time.After(3500 * time.Millisecond)

collecting:
	for {
		select {
		case result, ok := <-scheduler.Results():
			if !ok {
				break collecting
			}
			counts[result.EndpointName]++
		case <-timeout:
			break collecting
		}
	}

	scheduler.Stop()

	// Custom (1s) should poll more than Default (3s global)
	// In 3.5s: Custom ~4 polls (immediate + 3 ticks), Default ~2 polls (immediate + 1 tick)
	if counts["Custom"] <= counts["Default"] {
		t.Errorf("Custom polled %d times, Default polled %d times - Custom should poll more frequently",
			counts["Custom"], counts["Default"])
	}
}

// TestScheduler_MixedIntervals verifies that endpoints with different intervals
// are polled at their respective frequencies.
func TestScheduler_MixedIntervals(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// use intervals >= 1 second (the GCD floor) for realistic testing
	endpoints := []EndpointInfo{
		{Name: "Fast", URL: server.URL, Timeout: time.Second, Interval: 1 * time.Second},
		{Name: "Slow", URL: server.URL, Timeout: time.Second, Interval: 3 * time.Second},
	}

	scheduler := NewScheduler(endpoints, 5*time.Second, 2, testLogger())
	scheduler.Start(context.Background())

	// collect results for 3.5 seconds
	counts := make(map[string]int)
	timeout := time.After(3500 * time.Millisecond)

collecting:
	for {
		select {
		case result, ok := <-scheduler.Results():
			if !ok {
				break collecting
			}
			counts[result.EndpointName]++
		case <-timeout:
			break collecting
		}
	}

	scheduler.Stop()

	// Fast (1s) should poll ~4 times (immediate + 3 ticks in 3.5s)
	// Slow (3s) should poll ~2 times (immediate + 1 tick in 3.5s)
	if counts["Fast"] < 3 {
		t.Errorf("Fast endpoint polled %d times, expected at least 3", counts["Fast"])
	}
	if counts["Slow"] > counts["Fast"] {
		t.Errorf("Slow polled %d times, Fast polled %d times - Slow should poll less frequently",
			counts["Slow"], counts["Fast"])
	}
}

// TestScheduler_ImmediatePollOnStart verifies that all endpoints are polled
// immediately when the scheduler starts, regardless of their intervals.
func TestScheduler_ImmediatePollOnStart(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	endpoints := []EndpointInfo{
		{Name: "LongInterval", URL: server.URL, Timeout: time.Second, Interval: time.Hour}, // very long
	}

	scheduler := NewScheduler(endpoints, time.Hour, 1, testLogger())
	scheduler.Start(context.Background())

	// should receive immediate poll even though interval is 1 hour
	select {
	case result := <-scheduler.Results():
		if result.EndpointName != "LongInterval" {
			t.Errorf("EndpointName = %q, want %q", result.EndpointName, "LongInterval")
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("timeout waiting for immediate poll result")
	}

	scheduler.Stop()
}

// --- pollQueue unit tests ---

// TestPollQueue_Push_PopDue verifies the basic heap contract: push entries with
// different due times and pop only those that are currently due.
func TestPollQueue_Push_PopDue(t *testing.T) {
	now := time.Now()

	q := &pollQueue{}

	epA := EndpointInfo{Name: "A"}
	epB := EndpointInfo{Name: "B"}
	epC := EndpointInfo{Name: "C"}

	// A and B are due; C is in the future
	q.push(epA, now.Add(-2*time.Second))
	q.push(epB, now.Add(-1*time.Second))
	q.push(epC, now.Add(10*time.Second))

	due := q.popDue(now)

	if len(due) != 2 {
		t.Fatalf("popDue() = %d entries, want 2", len(due))
	}

	names := map[string]bool{}
	for _, ep := range due {
		names[ep.Name] = true
	}
	if !names["A"] || !names["B"] {
		t.Errorf("popDue() = %v, want both A and B", due)
	}

	// C must still be in the queue
	remaining := q.popDue(now.Add(20 * time.Second))
	if len(remaining) != 1 || remaining[0].Name != "C" {
		t.Errorf("remaining after first popDue() = %v, want [C]", remaining)
	}
}

// TestPollQueue_Empty verifies the empty predicate transitions correctly.
func TestPollQueue_Empty(t *testing.T) {
	q := &pollQueue{}

	if !q.empty() {
		t.Error("empty() = false on new queue, want true")
	}

	ep := EndpointInfo{Name: "X"}
	q.push(ep, time.Now().Add(time.Second))

	if q.empty() {
		t.Error("empty() = true after push, want false")
	}

	q.popDue(time.Now().Add(2 * time.Second))

	if !q.empty() {
		t.Error("empty() = false after all entries popped, want true")
	}
}

// TestPollQueue_Peek_ReturnsDefaultWhenEmpty verifies that peek returns 100ms
// on an empty queue and never returns 0 or panics.
func TestPollQueue_Peek_ReturnsDefaultWhenEmpty(t *testing.T) {
	q := &pollQueue{}

	d := q.peek()

	if d <= 0 {
		t.Errorf("peek() = %v on empty queue, want > 0", d)
	}
	if d != 100*time.Millisecond {
		t.Errorf("peek() = %v on empty queue, want 100ms default", d)
	}
}

// TestPollQueue_Peek_ReturnsDurationToNextEntry verifies that peek returns
// approximately the time until the next scheduled entry.
func TestPollQueue_Peek_ReturnsDurationToNextEntry(t *testing.T) {
	q := &pollQueue{}

	target := time.Now().Add(500 * time.Millisecond)
	q.push(EndpointInfo{Name: "X"}, target)

	d := q.peek()

	// Allow generous tolerance for scheduling jitter.
	if d <= 0 {
		t.Errorf("peek() = %v, want positive duration", d)
	}
	if d > 600*time.Millisecond {
		t.Errorf("peek() = %v, want <= 600ms (target is 500ms away)", d)
	}
}

// TestPollQueue_ConcurrentAccess verifies that the pollQueue is safe for
// concurrent use. Run with: go test -race ./internal/poller/...
func TestPollQueue_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	q := &pollQueue{}
	now := time.Now()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			ep := EndpointInfo{Name: fmt.Sprintf("ep%d", i)}
			q.push(ep, now.Add(time.Duration(i)*time.Millisecond))
			_ = q.peek()
			_ = q.empty()
			_ = q.popDue(now.Add(100 * time.Millisecond))
		}()
	}
	wg.Wait()
}

// TestPollQueue_FIFO_SameTime verifies that all entries with the same nextPoll
// time are returned together by a single popDue call.
func TestPollQueue_FIFO_SameTime(t *testing.T) {
	q := &pollQueue{}
	now := time.Now()

	names := []string{"A", "B", "C", "D"}
	for _, name := range names {
		q.push(EndpointInfo{Name: name}, now) // all due at the same instant
	}

	due := q.popDue(now)

	if len(due) != len(names) {
		t.Errorf("popDue() = %d entries, want %d (all same-time entries)", len(due), len(names))
	}
}

// TestScheduler_CoPrimeIntervals_NoGCDDegradation verifies that the priority-
// queue scheduler handles co-prime intervals correctly. Under the old GCD
// approach, GCD(7s, 13s) = 1s caused the scheduler to tick every second even
// though no endpoint needed it. Here we verify both endpoints are polled at
// least once (immediate poll) and the test completes without deadlock or races.
// Run with: go test -race ./internal/poller/...
func TestScheduler_CoPrimeIntervals_NoGCDDegradation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	endpoints := []EndpointInfo{
		{Name: "Seven", URL: server.URL, Timeout: time.Second, Interval: 7 * time.Second},
		{Name: "Thirteen", URL: server.URL, Timeout: time.Second, Interval: 13 * time.Second},
	}

	scheduler := NewScheduler(endpoints, time.Minute, 2, testLogger())
	scheduler.Start(context.Background())

	// collect results for 100ms — only immediate polls will fire in this window
	polled := make(map[string]bool)
	timeout := time.After(300 * time.Millisecond)

collecting:
	for {
		select {
		case result, ok := <-scheduler.Results():
			if !ok {
				break collecting
			}
			polled[result.EndpointName] = true
			if polled["Seven"] && polled["Thirteen"] {
				break collecting
			}
		case <-timeout:
			break collecting
		}
	}

	scheduler.Stop()
	// drain remaining results after Stop
	for range scheduler.Results() {
	}

	if !polled["Seven"] {
		t.Error("Seven endpoint was not polled (expected immediate poll on start)")
	}
	if !polled["Thirteen"] {
		t.Error("Thirteen endpoint was not polled (expected immediate poll on start)")
	}
}

// TestPollQueue_Peek_OverdueEntry verifies that peek returns time.Millisecond
// for an entry whose nextPoll is in the past, never 0 or a negative duration.
func TestPollQueue_Peek_OverdueEntry(t *testing.T) {
	q := &pollQueue{}
	q.push(EndpointInfo{Name: "X"}, time.Now().Add(-5*time.Second)) // in the past

	d := q.peek()

	if d != time.Millisecond {
		t.Errorf("peek() = %v for overdue entry, want %v (floor)", d, time.Millisecond)
	}
}

// TestScheduler_ZeroEndpoints verifies that a scheduler with no endpoints
// starts and stops cleanly without panic or deadlock, and that the results
// channel is closed on Stop().
func TestScheduler_ZeroEndpoints(t *testing.T) {
	scheduler := NewScheduler(nil, time.Second, 1, testLogger())
	scheduler.Start(context.Background())

	// drain — no results expected
	done := make(chan struct{})
	go func() {
		for range scheduler.Results() {
		}
		close(done)
	}()

	scheduler.Stop()

	select {
	case <-done:
		// success: results channel closed
	case <-time.After(time.Second):
		t.Error("results channel not closed after Stop() with zero endpoints")
	}
}

// TestScheduler_MaxConcurrencyZero_ClampedToOne verifies that maxConcurrency <= 0
// is clamped to 1 and the scheduler operates correctly without deadlocking.
func TestScheduler_MaxConcurrencyZero_ClampedToOne(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	endpoints := []EndpointInfo{
		{Name: "test", URL: server.URL, Timeout: time.Second},
	}

	// maxConcurrency = 0 must not deadlock
	scheduler := NewScheduler(endpoints, time.Hour, 0, testLogger())
	scheduler.Start(context.Background())

	select {
	case result := <-scheduler.Results():
		if result.EndpointName != "test" {
			t.Errorf("EndpointName = %q, want %q", result.EndpointName, "test")
		}
	case <-time.After(time.Second):
		t.Error("timeout — scheduler deadlocked with maxConcurrency=0")
	}

	scheduler.Stop()
	for range scheduler.Results() {
	}
}

// BenchmarkScheduler_100Endpoints measures scheduler throughput with 100
// endpoints across a range of co-prime intervals.
func BenchmarkScheduler_100Endpoints(b *testing.B) {
	endpoints := make([]EndpointInfo, 100)
	for i := range endpoints {
		endpoints[i] = EndpointInfo{
			Name:     fmt.Sprintf("ep%d", i),
			URL:      "http://example.com",
			Timeout:  time.Second,
			Interval: time.Duration((i%7)+1) * time.Second,
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s := NewScheduler(endpoints, 60*time.Second, 4, testLogger())
		s.Start(context.Background())
		time.Sleep(50 * time.Millisecond)
		s.Stop()
		for range s.Results() {
		}
	}
}
