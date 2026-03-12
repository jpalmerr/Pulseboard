package server

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/jpalmerr/pulseboard/internal/metrics"
	"github.com/jpalmerr/pulseboard/internal/store"
)

const (
	// sseWriteTimeout is the maximum time allowed for a single SSE write operation.
	// This prevents goroutine leaks when clients are slow or disconnected.
	// Must be <= shutdown timeout to ensure clean shutdown.
	sseWriteTimeout = 5 * time.Second

	// defaultTitle is used when no custom title is configured.
	defaultTitle = "PulseBoard"

	// titlePlaceholder is the marker in HTML that gets replaced with the actual title.
	titlePlaceholder = "{{.Title}}"
)

// Server handles HTTP requests for the PulseBoard dashboard and API.
//
// Server provides three endpoints:
//   - GET /: Serves the embedded dashboard HTML
//   - GET /api/status: Returns all current statuses as JSON
//   - GET /api/sse: Server-Sent Events stream for real-time updates
//
// The server is designed for graceful shutdown via context cancellation.
type Server struct {
	store       store.Store
	port        int
	addr        string // actual bound address, set by Start after net.Listen
	httpServer  *http.Server
	assets      fs.FS
	title       string
	logger      *slog.Logger
	middleware  []func(http.Handler) http.Handler
	tlsCertFile string
	tlsKeyFile  string
	metrics     *metrics.Collector // nil if metrics disabled
}

// NewServer creates a new HTTP [Server].
//
// Parameters:
//   - st: Store implementation for status data
//   - port: TCP port to listen on
//   - assets: Embedded filesystem containing dashboard assets (may be nil)
//   - title: Dashboard title (defaults to "PulseBoard" if empty)
//   - logger: Logger for server events
//   - middleware: Optional middleware chain applied to all handlers
//   - tlsCertFile: Path to TLS certificate PEM file (empty disables TLS)
//   - tlsKeyFile: Path to TLS private key PEM file (empty disables TLS)
//   - metricsCollector: Prometheus metrics collector (nil disables /metrics route)
//
// The server is not started until [Server.Start] is called.
func NewServer(st store.Store, port int, assets fs.FS, title string, logger *slog.Logger, middleware []func(http.Handler) http.Handler, tlsCertFile, tlsKeyFile string, metricsCollector *metrics.Collector) *Server {
	return &Server{
		store:       st,
		port:        port,
		assets:      assets,
		title:       title,
		logger:      logger,
		middleware:  middleware,
		tlsCertFile: tlsCertFile,
		tlsKeyFile:  tlsKeyFile,
		metrics:     metricsCollector,
	}
}

// Start begins serving HTTP requests in a background goroutine.
//
// Start is non-blocking and returns immediately after confirming the server
// is listening. The server will continue running until the context is
// cancelled, at which point it initiates a graceful shutdown with a 5-second
// timeout.
//
// Returns an error if the server fails to bind to the configured port.
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/sse", s.handleSSE)
	if s.assets != nil {
		mux.HandleFunc("/", s.handleDashboard)
	}
	if s.metrics != nil {
		mux.HandleFunc("/metrics", s.handleMetrics)
	}

	// create listener first to verify port availability synchronously
	addr := fmt.Sprintf(":%d", s.port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to bind to port %d: %w", s.port, err)
	}
	s.addr = ln.Addr().String()

	// wrap mux with middleware; first added = outermost wrapper
	var handler http.Handler = mux
	for i := len(s.middleware) - 1; i >= 0; i-- {
		handler = s.middleware[i](handler)
	}

	s.httpServer = &http.Server{
		Handler: handler,
		// BaseContext derives all request contexts from the server context.
		// When ctx is cancelled, all request contexts are also cancelled,
		// enabling graceful shutdown of long-running handlers like SSE.
		BaseContext: func(_ net.Listener) context.Context {
			return ctx
		},
	}

	tlsEnabled := s.tlsCertFile != "" && s.tlsKeyFile != ""

	go func() {
		var err error
		if tlsEnabled {
			err = s.httpServer.ServeTLS(ln, s.tlsCertFile, s.tlsKeyFile)
		} else {
			err = s.httpServer.Serve(ln)
		}
		if err != nil && err != http.ErrServerClosed {
			s.logger.Error("http server error", "error", err)
		}
	}()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
			s.logger.Error("http server shutdown error", "error", err)
		}
	}()

	return nil
}

// Addr returns the actual network address the server is listening on (e.g. "[::]:8080").
// Returns an empty string if Start has not been called successfully.
func (s *Server) Addr() string {
	return s.addr
}

// handleMetrics serves Prometheus text exposition format metrics.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	if err := s.metrics.WritePrometheus(w); err != nil {
		s.logger.Error("failed to write metrics response", "error", err)
	}
}

// handleDashboard serves the main dashboard page.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	if s.assets == nil {
		http.Error(w, "Dashboard not found", http.StatusInternalServerError)
		return
	}

	content, err := fs.ReadFile(s.assets, "assets/index.html")
	if err != nil {
		http.Error(w, "Dashboard not found", http.StatusInternalServerError)
		return
	}

	// apply title substitution with HTML escaping to prevent XSS
	title := s.title
	if title == "" {
		title = defaultTitle
	}
	safeTitle := html.EscapeString(title)
	rendered := strings.ReplaceAll(string(content), titlePlaceholder, safeTitle)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err = w.Write([]byte(rendered)); err != nil {
		s.logger.Error("failed to write dashboard response", "error", err)
	}
}

// handleStatus returns all current statuses as JSON.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	statuses := s.store.GetAll()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")

	if err := json.NewEncoder(w).Encode(statuses); err != nil {
		s.logger.Error("failed to encode status response", "error", err)
	}
}

// handleSSE streams status updates via Server-Sent Events.
//
// The handler uses write deadlines to prevent goroutine leaks when clients are
// slow or disconnected. Without deadlines, a blocked Fprintf call would prevent
// the handler from detecting context cancellation or channel closure.
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	// check if flushing is supported
	if _, ok := w.(http.Flusher); !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	rc := http.NewResponseController(w)
	deadlinesSupported := true

	// writeAndFlush writes SSE data with a deadline to prevent blocking forever.
	// If the client is slow or disconnected, the write will timeout rather than
	// blocking indefinitely, allowing the handler to detect shutdown signals.
	writeAndFlush := func(data []byte) error {
		if deadlinesSupported {
			if err := rc.SetWriteDeadline(time.Now().Add(sseWriteTimeout)); err != nil {
				// deadline not supported by underlying connection, continue without
				s.logger.Warn("sse write deadlines not supported", "error", err)
				deadlinesSupported = false
			}
		}

		if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			return err
		}

		// ResponseController.Flush respects the write deadline
		return rc.Flush()
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ch := s.store.Subscribe()
	defer s.store.Unsubscribe(ch)

	for _, status := range s.store.GetAll() {
		data, err := json.Marshal(status)
		if err != nil {
			continue
		}
		if err := writeAndFlush(data); err != nil {
			return
		}
	}

	for {
		select {
		case result, ok := <-ch:
			if !ok {
				return
			}
			data, err := json.Marshal(result)
			if err != nil {
				continue
			}
			if err := writeAndFlush(data); err != nil {
				return
			}

		case <-r.Context().Done():
			// request context is derived from server context via BaseContext,
			// so this fires on both client disconnect AND server shutdown
			return
		}
	}
}
