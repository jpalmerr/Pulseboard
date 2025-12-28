// Package pulseboard provides a lightweight, embeddable status dashboard
// for monitoring HTTP endpoints in real-time.
//
// PulseBoard is designed as an SDK-first library, allowing developers to
// programmatically configure and deploy status dashboards as part of their
// applications. It follows functional programming principles with immutable
// types, pure functions, and composable configuration via the functional
// options pattern.
//
// # Quick Start
//
// Create endpoints and start the dashboard with graceful shutdown:
//
//	ep, _ := pulseboard.NewEndpoint("API", "https://api.example.com/health")
//	pb, _ := pulseboard.New(pulseboard.WithEndpoint(ep))
//
//	// Set up graceful shutdown on SIGINT/SIGTERM
//	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
//	defer stop()
//
//	pb.Start(ctx) // blocks until context is cancelled
//
// # Configuration
//
// PulseBoard uses the functional options pattern for configuration:
//
//	pb, err := pulseboard.New(
//	    pulseboard.WithEndpoint(ep1),
//	    pulseboard.WithEndpoint(ep2),
//	    pulseboard.WithPollingInterval(30 * time.Second),
//	    pulseboard.WithPort(9090),
//	    pulseboard.WithMaxConcurrency(5),
//	)
//
// Endpoints can also be configured with options:
//
//	ep, err := pulseboard.NewEndpoint("API", "https://api.example.com/health",
//	    pulseboard.WithLabels("env", "production", "team", "platform"),
//	    pulseboard.WithHeaders("Authorization", "Bearer token"),
//	    pulseboard.WithTimeout(5 * time.Second),
//	    pulseboard.WithExtractor(pulseboard.JSONFieldExtractor("data.status")),
//	)
//
// # Status Extractors
//
// Extractors determine how HTTP responses are interpreted as status values.
// Several built-in extractors are provided:
//
//   - [HTTPStatusExtractor]: Maps HTTP status codes to status (2xx=up, 4xx=degraded, 5xx=down)
//   - [JSONFieldExtractor]: Extracts status from a JSON field using dot notation
//   - [RegexExtractor]: Matches response body against a regex pattern
//   - [FirstMatch]: Tries multiple extractors in order, returning the first non-unknown result
//   - [DefaultExtractor]: Tries JSON "status" field, then falls back to HTTP status code
//
// Custom extractors can be created by implementing the [StatusExtractor] function type.
//
// # Architecture
//
// PulseBoard consists of several internal packages (under internal/):
//
//   - internal/poller: Concurrent HTTP polling with worker pool
//   - internal/store: In-memory storage with pub/sub for real-time updates
//   - internal/server: HTTP server with REST API and Server-Sent Events
//   - dashboard: Embedded web UI assets
//
// The internal packages are not part of the public API and may change
// without notice. The library is designed for single-binary deployment
// using Go's embed directive for static assets.
package pulseboard
