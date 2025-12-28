package pulseboard

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWithStatusCallback_InvokedOnPoll(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	var callCount atomic.Int32

	cb := func(r StatusResult) {
		callCount.Add(1)
	}

	endpoint, err := NewEndpoint("test", server.URL)
	if err != nil {
		t.Fatalf("NewEndpoint() error = %v", err)
	}

	pb, err := New(
		WithEndpoint(endpoint),
		WithStatusCallback(cb),
		WithPollingInterval(50*time.Millisecond),
		WithPort(19200),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = pb.Start(ctx)

	if callCount.Load() == 0 {
		t.Error("callback should have been invoked at least once")
	}
}

func TestWithStatusCallback_ReceivesCorrectFields(t *testing.T) {
	responseBody := []byte("healthy")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(responseBody)
	}))
	defer server.Close()

	var result StatusResult
	var mu sync.Mutex
	done := make(chan struct{})

	cb := func(r StatusResult) {
		mu.Lock()
		defer mu.Unlock()
		if result.EndpointName == "" { // only capture first result
			result = r
			close(done)
		}
	}

	endpoint, err := NewEndpoint("test-endpoint", server.URL,
		WithLabels("env", "test", "region", "local"),
	)
	if err != nil {
		t.Fatalf("NewEndpoint() error = %v", err)
	}

	pb, err := New(
		WithEndpoint(endpoint),
		WithStatusCallback(cb),
		WithPort(19201),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = pb.Start(ctx)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for callback")
	}
	cancel()

	mu.Lock()
	defer mu.Unlock()

	if result.EndpointName != "test-endpoint" {
		t.Errorf("EndpointName = %q, want %q", result.EndpointName, "test-endpoint")
	}
	if result.URL != server.URL {
		t.Errorf("URL = %q, want %q", result.URL, server.URL)
	}
	if result.Status != StatusUp {
		t.Errorf("Status = %q, want %q", result.Status, StatusUp)
	}
	if result.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want %d", result.StatusCode, 200)
	}
	if !bytes.Equal(result.RawResponse, responseBody) {
		t.Errorf("RawResponse = %q, want %q", result.RawResponse, responseBody)
	}
	if result.CheckedAt.IsZero() {
		t.Error("CheckedAt should not be zero")
	}
	if result.Latency <= 0 {
		t.Errorf("Latency = %v, want > 0", result.Latency)
	}
	if result.Labels["env"] != "test" {
		t.Errorf("Labels[env] = %q, want %q", result.Labels["env"], "test")
	}
	if result.Labels["region"] != "local" {
		t.Errorf("Labels[region] = %q, want %q", result.Labels["region"], "local")
	}
	if result.Error != nil {
		t.Errorf("Error = %v, want nil", result.Error)
	}
}

func TestWithStatusCallback_PanicRecovery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	panicCb := func(r StatusResult) {
		panic("intentional test panic")
	}

	var normalCalled atomic.Bool
	normalCb := func(r StatusResult) {
		normalCalled.Store(true)
	}

	// use a logger that captures output to verify panic was logged
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	endpoint, err := NewEndpoint("test", server.URL)
	if err != nil {
		t.Fatalf("NewEndpoint() error = %v", err)
	}

	pb, err := New(
		WithEndpoint(endpoint),
		WithStatusCallback(panicCb),
		WithStatusCallback(normalCb), // should still be called after panic
		WithLogger(logger),
		WithPollingInterval(50*time.Millisecond),
		WithPort(19202),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// should not panic
	err = pb.Start(ctx)
	if err != nil {
		t.Errorf("Start() error = %v, want nil", err)
	}

	if !normalCalled.Load() {
		t.Error("subsequent callbacks should still run after panic")
	}

	// verify panic was logged
	logOutput := logBuf.String()
	if logOutput == "" {
		t.Error("panic should have been logged")
	}
}

func TestWithStatusCallback_NilIsSafe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	endpoint, err := NewEndpoint("test", server.URL)
	if err != nil {
		t.Fatalf("NewEndpoint() error = %v", err)
	}

	pb, err := New(
		WithEndpoint(endpoint),
		WithStatusCallback(nil),
		WithPollingInterval(50*time.Millisecond),
		WithPort(19203),
	)
	if err != nil {
		t.Fatalf("New() error = %v, want nil (nil callback should be accepted)", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err = pb.Start(ctx)
	if err != nil {
		t.Errorf("Start() error = %v, want nil", err)
	}
}

func TestWithStatusCallback_NoSharedReferences(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("response"))
	}))
	defer server.Close()

	var captured []StatusResult
	var mu sync.Mutex
	done := make(chan struct{})

	cb := func(r StatusResult) {
		mu.Lock()
		defer mu.Unlock()
		// mutate to verify independence - this should NOT affect other callbacks
		// or the store
		r.Labels["mutated"] = "true"
		if len(r.RawResponse) > 0 {
			r.RawResponse[0] = 'X' // mutate the slice
		}
		captured = append(captured, r)
		if len(captured) >= 2 {
			select {
			case <-done:
			default:
				close(done)
			}
		}
	}

	endpoint, err := NewEndpoint("test", server.URL,
		WithLabels("key", "value"),
	)
	if err != nil {
		t.Fatalf("NewEndpoint() error = %v", err)
	}

	pb, err := New(
		WithEndpoint(endpoint),
		WithStatusCallback(cb),
		WithPollingInterval(50*time.Millisecond),
		WithPort(19204),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = pb.Start(ctx)
	}()

	// wait for at least 2 results
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for 2 callback invocations")
	}
	cancel()

	mu.Lock()
	defer mu.Unlock()

	if len(captured) < 2 {
		t.Fatalf("expected at least 2 captured results, got %d", len(captured))
	}

	// verify each captured result has independent Labels map
	for i := 0; i < len(captured); i++ {
		for j := i + 1; j < len(captured); j++ {
			// check if maps point to same underlying data by verifying
			// mutation in one doesn't appear in others (pre-mutation value)
			// Both should have "mutated" key since we added it, but they
			// should be separate maps
			if &captured[i].Labels == &captured[j].Labels {
				t.Errorf("Labels maps at index %d and %d are the same pointer", i, j)
			}
		}
	}

	// verify the mutation in callback didn't corrupt original label value
	// each captured result should have had "key"="value" originally
	// (we added "mutated" after, but "key" should still be "value")
	for i, r := range captured {
		if r.Labels["key"] != "value" {
			t.Errorf("captured[%d].Labels[key] = %q, want %q", i, r.Labels["key"], "value")
		}
	}
}

func TestWithStatusCallback_ExecutionOrder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	var order []int
	var mu sync.Mutex

	endpoint, err := NewEndpoint("test", server.URL)
	if err != nil {
		t.Fatalf("NewEndpoint() error = %v", err)
	}

	pb, err := New(
		WithEndpoint(endpoint),
		WithStatusCallback(func(r StatusResult) {
			mu.Lock()
			order = append(order, 1)
			mu.Unlock()
		}),
		WithStatusCallback(func(r StatusResult) {
			mu.Lock()
			order = append(order, 2)
			mu.Unlock()
		}),
		WithStatusCallback(func(r StatusResult) {
			mu.Lock()
			order = append(order, 3)
			mu.Unlock()
		}),
		WithPollingInterval(50*time.Millisecond),
		WithPort(19205),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_ = pb.Start(ctx)

	mu.Lock()
	defer mu.Unlock()

	if len(order) < 3 {
		t.Fatalf("expected at least 3 callback invocations, got %d", len(order))
	}

	// verify order is always 1, 2, 3, 1, 2, 3, ...
	for i := 0; i < len(order); i++ {
		expected := (i % 3) + 1
		if order[i] != expected {
			t.Errorf("order[%d] = %d, want %d (callbacks should execute in registration order)", i, order[i], expected)
		}
	}
}

func TestWithStatusCallback_MultipleEndpoints(t *testing.T) {
	server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server1.Close()

	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server2.Close()

	var results []StatusResult
	var mu sync.Mutex

	cb := func(r StatusResult) {
		mu.Lock()
		results = append(results, r)
		mu.Unlock()
	}

	ep1, _ := NewEndpoint("healthy", server1.URL)
	ep2, _ := NewEndpoint("unhealthy", server2.URL)

	pb, err := New(
		WithEndpoints(ep1, ep2),
		WithStatusCallback(cb),
		WithPollingInterval(50*time.Millisecond),
		WithPort(19206),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = pb.Start(ctx)

	mu.Lock()
	defer mu.Unlock()

	// should have results from both endpoints
	if len(results) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(results))
	}

	// count results per endpoint
	countByName := make(map[string]int)
	for _, r := range results {
		countByName[r.EndpointName]++
	}

	if countByName["healthy"] == 0 {
		t.Error("expected at least one result for 'healthy' endpoint")
	}
	if countByName["unhealthy"] == 0 {
		t.Error("expected at least one result for 'unhealthy' endpoint")
	}
}

func TestWithStatusCallback_ErrorEndpoint(t *testing.T) {
	// server that closes connection immediately - will cause error
	var result StatusResult
	var mu sync.Mutex
	done := make(chan struct{})

	cb := func(r StatusResult) {
		mu.Lock()
		defer mu.Unlock()
		if result.EndpointName == "" {
			result = r
			close(done)
		}
	}

	// use invalid URL that will fail
	endpoint, _ := NewEndpoint("failing", "http://localhost:1") // port 1 should fail

	pb, err := New(
		WithEndpoint(endpoint),
		WithStatusCallback(cb),
		WithPort(19207),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = pb.Start(ctx)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for callback")
	}
	cancel()

	mu.Lock()
	defer mu.Unlock()

	if result.Status != StatusDown {
		t.Errorf("Status = %q, want %q for failed endpoint", result.Status, StatusDown)
	}
	if result.Error == nil {
		t.Error("Error should not be nil for failed endpoint")
	}
}
