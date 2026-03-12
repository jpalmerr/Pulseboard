package pulseboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// makeStatusChange returns a minimal StatusChange for use in webhook tests.
func makeStatusChange(name string, current Status) StatusChange {
	return StatusChange{
		EndpointName:   name,
		URL:            "https://example.com",
		PreviousStatus: StatusUp,
		CurrentStatus:  current,
		LatencyMs:      42,
		CheckedAt:      time.Now(),
	}
}

func TestWebhookNotifierSendsJSON(t *testing.T) {
	var received StatusChange
	gotRequest := make(chan struct{}, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Errorf("decode body: %v", err)
		}
		gotRequest <- struct{}{}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	change := StatusChange{
		EndpointName:   "api",
		URL:            "https://api.example.com",
		Labels:         map[string]string{"env": "prod"},
		PreviousStatus: StatusUp,
		CurrentStatus:  StatusDown,
		LatencyMs:      123,
		CheckedAt:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Error:          "connection refused",
	}

	cb := WebhookNotifier(srv.URL)
	cb(change)

	select {
	case <-gotRequest:
	case <-time.After(2 * time.Second):
		t.Fatal("webhook request not received within timeout")
	}

	if received.EndpointName != change.EndpointName {
		t.Errorf("EndpointName = %q, want %q", received.EndpointName, change.EndpointName)
	}
	if received.URL != change.URL {
		t.Errorf("URL = %q, want %q", received.URL, change.URL)
	}
	if received.PreviousStatus != change.PreviousStatus {
		t.Errorf("PreviousStatus = %q, want %q", received.PreviousStatus, change.PreviousStatus)
	}
	if received.CurrentStatus != change.CurrentStatus {
		t.Errorf("CurrentStatus = %q, want %q", received.CurrentStatus, change.CurrentStatus)
	}
	if received.LatencyMs != change.LatencyMs {
		t.Errorf("LatencyMs = %d, want %d", received.LatencyMs, change.LatencyMs)
	}
	if received.Error != change.Error {
		t.Errorf("Error = %q, want %q", received.Error, change.Error)
	}
	if received.Labels["env"] != "prod" {
		t.Errorf("Labels[env] = %q, want %q", received.Labels["env"], "prod")
	}
}

func TestWebhookNotifierEventFilter_Allows(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cb := WebhookNotifier(srv.URL, WithWebhookEventFilter("down"))
	cb(makeStatusChange("api", StatusDown))

	time.Sleep(100 * time.Millisecond)
	if calls.Load() != 1 {
		t.Errorf("webhook calls = %d, want 1 (matching event should fire)", calls.Load())
	}
}

func TestWebhookNotifierEventFilter_Blocks(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cb := WebhookNotifier(srv.URL, WithWebhookEventFilter("down"))
	cb(makeStatusChange("api", StatusUp)) // "up" does not match filter

	time.Sleep(100 * time.Millisecond)
	if calls.Load() != 0 {
		t.Errorf("webhook calls = %d, want 0 (non-matching event should be filtered)", calls.Load())
	}
}

func TestWebhookNotifierNoFilter_AllowsAll(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cb := WebhookNotifier(srv.URL) // no filter
	cb(makeStatusChange("api", StatusUp))
	cb(makeStatusChange("api", StatusDown))
	cb(makeStatusChange("api", StatusDegraded))

	time.Sleep(100 * time.Millisecond)
	if calls.Load() != 3 {
		t.Errorf("webhook calls = %d, want 3 (all transitions should fire without filter)", calls.Load())
	}
}

func TestWebhookNotifierCustomHeaders(t *testing.T) {
	var authHeader string
	gotRequest := make(chan struct{}, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		gotRequest <- struct{}{}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cb := WebhookNotifier(srv.URL, WithWebhookHeaders(map[string]string{
		"Authorization": "Bearer secret-token",
	}))
	cb(makeStatusChange("api", StatusDown))

	select {
	case <-gotRequest:
	case <-time.After(2 * time.Second):
		t.Fatal("webhook request not received within timeout")
	}

	if authHeader != "Bearer secret-token" {
		t.Errorf("Authorization header = %q, want %q", authHeader, "Bearer secret-token")
	}
}

func TestWebhookNotifierTimeout(t *testing.T) {
	// Server that blocks longer than the webhook timeout.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Use a very short timeout to force a context deadline exceeded.
	cb := WebhookNotifier(srv.URL, WithWebhookTimeout(30*time.Millisecond))

	// Must not panic or block.
	done := make(chan struct{})
	go func() {
		defer close(done)
		cb(makeStatusChange("api", StatusDown))
	}()

	select {
	case <-done:
		// success — timed out cleanly
	case <-time.After(2 * time.Second):
		t.Fatal("webhook call did not return after timeout")
	}
}

func TestWebhookNotifierDebounce(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	debounce := 80 * time.Millisecond
	cb := WebhookNotifier(srv.URL, WithWebhookDebounce(debounce))

	// Two rapid calls — only the second (most recent) should fire after debounce elapses.
	cb(makeStatusChange("api", StatusDown))
	cb(makeStatusChange("api", StatusDown))

	// Wait long enough for the debounce timer to fire.
	time.Sleep(200 * time.Millisecond)

	if calls.Load() != 1 {
		t.Errorf("webhook calls = %d, want 1 (debounce should collapse rapid calls)", calls.Load())
	}
}

func TestWebhookNotifierServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	// Must not panic even on non-2xx response.
	cb := WebhookNotifier(srv.URL)
	cb(makeStatusChange("api", StatusDown)) // should not panic
}

func TestWebhookNotifierBadURL(t *testing.T) {
	// Invalid URL scheme — no panic expected.
	cb := WebhookNotifier("not-a-valid-url://???")
	cb(makeStatusChange("api", StatusDown)) // should not panic
}
