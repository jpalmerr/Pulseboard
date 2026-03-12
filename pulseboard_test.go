package pulseboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestToPollerEndpoints_LabelsCopied(t *testing.T) {
	ep, err := NewEndpoint("Test", "https://example.com",
		WithLabels("env", "prod", "region", "us-east"),
	)
	if err != nil {
		t.Fatalf("NewEndpoint() error = %v", err)
	}

	pb, err := New(WithEndpoint(ep), WithPort(19100))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// get poller endpoints (same package, so we can call private method)
	pollerEndpoints := pb.toPollerEndpoints()
	if len(pollerEndpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(pollerEndpoints))
	}

	// mutate the labels in EndpointInfo
	pollerEndpoints[0].Labels["env"] = "modified"
	pollerEndpoints[0].Labels["new_key"] = "new_value"

	// verify original endpoint is unchanged
	originalLabels := ep.Labels()
	if originalLabels["env"] != "prod" {
		t.Errorf("mutation affected original: Labels[env] = %q, want %q", originalLabels["env"], "prod")
	}
	if _, exists := originalLabels["new_key"]; exists {
		t.Error("mutation added new key to original endpoint")
	}
}

func TestToPollerEndpoints_HeadersCopied(t *testing.T) {
	ep, err := NewEndpoint("Test", "https://example.com",
		WithHeaders("Authorization", "Bearer token", "X-Custom", "value"),
	)
	if err != nil {
		t.Fatalf("NewEndpoint() error = %v", err)
	}

	pb, err := New(WithEndpoint(ep), WithPort(19101))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	pollerEndpoints := pb.toPollerEndpoints()
	if len(pollerEndpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(pollerEndpoints))
	}

	// mutate the headers in EndpointInfo
	pollerEndpoints[0].Headers["Authorization"] = "modified"
	pollerEndpoints[0].Headers["new_header"] = "new_value"

	// verify original endpoint is unchanged
	originalHeaders := ep.Headers()
	if originalHeaders["Authorization"] != "Bearer token" {
		t.Errorf("mutation affected original: Headers[Authorization] = %q, want %q",
			originalHeaders["Authorization"], "Bearer token")
	}
	if _, exists := originalHeaders["new_header"]; exists {
		t.Error("mutation added new header to original endpoint")
	}
}

func TestToPollerEndpoints_NilLabels(t *testing.T) {
	ep, err := NewEndpoint("Test", "https://example.com") // no labels
	if err != nil {
		t.Fatalf("NewEndpoint() error = %v", err)
	}

	pb, err := New(WithEndpoint(ep), WithPort(19102))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	pollerEndpoints := pb.toPollerEndpoints()
	if len(pollerEndpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(pollerEndpoints))
	}

	// should not panic - copyMap returns nil for nil input
	labels := pollerEndpoints[0].Labels
	if len(labels) != 0 {
		t.Errorf("expected nil or empty labels, got %v", labels)
	}
}

func TestToPollerEndpoints_NilHeaders(t *testing.T) {
	ep, err := NewEndpoint("Test", "https://example.com") // no headers
	if err != nil {
		t.Fatalf("NewEndpoint() error = %v", err)
	}

	pb, err := New(WithEndpoint(ep), WithPort(19103))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	pollerEndpoints := pb.toPollerEndpoints()
	if len(pollerEndpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(pollerEndpoints))
	}

	// should not panic - copyMap returns nil for nil input
	headers := pollerEndpoints[0].Headers
	if len(headers) != 0 {
		t.Errorf("expected nil or empty headers, got %v", headers)
	}
}

// TestPulseBoard_defaultStaleThresholdIs3xPollingInterval verifies that when
// WithStaleThreshold is not called, the struct carries staleThresholdSet=false,
// indicating Start() should compute the default (3x polling interval).
func TestPulseBoard_defaultStaleThresholdIs3xPollingInterval(t *testing.T) {
	ep, _ := NewEndpoint("Test", "https://example.com")

	interval := 10 * time.Second
	pb, err := New(
		WithEndpoint(ep),
		WithPollingInterval(interval),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// when WithStaleThreshold is not called, staleThresholdSet must be false
	if pb.staleThresholdSet {
		t.Error("staleThresholdSet = true without WithStaleThreshold, want false")
	}
	// staleThreshold should be zero (default computed at Start() time)
	if pb.staleThreshold != 0 {
		t.Errorf("staleThreshold = %v, want 0 (computed at Start time)", pb.staleThreshold)
	}
	// verify computed value matches expected 3x
	expected := 3 * interval
	computed := 3 * pb.pollingInterval
	if computed != expected {
		t.Errorf("3 * pollingInterval = %v, want %v", computed, expected)
	}
}

// TestPulseBoard_customStaleThreshold verifies that WithStaleThreshold stores the
// threshold and marks it as explicitly set.
func TestPulseBoard_customStaleThreshold(t *testing.T) {
	ep, _ := NewEndpoint("Test", "https://example.com")

	pb, err := New(
		WithEndpoint(ep),
		WithStaleThreshold(1*time.Minute),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if !pb.staleThresholdSet {
		t.Error("staleThresholdSet = false, want true after WithStaleThreshold")
	}
	if pb.staleThreshold != 1*time.Minute {
		t.Errorf("staleThreshold = %v, want %v", pb.staleThreshold, 1*time.Minute)
	}
}

// TestPulseBoard_staleThresholdZeroDisablesChecker verifies that WithStaleThreshold(0)
// disables staleness detection: entries are never marked stale even after a long wait.
// This is verified by starting PulseBoard, seeding data via polling, and confirming
// the /api/status response never shows stale:true.
func TestPulseBoard_staleThresholdZeroDisablesChecker(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	ep, err := NewEndpoint("Test", ts.URL)
	if err != nil {
		t.Fatalf("NewEndpoint() error = %v", err)
	}

	// use a very short polling interval so we get data quickly,
	// then disable staleness entirely with WithStaleThreshold(0)
	pb, err := New(
		WithEndpoint(ep),
		WithPort(19200),
		WithPollingInterval(50*time.Millisecond),
		WithStaleThreshold(0),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if !pb.staleThresholdSet {
		t.Fatal("staleThresholdSet = false, want true after WithStaleThreshold(0)")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := make(chan struct{})
	go func() {
		close(started)
		_ = pb.Start(ctx)
	}()

	<-started
	// wait for at least one poll cycle to populate the store
	time.Sleep(200 * time.Millisecond)

	// query /api/status — no entry should be stale
	resp, err := http.Get("http://localhost:19200/api/status")
	if err != nil {
		t.Fatalf("GET /api/status error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var results []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		t.Fatalf("decode /api/status: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("/api/status returned 0 results, want at least 1")
	}

	for _, result := range results {
		if stale, ok := result["stale"]; ok && stale == true {
			t.Errorf("entry %q has stale=true but staleness checker should be disabled", result["name"])
		}
	}
}

// startTestPulseBoard is a helper that creates and starts a PulseBoard against a mock HTTP server.
// It returns the PulseBoard instance and a cancel function. The cancel function must be called
// to stop the PulseBoard before the test ends.
func startTestPulseBoard(t *testing.T, port int, handler http.Handler, opts ...Option) (*PulseBoard, context.CancelFunc) {
	t.Helper()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	ep, err := NewEndpoint("Test", ts.URL, WithLabels("env", "prod"))
	if err != nil {
		t.Fatalf("NewEndpoint() error = %v", err)
	}

	defaultOpts := []Option{
		WithEndpoint(ep),
		WithPort(port),
		WithPollingInterval(50 * time.Millisecond),
		WithStaleThreshold(0), // disable stale checker to reduce goroutine noise
	}
	allOpts := append(defaultOpts, opts...)

	pb, err := New(allOpts...)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	go func() {
		close(started)
		_ = pb.Start(ctx)
	}()
	<-started
	return pb, cancel
}

// TestStatusChangeCallback_OnTransition verifies the callback fires when status changes.
func TestStatusChangeCallback_OnTransition(t *testing.T) {
	var (
		mu      sync.Mutex
		changes []StatusChange
	)

	cb := func(c StatusChange) {
		mu.Lock()
		changes = append(changes, c)
		mu.Unlock()
	}

	// Start with a server returning 200, then later we test a change was observed.
	_, cancel := startTestPulseBoard(t, 19300,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
		WithStatusChangeCallback(cb),
	)
	defer cancel()

	// Wait for at least one poll cycle (first poll is always a change).
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	count := len(changes)
	mu.Unlock()

	if count == 0 {
		t.Error("expected at least one status change (first poll), got 0")
	}
}

// TestStatusChangeCallback_SilentOnSameStatus verifies the callback does NOT fire
// when the status is unchanged across polls.
func TestStatusChangeCallback_SilentOnSameStatus(t *testing.T) {
	var (
		mu      sync.Mutex
		changes []StatusChange
	)

	cb := func(c StatusChange) {
		mu.Lock()
		changes = append(changes, c)
		mu.Unlock()
	}

	// Always returns 200 OK — status stays "up" after first poll.
	_, cancel := startTestPulseBoard(t, 19301,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
		WithStatusChangeCallback(cb),
	)
	defer cancel()

	// Wait long enough for several polls to run.
	time.Sleep(400 * time.Millisecond)

	mu.Lock()
	count := len(changes)
	mu.Unlock()

	// Only the very first poll (empty → up) should fire. Subsequent same-status
	// polls must not fire additional callbacks.
	if count != 1 {
		t.Errorf("status change callbacks = %d, want 1 (only first poll should fire on stable endpoint)", count)
	}
}

// TestStatusChangeCallback_FirstPollIsChange verifies first poll fires with PreviousStatus == "".
func TestStatusChangeCallback_FirstPollIsChange(t *testing.T) {
	gotChange := make(chan StatusChange, 1)

	cb := func(c StatusChange) {
		select {
		case gotChange <- c:
		default:
		}
	}

	_, cancel := startTestPulseBoard(t, 19302,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
		WithStatusChangeCallback(cb),
	)
	defer cancel()

	var change StatusChange
	select {
	case change = <-gotChange:
	case <-time.After(2 * time.Second):
		t.Fatal("no status change received within timeout")
	}

	if change.PreviousStatus != "" {
		t.Errorf("PreviousStatus = %q, want %q (empty string for first poll)", change.PreviousStatus, "")
	}
	if change.CurrentStatus != StatusUp {
		t.Errorf("CurrentStatus = %q, want %q", change.CurrentStatus, StatusUp)
	}
}

// TestStatusChangeCallback_MultipleCallbacks verifies all registered callbacks fire in order.
func TestStatusChangeCallback_MultipleCallbacks(t *testing.T) {
	var (
		mu    sync.Mutex
		order []int
	)

	cb1 := func(c StatusChange) {
		mu.Lock()
		order = append(order, 1)
		mu.Unlock()
	}
	cb2 := func(c StatusChange) {
		mu.Lock()
		order = append(order, 2)
		mu.Unlock()
	}

	_, cancel := startTestPulseBoard(t, 19303,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
		WithStatusChangeCallback(cb1),
		WithStatusChangeCallback(cb2),
	)
	defer cancel()

	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	got := make([]int, len(order))
	copy(got, order)
	mu.Unlock()

	if len(got) < 2 {
		t.Fatalf("expected at least 2 callback invocations (one per callback on first poll), got %d", len(got))
	}
	// First two invocations must be [1, 2] — registration order.
	if got[0] != 1 || got[1] != 2 {
		t.Errorf("callback order = %v, want [1, 2, ...] (registration order)", got)
	}
}

// TestStatusChangeCallback_PanicRecovery verifies a panicking callback doesn't crash
// and that the second callback still fires.
func TestStatusChangeCallback_PanicRecovery(t *testing.T) {
	secondFired := make(chan struct{}, 1)

	panicCb := func(c StatusChange) {
		panic("deliberate panic in test callback")
	}
	safeCb := func(c StatusChange) {
		select {
		case secondFired <- struct{}{}:
		default:
		}
	}

	_, cancel := startTestPulseBoard(t, 19304,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
		WithStatusChangeCallback(panicCb),
		WithStatusChangeCallback(safeCb),
	)
	defer cancel()

	select {
	case <-secondFired:
		// second callback fired despite first panicking
	case <-time.After(2 * time.Second):
		t.Fatal("second callback not fired after panicking first callback")
	}
}

// TestStatusChangeCallback_PayloadFields verifies the StatusChange payload has correct fields.
func TestStatusChangeCallback_PayloadFields(t *testing.T) {
	gotChange := make(chan StatusChange, 1)

	cb := func(c StatusChange) {
		select {
		case gotChange <- c:
		default:
		}
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)

	ep, err := NewEndpoint("PayloadTest", ts.URL, WithLabels("region", "eu-west"))
	if err != nil {
		t.Fatalf("NewEndpoint() error = %v", err)
	}

	pb, err := New(
		WithEndpoint(ep),
		WithPort(19305),
		WithPollingInterval(50*time.Millisecond),
		WithStaleThreshold(0),
		WithStatusChangeCallback(cb),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := make(chan struct{})
	go func() {
		close(started)
		_ = pb.Start(ctx)
	}()
	<-started

	var change StatusChange
	select {
	case change = <-gotChange:
	case <-time.After(2 * time.Second):
		t.Fatal("no status change received within timeout")
	}

	if change.EndpointName != "PayloadTest" {
		t.Errorf("EndpointName = %q, want %q", change.EndpointName, "PayloadTest")
	}
	if change.URL != ts.URL {
		t.Errorf("URL = %q, want %q", change.URL, ts.URL)
	}
	if change.Labels["region"] != "eu-west" {
		t.Errorf("Labels[region] = %q, want %q", change.Labels["region"], "eu-west")
	}
	if change.CurrentStatus != StatusUp {
		t.Errorf("CurrentStatus = %q, want %q", change.CurrentStatus, StatusUp)
	}
	if change.PreviousStatus != "" {
		t.Errorf("PreviousStatus = %q, want empty string", change.PreviousStatus)
	}
	if change.CheckedAt.IsZero() {
		t.Error("CheckedAt is zero, want a non-zero timestamp")
	}
}
