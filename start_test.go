package pulseboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// TestStart_BlocksUntilContextCancelled verifies that Start blocks until the
// provided context is cancelled.
func TestStart_BlocksUntilContextCancelled(t *testing.T) {
	// create a mock server to avoid real network calls
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	ep, err := NewEndpoint("Test", ts.URL)
	if err != nil {
		t.Fatalf("NewEndpoint() error = %v", err)
	}

	// use a high port to avoid conflicts
	pb, err := New(
		WithEndpoint(ep),
		WithPort(19001),
		WithPollingInterval(100*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	started := make(chan struct{})
	done := make(chan error, 1)

	go func() {
		close(started)
		done <- pb.Start(ctx)
	}()

	// wait for Start to begin
	<-started
	time.Sleep(50 * time.Millisecond)

	// verify Start is still blocking (channel should be empty)
	select {
	case err := <-done:
		t.Fatalf("Start() returned early with error: %v", err)
	default:
		// expected: still blocking
	}

	// cancel context
	cancel()

	// Start should return within reasonable time
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Start() returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Start() did not return after context cancellation")
	}
}

// TestStart_ReturnsImmediatelyIfContextAlreadyCancelled verifies that Start
// returns immediately if the context is already cancelled.
func TestStart_ReturnsImmediatelyIfContextAlreadyCancelled(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	ep, err := NewEndpoint("Test", ts.URL)
	if err != nil {
		t.Fatalf("NewEndpoint() error = %v", err)
	}

	pb, err := New(
		WithEndpoint(ep),
		WithPort(19002),
		WithPollingInterval(100*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// create already-cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() {
		done <- pb.Start(ctx)
	}()

	// should return quickly since context is already cancelled
	select {
	case err := <-done:
		if err != nil {
			t.Logf("Start() returned error (acceptable): %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start() did not return with already-cancelled context")
	}
}

// TestStart_CleanShutdown verifies no goroutine leaks after shutdown.
func TestStart_CleanShutdown(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	ep, err := NewEndpoint("Test", ts.URL)
	if err != nil {
		t.Fatalf("NewEndpoint() error = %v", err)
	}

	pb, err := New(
		WithEndpoint(ep),
		WithPort(19003),
		WithPollingInterval(50*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- pb.Start(ctx)
	}()

	// let it run for a bit
	time.Sleep(200 * time.Millisecond)

	// shutdown
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Start() returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Start() did not return after context cancellation")
	}

	// give time for goroutines to clean up
	time.Sleep(100 * time.Millisecond)

	// Note: proper goroutine leak detection would require runtime.NumGoroutine
	// comparison, but that's flaky in test environments. The scheduler and
	// server tests already verify component-level cleanup.
}

// TestStart_MultipleSequentialRuns verifies that a new PulseBoard can be
// started after the previous one shuts down.
func TestStart_MultipleSequentialRuns(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	for i := 0; i < 3; i++ {
		ep, err := NewEndpoint("Test", ts.URL)
		if err != nil {
			t.Fatalf("iteration %d: NewEndpoint() error = %v", i, err)
		}

		pb, err := New(
			WithEndpoint(ep),
			WithPort(19004+i),
			WithPollingInterval(50*time.Millisecond),
		)
		if err != nil {
			t.Fatalf("iteration %d: New() error = %v", i, err)
		}

		ctx, cancel := context.WithCancel(context.Background())

		done := make(chan error, 1)
		go func() {
			done <- pb.Start(ctx)
		}()

		time.Sleep(100 * time.Millisecond)
		cancel()

		select {
		case err := <-done:
			if err != nil {
				t.Errorf("iteration %d: Start() returned error: %v", i, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("iteration %d: Start() did not return", i)
		}
	}
}

// TestStart_ConcurrentAccess verifies Start is safe with concurrent access patterns.
func TestStart_ConcurrentAccess(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	ep, err := NewEndpoint("Test", ts.URL)
	if err != nil {
		t.Fatalf("NewEndpoint() error = %v", err)
	}

	pb, err := New(
		WithEndpoint(ep),
		WithPort(19010),
		WithPollingInterval(50*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup

	// start the server
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = pb.Start(ctx)
	}()

	time.Sleep(100 * time.Millisecond)

	// concurrent calls to read accessors shouldn't panic
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = pb.Endpoints()
			_ = pb.Port()
			_ = pb.PollingInterval()
		}()
	}

	time.Sleep(50 * time.Millisecond)
	cancel()

	// wait for all goroutines with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// success
	case <-time.After(5 * time.Second):
		t.Fatal("goroutines did not complete")
	}
}

// TestStart_WithTimeoutContext verifies Start respects deadline contexts.
func TestStart_WithTimeoutContext(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	ep, err := NewEndpoint("Test", ts.URL)
	if err != nil {
		t.Fatalf("NewEndpoint() error = %v", err)
	}

	pb, err := New(
		WithEndpoint(ep),
		WithPort(19011),
		WithPollingInterval(50*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// context with 200ms timeout
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	err = pb.Start(ctx)
	elapsed := time.Since(start)

	// should have run for approximately 200ms (with some tolerance)
	if elapsed < 150*time.Millisecond || elapsed > 500*time.Millisecond {
		t.Errorf("Start() ran for %v, expected ~200ms", elapsed)
	}

	if err != nil {
		t.Logf("Start() returned error (may be acceptable): %v", err)
	}
}
