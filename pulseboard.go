package pulseboard

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jpalmerr/pulseboard/dashboard"
	"github.com/jpalmerr/pulseboard/internal/poller"
	"github.com/jpalmerr/pulseboard/internal/server"
	"github.com/jpalmerr/pulseboard/internal/store"
)

const (
	defaultPollingInterval = 15 * time.Second
	defaultPort            = 8080
	defaultMaxConcurrency  = 10
)

// PulseBoard is the main orchestrator for endpoint polling and dashboard serving.
//
// PulseBoard coordinates the polling of HTTP endpoints, stores their status,
// and serves a real-time dashboard via HTTP. It is created using [New] with
// functional options and started with [PulseBoard.Start].
//
// The typical lifecycle is:
//
//	pb, err := pulseboard.New(pulseboard.WithEndpoint(ep))
//	if err != nil {
//	    slog.Error("failed to create pulseboard", "error", err)
//	    os.Exit(1)
//	}
//
//	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
//	defer cancel()
//
//	pb.Start(ctx) // blocks until context cancelled
//
// The caller controls the lifecycle via the context. Cancel the context to
// trigger graceful shutdown.
type PulseBoard struct {
	title           string
	endpoints       []Endpoint
	pollingInterval time.Duration
	port            int
	maxConcurrency  int
	logger          *slog.Logger
	statusCallbacks []func(StatusResult)
}

// New creates a new [PulseBoard] instance with the given options.
//
// At least one endpoint must be configured via [WithEndpoint] or [WithEndpoints].
// Other options have sensible defaults:
//   - Polling interval: 15 seconds
//   - Port: 8080
//   - Max concurrency: 10
//
// Returns an error if no endpoints are configured or if any option is invalid.
//
// Example:
//
//	pb, err := pulseboard.New(
//	    pulseboard.WithEndpoint(ep),
//	    pulseboard.WithPollingInterval(30 * time.Second),
//	    pulseboard.WithPort(9090),
//	)
func New(opts ...Option) (*PulseBoard, error) {
	cfg := &pbConfig{
		endpoints:       []Endpoint{},
		pollingInterval: defaultPollingInterval,
		port:            defaultPort,
		maxConcurrency:  defaultMaxConcurrency,
	}

	for _, opt := range opts {
		if err := opt(cfg); err != nil {
			return nil, err
		}
	}

	if len(cfg.endpoints) == 0 {
		return nil, errors.New("at least one endpoint is required")
	}

	// validate endpoint name uniqueness (required for per-endpoint interval tracking)
	seen := make(map[string]bool, len(cfg.endpoints))
	for _, ep := range cfg.endpoints {
		if seen[ep.name] {
			return nil, fmt.Errorf("duplicate endpoint name: %q", ep.name)
		}
		seen[ep.name] = true
	}

	if cfg.port < 1 || cfg.port > 65535 {
		return nil, fmt.Errorf("port must be between 1 and 65535, got %d", cfg.port)
	}

	// default to slog.Default() if no logger provided
	logger := cfg.logger
	if logger == nil {
		logger = slog.Default()
	}

	return &PulseBoard{
		title:           cfg.title,
		endpoints:       cfg.endpoints,
		pollingInterval: cfg.pollingInterval,
		port:            cfg.port,
		maxConcurrency:  cfg.maxConcurrency,
		logger:          logger,
		statusCallbacks: cfg.statusCallbacks,
	}, nil
}

// Start begins polling endpoints and serving the dashboard.
//
// Start is a blocking call that runs until the provided context is cancelled.
// During execution:
//
//   - All configured endpoints are polled immediately, then at the configured interval
//   - The HTTP server starts on the configured port
//   - Poll results are logged to stdout
//   - The dashboard is available at http://localhost:<port>
//
// The caller controls the lifecycle via context cancellation. For signal handling,
// use [signal.NotifyContext]:
//
//	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
//	defer cancel()
//	pb.Start(ctx)
//
// Returns nil on graceful shutdown. Returns an error if the HTTP server fails to start.
func (pb *PulseBoard) Start(ctx context.Context) error {
	pb.logger.Info("pulseboard starting", "endpoint_count", len(pb.endpoints))
	pb.logger.Info("polling configured", "interval", pb.pollingInterval.String())
	pb.logger.Info("dashboard available", "url", fmt.Sprintf("http://localhost:%d", pb.port))

	// check if context already cancelled
	if ctx.Err() != nil {
		return nil
	}

	// convert endpoints to poller format
	pollerEndpoints := pb.toPollerEndpoints()

	// create the store
	statusStore := store.NewMemoryStore()

	// start the polling scheduler
	scheduler := poller.NewScheduler(pollerEndpoints, pb.pollingInterval, pb.maxConcurrency, pb.logger)
	scheduler.Start(ctx)

	// track the results consumer goroutine to ensure clean shutdown
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for result := range scheduler.Results() {
			// store update first (callbacks fire after data is persisted)
			storeResult := pollerResultToStoreResult(result)
			statusStore.Update(storeResult)

			// invoke status callbacks (after store update)
			if len(pb.statusCallbacks) > 0 {
				publicResult := pollerResultToPublicResult(result)
				for _, cb := range pb.statusCallbacks {
					invokeCallbackSafe(cb, publicResult, pb.logger)
				}
			}

			// log poll results (DEBUG level for success to reduce noise)
			logAttrs := []any{
				"status", result.Status,
				"endpoint", result.EndpointName,
				"url", result.URL,
				"latency_ms", result.Latency.Milliseconds(),
			}
			if result.Error != nil {
				pb.logger.Warn("poll completed with error", append(logAttrs, "error", result.Error.Error())...)
			} else {
				pb.logger.Debug("poll completed", logAttrs...)
			}
		}
	}()

	// cleanup function ensures scheduler is stopped and all results are processed
	cleanup := func() {
		scheduler.Stop() // closes results channel
		wg.Wait()        // wait for all results to be processed
	}

	// start the HTTP server
	httpServer := server.NewServer(statusStore, pb.port, dashboard.Assets, pb.title, pb.logger)
	if err := httpServer.Start(ctx); err != nil {
		cleanup()
		return fmt.Errorf("failed to start HTTP server: %w", err)
	}

	<-ctx.Done()
	cleanup()
	pb.logger.Info("pulseboard stopped")
	return nil
}

// toPollerEndpoints converts Endpoint slice to poller.EndpointInfo slice.
func (pb *PulseBoard) toPollerEndpoints() []poller.EndpointInfo {
	result := make([]poller.EndpointInfo, len(pb.endpoints))

	for i, ep := range pb.endpoints {
		var extractor poller.StatusExtractor
		if ep.extractor != nil {
			// wrap the pulseboard extractor to return string
			pbExtractor := ep.extractor
			extractor = func(body []byte, statusCode int) string {
				return pbExtractor(body, statusCode).String()
			}
		}

		result[i] = poller.EndpointInfo{
			Name:      ep.name,
			URL:       ep.url,
			Labels:    copyMap(ep.labels),
			Headers:   copyMap(ep.headers),
			Timeout:   ep.timeout,
			Extractor: extractor,
			Method:    ep.method,
			Interval:  ep.interval,
		}
	}

	return result
}

// Endpoints returns a copy of the configured endpoints.
//
// The returned slice is a copy; modifying it does not affect the PulseBoard.
// Each [Endpoint] in the slice is immutable.
func (pb *PulseBoard) Endpoints() []Endpoint {
	cp := make([]Endpoint, len(pb.endpoints))
	copy(cp, pb.endpoints)
	return cp
}

// Port returns the configured HTTP port for the dashboard server.
func (pb *PulseBoard) Port() int {
	return pb.port
}

// PollingInterval returns the configured interval between polling cycles.
func (pb *PulseBoard) PollingInterval() time.Duration {
	return pb.pollingInterval
}

// pollerResultToStoreResult converts a poller result to a store result.
func pollerResultToStoreResult(pr poller.StatusResult) store.StatusResult {
	var errStr *string
	if pr.Error != nil {
		s := pr.Error.Error()
		errStr = &s
	}

	return store.StatusResult{
		Name:           pr.EndpointName,
		URL:            pr.URL,
		Status:         pr.Status,
		Labels:         pr.Labels,
		ResponseTimeMs: pr.Latency.Milliseconds(),
		CheckedAt:      pr.CheckedAt,
		Error:          errStr,
	}
}

// pollerResultToPublicResult converts internal poller result to public API type.
// Creates defensive copies of mutable fields to prevent data races.
func pollerResultToPublicResult(pr poller.StatusResult) StatusResult {
	return StatusResult{
		EndpointName: pr.EndpointName,
		URL:          pr.URL,
		Status:       Status(pr.Status),
		Labels:       copyMap(pr.Labels),
		Latency:      pr.Latency,
		CheckedAt:    pr.CheckedAt,
		Error:        pr.Error,
		RawResponse:  copyBytes(pr.RawResponse),
		StatusCode:   pr.StatusCode,
	}
}

// copyBytes returns a copy of the byte slice, or nil if input is nil.
func copyBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	return append([]byte(nil), b...)
}

// invokeCallbackSafe calls a status callback with panic recovery.
// Panics are logged but do not propagate.
func invokeCallbackSafe(cb func(StatusResult), result StatusResult, logger *slog.Logger) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("status callback panicked",
				"panic", r,
				"endpoint", result.EndpointName,
			)
		}
	}()
	cb(result)
}
