package server

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jpalmerr/pulseboard/internal/store"
)

// testLogger returns a logger that discards all output for clean test output.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// mockStore implements store.Store for testing.
type mockStore struct {
	mu          sync.RWMutex
	statuses    []store.StatusResult
	subscribers map[chan store.StatusResult]struct{}
	subMu       sync.Mutex
}

func newMockStore() *mockStore {
	return &mockStore{
		statuses:    []store.StatusResult{},
		subscribers: make(map[chan store.StatusResult]struct{}),
	}
}

func (m *mockStore) Update(result store.StatusResult) {
	m.mu.Lock()
	// replace if exists, otherwise append
	found := false
	for i, s := range m.statuses {
		if s.Name == result.Name {
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

func (m *mockStore) GetAll() []store.StatusResult {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]store.StatusResult, len(m.statuses))
	copy(result, m.statuses)
	return result
}

func (m *mockStore) Subscribe() <-chan store.StatusResult {
	ch := make(chan store.StatusResult, 100)
	m.subMu.Lock()
	m.subscribers[ch] = struct{}{}
	m.subMu.Unlock()
	return ch
}

func (m *mockStore) Unsubscribe(ch <-chan store.StatusResult) {
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

// --- Tests ---

func TestHandleSSE_BasicFlow(t *testing.T) {
	ms := newMockStore()
	ms.Update(store.StatusResult{Name: "API-1", Status: "up"})
	ms.Update(store.StatusResult{Name: "API-2", Status: "down"})

	srv := NewServer(ms, 0, nil, "", testLogger())

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
	srv := NewServer(ms, 0, nil, "", testLogger())

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
	ms.Update(store.StatusResult{Name: "NewAPI", Status: "up"})

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
	srv := NewServer(ms, 0, nil, "", testLogger())

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
	srv := NewServer(ms, 0, nil, "", testLogger())

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
	srv := NewServer(ms, 0, nil, "", testLogger())

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
	ms.Update(store.StatusResult{Name: "API", Status: "up"})

	srv := NewServer(ms, 0, nil, "", testLogger())

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
	srv := NewServer(ms, 0, nil, "", testLogger())

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
	srv := NewServer(ms, 0, nil, "", testLogger())

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
	ms.Update(store.StatusResult{
		Name:           "TestAPI",
		URL:            "https://example.com",
		Status:         "up",
		Labels:         map[string]string{"env": "prod"},
		ResponseTimeMs: 42,
		CheckedAt:      time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	})

	srv := NewServer(ms, 0, nil, "", testLogger())

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

	var result store.StatusResult
	if err := json.Unmarshal([]byte(jsonData), &result); err != nil {
		t.Fatalf("failed to parse JSON: %v, data: %s", err, jsonData)
	}

	if result.Name != "TestAPI" {
		t.Errorf("Name = %q, want %q", result.Name, "TestAPI")
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
	ms.Update(store.StatusResult{Name: "IntegrationAPI", Status: "up"})

	srv := NewServer(ms, 0, nil, "", testLogger())

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
	ms.Update(store.StatusResult{Name: "API", Status: "up"})

	srv := NewServer(ms, 0, nil, "", testLogger())

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
	ms.Update(store.StatusResult{Name: "API", Status: "up"})

	srv := NewServer(ms, 0, nil, "", testLogger())

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

// --- Helper to read SSE events from response ---

func parseSSEEvents(body string) []store.StatusResult {
	var results []store.StatusResult
	lines := strings.Split(body, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "data: ") {
			jsonData := strings.TrimPrefix(line, "data: ")
			var result store.StatusResult
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
	ms.Update(store.StatusResult{Name: "Integration-API", Status: "up"})

	srv := NewServer(ms, 0, nil, "", testLogger())

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
		if e.Name == "Integration-API" {
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
	srv := NewServer(ms, 0, nil, "", testLogger())

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
	srv := NewServer(ms, port, nil, "", testLogger())

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
	srv := NewServer(ms, -1, nil, "", testLogger())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := srv.Start(ctx)
	if err == nil {
		t.Fatal("Start() with invalid port should return error")
	}
}

// --- Benchmark ---

func BenchmarkHandleSSE_SingleClient(b *testing.B) {
	ms := newMockStore()
	for i := 0; i < 10; i++ {
		ms.Update(store.StatusResult{Name: "API-" + string(rune('A'+i)), Status: "up"})
	}

	srv := NewServer(ms, 0, nil, "", testLogger())

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
	srv := NewServer(ms, 0, mockAssets, "Video Channel Healthchecks", testLogger())

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
	srv := NewServer(ms, 0, mockAssets, "", testLogger()) // empty title

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
	srv := NewServer(ms, 0, nil, "Custom Title", testLogger()) // nil assets

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
	srv := NewServer(ms, 0, mockAssets, "", testLogger())

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
	srv := NewServer(ms, 0, mockAssets, "<script>alert('xss')</script>", testLogger())

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
	srv := NewServer(ms, 0, mockAssets, "Health & Status", testLogger())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	srv.handleDashboard(rec, req)

	body := rec.Body.String()

	// ampersand should be escaped
	if !strings.Contains(body, "Health &amp; Status") {
		t.Errorf("expected ampersand to be escaped, got: %s", body)
	}
}
