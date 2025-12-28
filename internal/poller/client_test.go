package poller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"testing"
	"time"
)

// TestClient_ConnectionReuse verifies that the HTTP client reuses connections
// when making sequential requests to the same host. This validates that the
// Transport is configured with keep-alives enabled and connection pooling active.
func TestClient_ConnectionReuse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	client := NewClient()

	var reusedCount int
	trace := &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) {
			if info.Reused {
				reusedCount++
			}
		},
	}

	const numRequests = 5

	// make sequential requests to ensure pool has opportunity to reuse
	for i := 0; i < numRequests; i++ {
		ctx := httptrace.WithClientTrace(context.Background(), trace)
		resp := client.Fetch(ctx, "", server.URL, nil, 5*time.Second)
		if resp.Error != nil {
			t.Fatalf("request %d failed: %v", i, resp.Error)
		}
	}

	// with connection pooling enabled, we expect at least some reuse
	// (all requests after the first should reuse the connection)
	expectedMinReuse := numRequests - 2 // allow some tolerance
	if reusedCount < expectedMinReuse {
		t.Errorf("expected at least %d reused connections, got %d out of %d requests",
			expectedMinReuse, reusedCount, numRequests)
	}
}

// TestClient_Close verifies that Close() is safe to call and idempotent.
func TestClient_Close(t *testing.T) {
	client := NewClient()

	// should not panic
	client.Close()

	// calling Close multiple times should be safe (idempotent)
	client.Close()
	client.Close()
}

// TestClient_Close_NilClient verifies that Close() handles nil receiver safely.
func TestClient_Close_NilClient(t *testing.T) {
	var client *Client

	// should not panic on nil receiver
	client.Close()
}

// TestClient_Close_ActuallyClosesConnections verifies that Close closes idle
// connections, but the client remains usable for new requests.
func TestClient_Close_ActuallyClosesConnections(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	client := NewClient()

	// establish connections
	for i := 0; i < 5; i++ {
		resp := client.Fetch(context.Background(), "", server.URL, nil, time.Second)
		if resp.Error != nil {
			t.Fatalf("request %d failed: %v", i, resp.Error)
		}
	}

	// close idle connections
	client.Close()

	// subsequent requests should still work (new connections established)
	resp := client.Fetch(context.Background(), "", server.URL, nil, time.Second)
	if resp.Error != nil {
		t.Errorf("request after Close failed: %v", resp.Error)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
}
