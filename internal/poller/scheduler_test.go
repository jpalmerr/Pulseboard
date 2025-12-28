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
// does not crash the scheduler. Instead, it should return status "down"
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
	var result StatusResult
	select {
	case result = <-scheduler.Results():
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for poll result")
	}

	scheduler.Stop()

	// verify panic was recovered and status is "down"
	if result.Status != "down" {
		t.Errorf("Status = %q, want %q", result.Status, "down")
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
	results := make(map[string]StatusResult)
	for i := 0; i < 2; i++ {
		select {
		case result := <-scheduler.Results():
			results[result.EndpointName] = result
		case <-time.After(5 * time.Second):
			t.Fatalf("timeout waiting for result %d", i+1)
		}
	}

	scheduler.Stop()

	// verify panicking endpoint returned "down"
	if results["Panicking"].Status != "down" {
		t.Errorf("Panicking.Status = %q, want %q", results["Panicking"].Status, "down")
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
		panic(nil)
	}

	endpoints := []EndpointInfo{{
		Name:      "Nil Panic Test",
		URL:       server.URL,
		Extractor: nilPanicExtractor,
		Timeout:   time.Second,
	}}

	scheduler := NewScheduler(endpoints, time.Hour, 1, testLogger())
	scheduler.Start(context.Background())

	var result StatusResult
	select {
	case result = <-scheduler.Results():
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for poll result")
	}

	scheduler.Stop()

	// verify panic was recovered and status is "down"
	if result.Status != "down" {
		t.Errorf("Status = %q, want %q", result.Status, "down")
	}

	// error should still be set even for nil panic
	if result.Error == nil {
		t.Fatal("Error = nil, want error for nil panic")
	}
}

// TestScheduler_GCDCalculation verifies that the base tick interval is
// calculated correctly as the GCD of all endpoint intervals.
func TestScheduler_GCDCalculation(t *testing.T) {
	tests := []struct {
		name           string
		intervals      []time.Duration
		globalInterval time.Duration
		expectedBase   time.Duration
	}{
		{
			name:           "all same interval",
			intervals:      []time.Duration{10 * time.Second, 10 * time.Second},
			globalInterval: 10 * time.Second,
			expectedBase:   10 * time.Second,
		},
		{
			name:           "5s and 10s gives GCD of 5s",
			intervals:      []time.Duration{5 * time.Second, 10 * time.Second},
			globalInterval: 30 * time.Second,
			expectedBase:   5 * time.Second,
		},
		{
			name:           "with zero (default) uses global",
			intervals:      []time.Duration{6 * time.Second, 0}, // 0 = use global
			globalInterval: 9 * time.Second,
			expectedBase:   3 * time.Second, // GCD(6, 9) = 3
		},
		{
			name:           "all use default",
			intervals:      []time.Duration{0, 0, 0},
			globalInterval: 15 * time.Second,
			expectedBase:   15 * time.Second,
		},
		{
			name:           "co-prime intervals",
			intervals:      []time.Duration{7 * time.Second, 11 * time.Second},
			globalInterval: 30 * time.Second,
			expectedBase:   1 * time.Second, // GCD(7, 11) = 1
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			endpoints := make([]EndpointInfo, len(tt.intervals))
			for i, interval := range tt.intervals {
				endpoints[i] = EndpointInfo{
					Name:     fmt.Sprintf("ep%d", i),
					URL:      "http://example.com",
					Timeout:  time.Second,
					Interval: interval,
				}
			}

			scheduler := NewScheduler(endpoints, tt.globalInterval, 1, testLogger())
			base := scheduler.calculateBaseInterval()

			if base != tt.expectedBase {
				t.Errorf("calculateBaseInterval() = %v, want %v", base, tt.expectedBase)
			}
		})
	}
}

// TestScheduler_GCDCalculation_EmptyEndpoints verifies that an empty endpoint
// list returns the global interval as the base.
func TestScheduler_GCDCalculation_EmptyEndpoints(t *testing.T) {
	globalInterval := 20 * time.Second
	scheduler := NewScheduler([]EndpointInfo{}, globalInterval, 1, testLogger())
	base := scheduler.calculateBaseInterval()

	if base != globalInterval {
		t.Errorf("calculateBaseInterval() = %v, want %v (global)", base, globalInterval)
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
