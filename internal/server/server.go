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
	store      store.Store
	port       int
	httpServer *http.Server
	assets     fs.FS
	title      string
	logger     *slog.Logger
}

// NewServer creates a new HTTP [Server].
//
// Parameters:
//   - st: Store implementation for status data
//   - port: TCP port to listen on
//   - assets: Embedded filesystem containing dashboard assets (may be nil)
//   - title: Dashboard title (defaults to "PulseBoard" if empty)
//   - logger: Logger for server events
//
// The server is not started until [Server.Start] is called.
func NewServer(st store.Store, port int, assets fs.FS, title string, logger *slog.Logger) *Server {
	return &Server{
		store:  st,
		port:   port,
		assets: assets,
		title:  title,
		logger: logger,
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

	// API routes
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/sse", s.handleSSE)

	// serve dashboard assets
	if s.assets != nil {
		// serve index.html at root
		mux.HandleFunc("/", s.handleDashboard)
	}

	// create listener first to verify port availability synchronously
	addr := fmt.Sprintf(":%d", s.port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to bind to port %d: %w", s.port, err)
	}

	s.httpServer = &http.Server{
		Handler: mux,
		// BaseContext derives all request contexts from the server context.
		// When ctx is cancelled, all request contexts are also cancelled,
		// enabling graceful shutdown of long-running handlers like SSE.
		BaseContext: func(_ net.Listener) context.Context {
			return ctx
		},
	}

	go func() {
		if err := s.httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			s.logger.Error("http server error", "error", err)
		}
	}()

	// shutdown on context cancellation
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

	// read index.html from embedded assets
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

	// ResponseController provides deadline-aware write and flush operations.
	// This is the Go 1.20+ idiomatic way to handle write timeouts.
	rc := http.NewResponseController(w)

	// track if write deadlines are supported (may not be for some ResponseWriter impls)
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

	// set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// subscribe to store updates
	ch := s.store.Subscribe()
	defer s.store.Unsubscribe(ch)

	// send initial statuses (also protected by write deadline)
	for _, status := range s.store.GetAll() {
		data, err := json.Marshal(status)
		if err != nil {
			continue
		}
		if err := writeAndFlush(data); err != nil {
			return
		}
	}

	// stream updates
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
