package server

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jpalmerr/pulseboard/internal/metrics"
	"github.com/jpalmerr/pulseboard/internal/store"
	"github.com/jpalmerr/pulseboard/internal/types"
)

// testLogger returns a logger that discards all output for clean test output.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// mockStore implements store.Store for testing.
type mockStore struct {
	mu          sync.RWMutex
	statuses    []types.StatusResult
	subscribers map[chan types.StatusResult]struct{}
	subMu       sync.Mutex
}

func newMockStore() *mockStore {
	return &mockStore{
		statuses:    []types.StatusResult{},
		subscribers: make(map[chan types.StatusResult]struct{}),
	}
}

func (m *mockStore) Update(result types.StatusResult) {
	m.mu.Lock()
	// replace if exists, otherwise append
	found := false
	for i, s := range m.statuses {
		if s.EndpointName == result.EndpointName {
			m.statuses[i] = result
			found = true
			break
		}
	}
	if !found {
		m.statuses = append(m.statuses, result)
	}
	m.mu.Unlock()

	m.subMu.Lock()
	for ch := range m.subscribers {
		select {
		case ch <- result:
		default:
		}
	}
	m.subMu.Unlock()
}

func (m *mockStore) GetAll() []types.StatusResult {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]types.StatusResult, len(m.statuses))
	copy(result, m.statuses)
	return result
}

func (m *mockStore) Subscribe() <-chan types.StatusResult {
	ch := make(chan types.StatusResult, 100)
	m.subMu.Lock()
	m.subscribers[ch] = struct{}{}
	m.subMu.Unlock()
	return ch
}

func (m *mockStore) Unsubscribe(ch <-chan types.StatusResult) {
	m.subMu.Lock()
	defer m.subMu.Unlock()
	for subCh := range m.subscribers {
		if subCh == ch {
			delete(m.subscribers, subCh)
			close(subCh)
			break
		}
	}
}

func (m *mockStore) MarkStale(_ time.Duration) int {
	// no-op in test mock — staleness checking is tested at the store level
	return 0
}

// --- Tests ---

func TestHandleSSE_BasicFlow(t *testing.T) {
	ms := newMockStore()
	ms.Update(types.StatusResult{EndpointName: "API-1", Status: "up"})
	ms.Update(types.StatusResult{EndpointName: "API-2", Status: "down"})

	srv := NewServer(ms, 0, nil, "", testLogger(), nil, "", "", nil)

	req := httptest.NewRequest(http.MethodGet, "/api/sse", nil)
	rec := httptest.NewRecorder()

	// run handler in goroutine since it blocks
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	req = req.WithContext(ctx)

	srv.handleSSE(rec, req)

	body := rec.Body.String()

	// should contain initial statuses
	if !strings.Contains(body, "API-1") {
		t.Errorf("response should contain API-1, got: %s", body)
	}
	if !strings.Contains(body, "API-2") {
		t.Errorf("response should contain API-2, got: %s", body)
	}
}

func TestHandleSSE_StreamsUpdates(t *testing.T) {
	ms := newMockStore()
	srv := NewServer(ms, 0, nil, "", testLogger(), nil, "", "", nil)

	req := httptest.NewRequest(http.MethodGet, "/api/sse", nil)
	rec := httptest.NewRecorder()

	ctx, cancel := context.WithCancel(context.Background())
	req = req.WithContext(ctx)

	done := make(chan struct{})
	go func() {
		srv.handleSSE(rec, req)
		close(done)
	}()

	// give handler time to subscribe
	time.Sleep(50 * time.Millisecond)

	// send an update
	ms.Update(types.StatusResult{EndpointName: "NewAPI", Status: "up"})

	// give time for update to be written
	time.Sleep(50 * time.Millisecond)

	// cancel to stop handler
	cancel()

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("handler did not exit after context cancellation")
	}

	body := rec.Body.String()
	if !strings.Contains(body, "NewAPI") {
		t.Errorf("response should contain streamed update NewAPI, got: %s", body)
	}
}

func TestHandleSSE_ClientDisconnect(t *testing.T) {
	ms := newMockStore()
	srv := NewServer(ms, 0, nil, "", testLogger(), nil, "", "", nil)

	req := httptest.NewRequest(http.MethodGet, "/api/sse", nil)
	rec := httptest.NewRecorder()

	ctx, cancel := context.WithCancel(context.Background())
	req = req.WithContext(ctx)

	done := make(chan struct{})
	go func() {
		srv.handleSSE(rec, req)
		close(done)
	}()

	// simulate client disconnect
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// handler exited as expected
	case <-time.After(1 * time.Second):
		t.Fatal("handler did not exit after client disconnect")
	}
}

func TestHandleSSE_ServerShutdown(t *testing.T) {
	ms := newMockStore()
	srv := NewServer(ms, 0, nil, "", testLogger(), nil, "", "", nil)

	// create a server context that we'll cancel to simulate shutdown
	serverCtx, serverCancel := context.WithCancel(context.Background())

	// when calling handleSSE directly (not through http.Server), we must
	// manually derive the request context from the server context to simulate
	// BaseContext behavior. In production, BaseContext does this automatically.
	req := httptest.NewRequest(http.MethodGet, "/api/sse", nil)
	req = req.WithContext(serverCtx) // key: request context derived from server context
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		srv.handleSSE(rec, req)
		close(done)
	}()

	// give handler time to subscribe and start waiting
	time.Sleep(50 * time.Millisecond)

	// trigger server shutdown by cancelling context
	serverCancel()

	select {
	case <-done:
		// handler exited as expected
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not exit after server shutdown")
	}
}

func TestHandleSSE_NoGoroutineLeaks(t *testing.T) {
	// allow existing goroutines to settle
	runtime.GC()
	time.Sleep(100 * time.Millisecond)
	before := runtime.NumGoroutine()

	ms := newMockStore()
	srv := NewServer(ms, 0, nil, "", testLogger(), nil, "", "", nil)

	// run multiple SSE connections
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

			req := httptest.NewRequest(http.MethodGet, "/api/sse", nil)
			req = req.WithContext(ctx)
			rec := httptest.NewRecorder()

			srv.handleSSE(rec, req)
		}()
	}

	wg.Wait()

	// allow cleanup
	runtime.GC()
	time.Sleep(200 * time.Millisecond)

	after := runtime.NumGoroutine()
	if after > before+2 { // small tolerance for runtime variance
		t.Errorf("potential goroutine leak: before=%d, after=%d", before, after)
	}
}

func TestHandleSSE_ConcurrentClientsShutdown(t *testing.T) {
	ms := newMockStore()
	ms.Update(types.StatusResult{EndpointName: "API", Status: "up"})

	srv := NewServer(ms, 0, nil, "", testLogger(), nil, "", "", nil)

	serverCtx, serverCancel := context.WithCancel(context.Background())

	numClients := 10
	var wg sync.WaitGroup
	started := make(chan struct{})
	var startedCount atomic.Int32

	// start multiple SSE clients
	for i := 0; i < numClients; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			req := httptest.NewRequest(http.MethodGet, "/api/sse", nil)
			req = req.WithContext(serverCtx)
			rec := httptest.NewRecorder()

			// use Add's return value to ensure only one goroutine closes the channel
			if startedCount.Add(1) == int32(numClients) {
				close(started)
			}

			srv.handleSSE(rec, req)
		}()
	}

	// wait for all clients to start
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("clients did not start in time")
	}

	// give handlers time to subscribe
	time.Sleep(100 * time.Millisecond)

	// trigger shutdown
	serverCancel()

	// all should exit promptly
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// all handlers exited
	case <-time.After(3 * time.Second):
		t.Fatal("not all handlers exited after shutdown")
	}
}

func TestHandleSSE_SSENotSupported(t *testing.T) {
	ms := newMockStore()
	srv := NewServer(ms, 0, nil, "", testLogger(), nil, "", "", nil)

	req := httptest.NewRequest(http.MethodGet, "/api/sse", nil)

	// use a writer that doesn't support flushing
	w := &nonFlushWriter{header: make(http.Header)}

	srv.handleSSE(w, req)

	if w.statusCode != http.StatusInternalServerError {
		t.Errorf("expected status %d, got %d", http.StatusInternalServerError, w.statusCode)
	}
}

type nonFlushWriter struct {
	header     http.Header
	statusCode int
	body       []byte
}

func (n *nonFlushWriter) Header() http.Header {
	return n.header
}

func (n *nonFlushWriter) Write(b []byte) (int, error) {
	n.body = append(n.body, b...)
	return len(b), nil
}

func (n *nonFlushWriter) WriteHeader(statusCode int) {
	n.statusCode = statusCode
}

func TestHandleSSE_Headers(t *testing.T) {
	ms := newMockStore()
	srv := NewServer(ms, 0, nil, "", testLogger(), nil, "", "", nil)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/sse", nil)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	srv.handleSSE(rec, req)

	expectedHeaders := map[string]string{
		"Content-Type":                "text/event-stream",
		"Cache-Control":               "no-cache",
		"Connection":                  "keep-alive",
		"Access-Control-Allow-Origin": "*",
	}

	for key, expected := range expectedHeaders {
		if got := rec.Header().Get(key); got != expected {
			t.Errorf("header %s = %q, want %q", key, got, expected)
		}
	}
}

func TestHandleSSE_JSONFormat(t *testing.T) {
	ms := newMockStore()
	ms.Update(types.StatusResult{
		EndpointName:   "TestAPI",
		URL:            "https://example.com",
		Status:         "up",
		Labels:         map[string]string{"env": "prod"},
		ResponseTimeMs: 42,
		CheckedAt:      time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	})

	srv := NewServer(ms, 0, nil, "", testLogger(), nil, "", "", nil)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/sse", nil)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	srv.handleSSE(rec, req)

	body := rec.Body.String()

	// extract JSON from "data: {...}\n\n" format
	lines := strings.Split(body, "\n")
	var jsonData string
	for _, line := range lines {
		if strings.HasPrefix(line, "data: ") {
			jsonData = strings.TrimPrefix(line, "data: ")
			break
		}
	}

	if jsonData == "" {
		t.Fatalf("no SSE data found in response: %s", body)
	}

	var result types.StatusResult
	if err := json.Unmarshal([]byte(jsonData), &result); err != nil {
		t.Fatalf("failed to parse JSON: %v, data: %s", err, jsonData)
	}

	if result.EndpointName != "TestAPI" {
		t.Errorf("EndpointName = %q, want %q", result.EndpointName, "TestAPI")
	}
	if result.Status != "up" {
		t.Errorf("Status = %q, want %q", result.Status, "up")
	}
}

// --- Integration tests for slow client / shutdown behavior ---
//
// These tests use httptest.Server to create real HTTP connections that support
// write deadlines. Mock ResponseWriters don't support SetWriteDeadline, so we
// can't unit test deadline behavior with mocks.

// TestHandleSSE_ServerShutdownIntegration tests that SSE handlers exit cleanly
// when the server is shut down, using a real HTTP connection.
func TestHandleSSE_ServerShutdownIntegration(t *testing.T) {
	ms := newMockStore()
	ms.Update(types.StatusResult{EndpointName: "IntegrationAPI", Status: "up"})

	srv := NewServer(ms, 0, nil, "", testLogger(), nil, "", "", nil)

	serverCtx, serverCancel := context.WithCancel(context.Background())

	// create HTTP handler that respects server context
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// derive request context from server context (simulates BaseContext)
		r = r.WithContext(serverCtx)
		srv.handleSSE(w, r)
	})

	ts := httptest.NewServer(handler)
	defer ts.Close()

	// start SSE connection
	client := ts.Client()
	req, err := http.NewRequest(http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	connDone := make(chan error, 1)
	go func() {
		resp, err := client.Do(req)
		if err != nil {
			connDone <- err
			return
		}
		defer func() { _ = resp.Body.Close() }()

		// read until connection closes
		buf := make([]byte, 1024)
		for {
			_, err := resp.Body.Read(buf)
			if err != nil {
				connDone <- nil // expected - connection closed
				return
			}
		}
	}()

	// give connection time to establish
	time.Sleep(100 * time.Millisecond)

	// trigger server shutdown
	serverCancel()

	// connection should close promptly
	select {
	case <-connDone:
		// success
	case <-time.After(3 * time.Second):
		t.Fatal("SSE connection did not close after server shutdown")
	}
}

// TestHandleSSE_MultipleClientsShutdownIntegration tests shutdown with multiple
// concurrent SSE clients.
func TestHandleSSE_MultipleClientsShutdownIntegration(t *testing.T) {
	ms := newMockStore()
	ms.Update(types.StatusResult{EndpointName: "API", Status: "up"})

	srv := NewServer(ms, 0, nil, "", testLogger(), nil, "", "", nil)

	serverCtx, serverCancel := context.WithCancel(context.Background())

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(serverCtx)
		srv.handleSSE(w, r)
	})

	ts := httptest.NewServer(handler)
	defer ts.Close()

	numClients := 5
	var wg sync.WaitGroup
	started := make(chan struct{})
	var startedCount atomic.Int32

	// start multiple SSE clients
	for i := 0; i < numClients; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			client := ts.Client()
			resp, err := client.Get(ts.URL)
			if err != nil {
				return // server might have shut down
			}
			defer func() { _ = resp.Body.Close() }()

			if startedCount.Add(1) == int32(numClients) {
				close(started)
			}

			// read until closed
			buf := make([]byte, 1024)
			for {
				_, err := resp.Body.Read(buf)
				if err != nil {
					return
				}
			}
		}()
	}

	// wait for clients to start
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Log("not all clients started, continuing anyway")
	}

	// give handlers time to subscribe
	time.Sleep(100 * time.Millisecond)

	// shutdown
	serverCancel()

	// all should exit
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("not all SSE clients disconnected after shutdown")
	}
}

// TestHandleSSE_WriteDeadlineProtection documents that write deadlines protect
// against slow clients. This test verifies the code path exists but can't fully
// test deadline behavior without a slow network simulation.
//
// The key behavior being tested:
// 1. SetWriteDeadline is called before each write
// 2. If deadline is not supported, handler logs once and continues
// 3. Handler still exits on context cancellation
func TestHandleSSE_WriteDeadlineProtection(t *testing.T) {
	ms := newMockStore()
	ms.Update(types.StatusResult{EndpointName: "API", Status: "up"})

	srv := NewServer(ms, 0, nil, "", testLogger(), nil, "", "", nil)

	serverCtx, serverCancel := context.WithCancel(context.Background())
	defer serverCancel()

	// Use httptest.ResponseRecorder which doesn't support deadlines.
	// This tests the fallback path where deadlines are not supported.
	req := httptest.NewRequest(http.MethodGet, "/api/sse", nil)
	req = req.WithContext(serverCtx)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		srv.handleSSE(rec, req)
		close(done)
	}()

	// give handler time to write initial data
	time.Sleep(100 * time.Millisecond)

	// cancel context
	serverCancel()

	// handler should exit (even without deadline support, context cancellation works)
	select {
	case <-done:
		// verify data was written
		body := rec.Body.String()
		if !strings.Contains(body, "API") {
			t.Errorf("expected API in response, got: %s", body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not exit after context cancellation")
	}
}

// TestHandleSSE_StaggeredDisconnect verifies that when one client disconnects,
// remaining subscribers continue to receive updates without deadlocks or panics.
// This tests the invariant that Unsubscribe does not corrupt the subscriber set.
//
// Note: the handler must NOT override r.Context() with a shared server context —
// each connection's per-request context must be used so that individual client
// disconnects (context cancellation) cause only that handler to exit.
func TestHandleSSE_StaggeredDisconnect(t *testing.T) {
	ms := newMockStore()
	// Pre-populate so handleSSE flushes response headers on connection;
	// without an initial write the response headers stay buffered and Do() blocks.
	ms.Update(types.StatusResult{EndpointName: "Initial", Status: "up"})
	srv := NewServer(ms, 0, nil, "", testLogger(), nil, "", "", nil)

	// Do NOT override request context: rely on the per-connection context that
	// Go's http.Server cancels when the client disconnects.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		srv.handleSSE(w, r)
	}))
	defer ts.Close()

	ctxA, cancelA := context.WithCancel(context.Background())
	defer cancelA()

	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelB()

	// secondReceived is closed when client B reads the "Second" update.
	secondReceived := make(chan struct{})
	var secondOnce sync.Once

	// Connect client A.
	reqA, err := http.NewRequestWithContext(ctxA, http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("failed to create request A: %v", err)
	}
	respA, err := ts.Client().Do(reqA)
	if err != nil {
		t.Fatalf("client A Do(): %v", err)
	}
	defer func() { _ = respA.Body.Close() }()

	// Connect client B.
	reqB, err := http.NewRequestWithContext(ctxB, http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("failed to create request B: %v", err)
	}
	respB, err := ts.Client().Do(reqB)
	if err != nil {
		t.Fatalf("client B Do(): %v", err)
	}
	defer func() { _ = respB.Body.Close() }()

	// Read B's stream in a goroutine; signal when "Second" arrives.
	bDone := make(chan struct{})
	go func() {
		defer close(bDone)
		scanner := bufio.NewScanner(respB.Body)
		for scanner.Scan() {
			if strings.Contains(scanner.Text(), `"Second"`) {
				secondOnce.Do(func() { close(secondReceived) })
			}
		}
	}()

	// Allow both handlers to subscribe and flush initial data.
	time.Sleep(100 * time.Millisecond)

	// Disconnect A — handler A exits via r.Context().Done().
	cancelA()
	time.Sleep(100 * time.Millisecond)

	// Send second update — B must receive it without being blocked by A's exit.
	ms.Update(types.StatusResult{EndpointName: "Second", Status: "up"})

	select {
	case <-secondReceived:
		// B received the update after A disconnected.
	case <-time.After(2 * time.Second):
		t.Fatal("remaining client did not receive update after peer disconnect")
	}

	// Disconnect B and wait for its reader goroutine to exit.
	cancelB()
	select {
	case <-bDone:
	case <-time.After(2 * time.Second):
		t.Fatal("client B reader did not exit after cancellation")
	}
}

// --- Helper to read SSE events from response ---

func parseSSEEvents(body string) []types.StatusResult {
	var results []types.StatusResult
	lines := strings.Split(body, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "data: ") {
			jsonData := strings.TrimPrefix(line, "data: ")
			var result types.StatusResult
			if err := json.Unmarshal([]byte(jsonData), &result); err == nil {
				results = append(results, result)
			}
		}
	}
	return results
}

// --- Integration test with real HTTP server ---

func TestServer_SSEIntegration(t *testing.T) {
	ms := newMockStore()
	ms.Update(types.StatusResult{EndpointName: "Integration-API", Status: "up"})

	srv := NewServer(ms, 0, nil, "", testLogger(), nil, "", "", nil)

	serverCtx, serverCancel := context.WithCancel(context.Background())
	defer serverCancel()

	if err := srv.Start(serverCtx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}

	// give server time to start
	time.Sleep(50 * time.Millisecond)

	// make real HTTP request to the server
	// Note: we need to use the actual server address
	// For now, we test the handler directly

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/sse", nil)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	srv.handleSSE(rec, req)

	events := parseSSEEvents(rec.Body.String())
	if len(events) == 0 {
		t.Fatal("expected at least one SSE event")
	}

	found := false
	for _, e := range events {
		if e.EndpointName == "Integration-API" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Integration-API not found in SSE events")
	}
}

// --- Server Start Tests (TICK-011) ---

func TestStart_AvailablePort_ReturnsNil(t *testing.T) {
	ms := newMockStore()
	// port 0 = OS assigns available port. Valid for internal Server package,
	// though public PulseBoard API validates port > 0.
	srv := NewServer(ms, 0, nil, "", testLogger(), nil, "", "", nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := srv.Start(ctx)
	if err != nil {
		t.Errorf("Start() on available port returned error: %v", err)
	}
	// cleanup verified by context cancellation via defer; shutdown behaviour
	// is covered by existing TestHandleSSE_ServerShutdownIntegration
}

func TestStart_PortInUse_ReturnsError(t *testing.T) {
	// occupy a port
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("failed to create listener: %v", err)
	}
	defer func() { _ = ln.Close() }()

	port := ln.Addr().(*net.TCPAddr).Port

	// try to start server on same port
	ms := newMockStore()
	srv := NewServer(ms, port, nil, "", testLogger(), nil, "", "", nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err = srv.Start(ctx)
	if err == nil {
		t.Fatal("Start() on occupied port should return error")
	}
	// verify error is from our code path, not some other failure
	if !strings.Contains(err.Error(), "failed to bind") {
		t.Errorf("expected bind error, got: %v", err)
	}
}

func TestStart_InvalidPort_ReturnsError(t *testing.T) {
	ms := newMockStore()
	srv := NewServer(ms, -1, nil, "", testLogger(), nil, "", "", nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := srv.Start(ctx)
	if err == nil {
		t.Fatal("Start() with invalid port should return error")
	}
}

// generateTestCert creates a temporary self-signed certificate and returns file paths.
// The certificate is valid for 127.0.0.1 and localhost.
func generateTestCert(t *testing.T) (certFile, keyFile string) {
	t.Helper()

	dir := t.TempDir()

	// generate ECDSA private key
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generateTestCert: generate key: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"PulseBoard Test"},
		},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:    []string{"localhost"},
		NotBefore:   time.Now().Add(-time.Hour),
		NotAfter:    time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("generateTestCert: create certificate: %v", err)
	}

	certFile = filepath.Join(dir, "cert.pem")
	f, err := os.Create(certFile)
	if err != nil {
		t.Fatalf("generateTestCert: create cert file: %v", err)
	}
	if err := pem.Encode(f, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}); err != nil {
		t.Fatalf("generateTestCert: encode cert: %v", err)
	}
	_ = f.Close()

	keyFile = filepath.Join(dir, "key.pem")
	kf, err := os.Create(keyFile)
	if err != nil {
		t.Fatalf("generateTestCert: create key file: %v", err)
	}
	privDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("generateTestCert: marshal key: %v", err)
	}
	if err := pem.Encode(kf, &pem.Block{Type: "EC PRIVATE KEY", Bytes: privDER}); err != nil {
		t.Fatalf("generateTestCert: encode key: %v", err)
	}
	_ = kf.Close()

	return certFile, keyFile
}

// TestStart_TLS_UsesListenerAddr verifies that Start() serves over TLS when cert
// and key files are provided. Uses port=0 so the OS assigns a free port, and
// reads the actual bound address via srv.Addr() — this eliminates the TOCTOU
// race that occurs when pre-selecting a port via bind-and-release.
func TestStart_TLS_UsesListenerAddr(t *testing.T) {
	certFile, keyFile := generateTestCert(t)

	ms := newMockStore()
	ms.Update(types.StatusResult{EndpointName: "TLS-Endpoint", Status: "up"})

	// port=0 lets the OS assign any available port
	srv := NewServer(ms, 0, nil, "", testLogger(), nil, certFile, keyFile, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := srv.Start(ctx); err != nil {
		t.Fatalf("Start() with TLS error: %v", err)
	}

	// resolve the actual port from the listener address
	_, portStr, err := net.SplitHostPort(srv.Addr())
	if err != nil {
		t.Fatalf("failed to parse server addr %q: %v", srv.Addr(), err)
	}

	// build a client that skips verification (InsecureSkipVerify acceptable in tests only)
	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test only
		},
		Timeout: 3 * time.Second,
	}

	url := fmt.Sprintf("https://127.0.0.1:%s/api/status", portStr)
	resp, err := httpClient.Get(url)
	if err != nil {
		t.Fatalf("GET %s error: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /api/status = %d, want 200", resp.StatusCode)
	}
}

// --- Benchmark ---

func BenchmarkHandleSSE_SingleClient(b *testing.B) {
	ms := newMockStore()
	for i := 0; i < 10; i++ {
		ms.Update(types.StatusResult{EndpointName: "API-" + string(rune('A'+i)), Status: "up"})
	}

	srv := NewServer(ms, 0, nil, "", testLogger(), nil, "", "", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		req := httptest.NewRequest(http.MethodGet, "/api/sse", nil)
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()

		srv.handleSSE(rec, req)
		cancel()
	}
}

// --- Dashboard Title Tests ---

// mockFS implements fs.ReadFileFS for testing dashboard rendering.
type mockFS struct {
	content string
}

func (m *mockFS) Open(name string) (fs.File, error) {
	return nil, fs.ErrNotExist
}

func (m *mockFS) ReadFile(name string) ([]byte, error) {
	if name == "assets/index.html" {
		return []byte(m.content), nil
	}
	return nil, fs.ErrNotExist
}

func TestHandleDashboard_CustomTitle(t *testing.T) {
	ms := newMockStore()
	mockAssets := &mockFS{content: "<title>{{.Title}}</title><h1>{{.Title}}</h1>"}
	srv := NewServer(ms, 0, mockAssets, "Video Channel Healthchecks", testLogger(), nil, "", "", nil)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	srv.handleDashboard(rec, req)

	body := rec.Body.String()

	if !strings.Contains(body, "<title>Video Channel Healthchecks</title>") {
		t.Errorf("expected title tag with custom title, got: %s", body)
	}
	if !strings.Contains(body, "<h1>Video Channel Healthchecks</h1>") {
		t.Errorf("expected h1 with custom title, got: %s", body)
	}
}

func TestHandleDashboard_DefaultTitle(t *testing.T) {
	ms := newMockStore()
	mockAssets := &mockFS{content: "<title>{{.Title}}</title><h1>{{.Title}}</h1>"}
	srv := NewServer(ms, 0, mockAssets, "", testLogger(), nil, "", "", nil) // empty title

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	srv.handleDashboard(rec, req)

	body := rec.Body.String()

	if !strings.Contains(body, "<title>PulseBoard</title>") {
		t.Errorf("expected default title PulseBoard, got: %s", body)
	}
	if !strings.Contains(body, "<h1>PulseBoard</h1>") {
		t.Errorf("expected default h1 PulseBoard, got: %s", body)
	}
}

func TestHandleDashboard_TitleNotFound(t *testing.T) {
	ms := newMockStore()
	srv := NewServer(ms, 0, nil, "Custom Title", testLogger(), nil, "", "", nil) // nil assets

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	srv.handleDashboard(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected status %d, got %d", http.StatusInternalServerError, rec.Code)
	}
}

func TestHandleDashboard_NonRootPath(t *testing.T) {
	ms := newMockStore()
	mockAssets := &mockFS{content: "<title>{{.Title}}</title>"}
	srv := NewServer(ms, 0, mockAssets, "", testLogger(), nil, "", "", nil)

	req := httptest.NewRequest(http.MethodGet, "/other", nil)
	rec := httptest.NewRecorder()

	srv.handleDashboard(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status %d for non-root path, got %d", http.StatusNotFound, rec.Code)
	}
}

func TestHandleDashboard_TitleWithHTMLChars(t *testing.T) {
	ms := newMockStore()
	mockAssets := &mockFS{content: "<title>{{.Title}}</title>"}
	srv := NewServer(ms, 0, mockAssets, "<script>alert('xss')</script>", testLogger(), nil, "", "", nil)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	srv.handleDashboard(rec, req)

	body := rec.Body.String()

	// should NOT contain unescaped script tag
	if strings.Contains(body, "<script>") {
		t.Error("title should be HTML-escaped to prevent XSS")
	}
	// should contain escaped version
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Errorf("expected escaped HTML, got: %s", body)
	}
}

func TestHandleDashboard_TitleWithAmpersand(t *testing.T) {
	ms := newMockStore()
	mockAssets := &mockFS{content: "<title>{{.Title}}</title>"}
	srv := NewServer(ms, 0, mockAssets, "Health & Status", testLogger(), nil, "", "", nil)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	srv.handleDashboard(rec, req)

	body := rec.Body.String()

	// ampersand should be escaped
	if !strings.Contains(body, "Health &amp; Status") {
		t.Errorf("expected ampersand to be escaped, got: %s", body)
	}
}

// --- Auth middleware integration tests ---

// TestServer_AuthMiddleware_WrapsAllEndpoints verifies that a middleware passed
// to NewServer is applied to both /api/status and /api/sse, returning 401 when
// credentials are absent and 200 when credentials are correct.
func TestServer_AuthMiddleware_WrapsAllEndpoints(t *testing.T) {
	const validToken = "test-bearer-token"

	// simple bearer-token middleware for testing
	authMW := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+validToken {
				w.Header().Set("WWW-Authenticate", `Bearer realm="PulseBoard"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}

	ms := newMockStore()
	ms.Update(types.StatusResult{EndpointName: "AuthTest-API", Status: "up"})

	srv := NewServer(ms, 0, nil, "", testLogger(), []func(http.Handler) http.Handler{authMW}, "", "", nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := srv.Start(ctx); err != nil {
		t.Fatalf("Start() error: %v", err)
	}

	endpoints := []string{"/api/status", "/api/sse"}

	for _, path := range endpoints {
		t.Run("unauthenticated_"+path, func(t *testing.T) {
			reqCtx, reqCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer reqCancel()

			req := httptest.NewRequest(http.MethodGet, path, nil)
			req = req.WithContext(reqCtx)
			rec := httptest.NewRecorder()
			srv.httpServer.Handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("%s without credentials: status = %d, want 401", path, rec.Code)
			}
		})

		t.Run("authenticated_"+path, func(t *testing.T) {
			reqCtx, reqCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer reqCancel()

			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Authorization", "Bearer "+validToken)
			req = req.WithContext(reqCtx)
			rec := httptest.NewRecorder()
			srv.httpServer.Handler.ServeHTTP(rec, req)

			// SSE and status both return 200 when auth passes
			if rec.Code != http.StatusOK {
				t.Errorf("%s with credentials: status = %d, want 200", path, rec.Code)
			}
		})
	}
}

// TestHandleStatus_ErrorStatusSerialisation verifies that a result stored with
// Status "error" is returned verbatim as "status": "error" in the JSON response
// from /api/status. This ensures the new StatusError value round-trips correctly
// through the API layer.
func TestHandleStatus_ErrorStatusSerialisation(t *testing.T) {
	ms := newMockStore()
	ms.Update(types.StatusResult{
		EndpointName: "BrokenExtractor",
		URL:          "https://example.com/health",
		Status:       "error",
	})

	srv := NewServer(ms, 0, nil, "", testLogger(), nil, "", "", nil)

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	rec := httptest.NewRecorder()

	srv.handleStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleStatus() status = %d, want %d", rec.Code, http.StatusOK)
	}

	var results []types.StatusResult
	if err := json.NewDecoder(rec.Body).Decode(&results); err != nil {
		t.Fatalf("failed to decode JSON response: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	if results[0].Status != "error" {
		t.Errorf("Status = %q, want %q", results[0].Status, "error")
	}
}

// Ensure mockStore satisfies store.Store interface at compile time.
var _ store.Store = (*mockStore)(nil)

// --- /metrics endpoint tests ---

func TestHandleMetrics_ReturnsOKWithContentType(t *testing.T) {
	ms := newMockStore()
	c := metrics.NewCollector([]string{"API"}, nil)
	srv := NewServer(ms, 0, nil, "", testLogger(), nil, "", "", c)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()

	srv.handleMetrics(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("handleMetrics() status = %d, want %d", rec.Code, http.StatusOK)
	}
	want := "text/plain; version=0.0.4; charset=utf-8"
	if got := rec.Header().Get("Content-Type"); got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}
	if !strings.Contains(rec.Body.String(), "pulseboard_info") {
		t.Error("expected metrics body to contain pulseboard_info")
	}
}

func TestHandleMetrics_NotRegisteredWhenNil(t *testing.T) {
	ms := newMockStore()
	// nil collector — /metrics route must not be registered
	srv := NewServer(ms, 0, nil, "", testLogger(), nil, "", "", nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := srv.Start(ctx); err != nil {
		t.Fatalf("Start() error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("/metrics with nil collector: status = %d, want 404", rec.Code)
	}
}

func TestHandleMetrics_MethodNotAllowed(t *testing.T) {
	ms := newMockStore()
	c := metrics.NewCollector([]string{"API"}, nil)
	srv := NewServer(ms, 0, nil, "", testLogger(), nil, "", "", c)

	req := httptest.NewRequest(http.MethodPost, "/metrics", nil)
	rec := httptest.NewRecorder()

	srv.handleMetrics(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /metrics: status = %d, want 405", rec.Code)
	}
}

// --- Handler error-path tests ---

// failingResponseWriter is an http.ResponseWriter whose Write always returns an error.
// Used to exercise error-handling branches in handlers.
type failingResponseWriter struct {
	header http.Header
	code   int
}

func newFailingResponseWriter() *failingResponseWriter {
	return &failingResponseWriter{header: http.Header{}}
}

func (f *failingResponseWriter) Header() http.Header  { return f.header }
func (f *failingResponseWriter) WriteHeader(code int) { f.code = code }
func (f *failingResponseWriter) Write([]byte) (int, error) {
	return 0, fmt.Errorf("forced write error")
}

// errorReadFileFS implements fs.ReadFileFS but always returns an error from ReadFile.
// Used to exercise the dashboard ReadFile error branch.
type errorReadFileFS struct{}

func (e *errorReadFileFS) Open(string) (fs.File, error)    { return nil, fs.ErrNotExist }
func (e *errorReadFileFS) ReadFile(string) ([]byte, error) { return nil, fmt.Errorf("read error") }

func TestHandleStatus_MethodNotAllowed(t *testing.T) {
	ms := newMockStore()
	srv := NewServer(ms, 0, nil, "", testLogger(), nil, "", "", nil)

	req := httptest.NewRequest(http.MethodPost, "/api/status", nil)
	rec := httptest.NewRecorder()

	srv.handleStatus(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /api/status: status = %d, want 405", rec.Code)
	}
}

func TestHandleStatus_WriteError(t *testing.T) {
	ms := newMockStore()
	ms.Update(types.StatusResult{EndpointName: "API", Status: "up"})
	srv := NewServer(ms, 0, nil, "", testLogger(), nil, "", "", nil)

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := newFailingResponseWriter()

	// Must not panic — the error is logged internally.
	srv.handleStatus(w, req)
}

func TestHandleDashboard_ReadFileError(t *testing.T) {
	ms := newMockStore()
	srv := NewServer(ms, 0, &errorReadFileFS{}, "Title", testLogger(), nil, "", "", nil)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	srv.handleDashboard(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("handleDashboard() with ReadFile error: status = %d, want 500", rec.Code)
	}
}

func TestHandleMetrics_WritePrometheusError(t *testing.T) {
	ms := newMockStore()
	c := metrics.NewCollector([]string{"API"}, nil)
	srv := NewServer(ms, 0, nil, "", testLogger(), nil, "", "", c)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := newFailingResponseWriter()

	// Must not panic — the error is logged internally.
	srv.handleMetrics(w, req)
}
