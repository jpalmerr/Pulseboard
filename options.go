package pulseboard

import (
	"errors"
	"log/slog"
	"time"
)

// pbConfig holds mutable state during PulseBoard construction.
type pbConfig struct {
	title           string
	endpoints       []Endpoint
	pollingInterval time.Duration
	port            int
	maxConcurrency  int
	logger          *slog.Logger
	statusCallbacks []func(StatusResult)
}

// Option is a function that configures a [PulseBoard] instance during construction.
//
// Option implements the functional options pattern, allowing optional
// configuration to be passed to [New] in a type-safe, extensible way.
// Options return an error if validation fails.
//
// Built-in options: [WithEndpoint], [WithEndpoints], [WithPollingInterval],
// [WithPort], [WithMaxConcurrency].
type Option func(*pbConfig) error

// WithEndpoint adds a single [Endpoint] to the polling list.
//
// Can be called multiple times to add multiple endpoints. At least one
// endpoint must be configured for [New] to succeed.
//
// Example:
//
//	pb, err := pulseboard.New(
//	    pulseboard.WithEndpoint(ep1),
//	    pulseboard.WithEndpoint(ep2),
//	)
func WithEndpoint(e Endpoint) Option {
	return func(cfg *pbConfig) error {
		cfg.endpoints = append(cfg.endpoints, e)
		return nil
	}
}

// WithEndpoints adds multiple [Endpoint] values to the polling list.
//
// This is a convenience function for adding several endpoints at once.
// Equivalent to calling [WithEndpoint] multiple times.
//
// Example:
//
//	pb, err := pulseboard.New(
//	    pulseboard.WithEndpoints(ep1, ep2, ep3),
//	)
func WithEndpoints(endpoints ...Endpoint) Option {
	return func(cfg *pbConfig) error {
		cfg.endpoints = append(cfg.endpoints, endpoints...)
		return nil
	}
}

// WithPollingInterval sets how often all endpoints are polled.
//
// The interval applies globally to all endpoints. Each polling cycle
// polls all endpoints concurrently (up to [WithMaxConcurrency] limit).
// Defaults to 15 seconds if not specified.
//
// Example:
//
//	pb, err := pulseboard.New(
//	    pulseboard.WithEndpoint(ep),
//	    pulseboard.WithPollingInterval(30 * time.Second),
//	)
//
// Returns an error if the duration is zero or negative.
func WithPollingInterval(d time.Duration) Option {
	return func(cfg *pbConfig) error {
		if d <= 0 {
			return errors.New("polling interval must be positive")
		}
		cfg.pollingInterval = d
		return nil
	}
}

// WithPort sets the HTTP port for the dashboard server.
//
// The dashboard UI and API will be available at http://localhost:<port>.
// Defaults to 8080 if not specified.
//
// Example:
//
//	pb, err := pulseboard.New(
//	    pulseboard.WithEndpoint(ep),
//	    pulseboard.WithPort(9090),
//	)
//
// Returns an error if the port is outside the valid range (1-65535).
func WithPort(port int) Option {
	return func(cfg *pbConfig) error {
		if port < 1 || port > 65535 {
			return errors.New("port must be between 1 and 65535")
		}
		cfg.port = port
		return nil
	}
}

// WithMaxConcurrency sets the maximum number of concurrent HTTP requests.
//
// This limits how many endpoints are polled simultaneously during each
// polling cycle. Use this to avoid overwhelming target services or to
// respect rate limits. Defaults to 10 if not specified.
//
// Example:
//
//	pb, err := pulseboard.New(
//	    pulseboard.WithEndpoints(endpoints...),
//	    pulseboard.WithMaxConcurrency(5),
//	)
//
// Returns an error if the value is zero or negative.
func WithMaxConcurrency(n int) Option {
	return func(cfg *pbConfig) error {
		if n <= 0 {
			return errors.New("max concurrency must be positive")
		}
		cfg.maxConcurrency = n
		return nil
	}
}

// WithLogger sets a custom [slog.Logger] for the PulseBoard instance.
//
// This allows SDK consumers to control where logs are written and in what
// format. If not specified, [slog.Default] is used.
//
// Example:
//
//	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
//	pb, err := pulseboard.New(
//	    pulseboard.WithEndpoint(ep),
//	    pulseboard.WithLogger(logger),
//	)
//
// Returns an error if the logger is nil.
func WithLogger(logger *slog.Logger) Option {
	return func(cfg *pbConfig) error {
		if logger == nil {
			return errors.New("logger cannot be nil")
		}
		cfg.logger = logger
		return nil
	}
}

// WithStatusCallback registers a function to be called on every poll completion.
//
// The callback receives a [StatusResult] containing the poll outcome, including
// the endpoint name, URL, status, latency, and the raw HTTP response.
//
// Multiple callbacks may be registered by calling WithStatusCallback multiple
// times; they execute in registration order.
//
// IMPORTANT: Callbacks must be non-blocking. Long-running operations should
// dispatch work to a separate goroutine. Blocking callbacks will delay
// subsequent poll result processing.
//
// Callbacks are invoked synchronously from a single goroutine. Panics within
// callbacks are recovered and logged; they do not crash the scheduler.
//
// Example:
//
//	pb, err := pulseboard.New(
//	    pulseboard.WithEndpoint(api),
//	    pulseboard.WithStatusCallback(func(result pulseboard.StatusResult) {
//	        if result.Status == pulseboard.StatusDown {
//	            log.Printf("ALERT: %s is down!", result.EndpointName)
//	        }
//	    }),
//	)
//
// Nil callbacks are silently ignored.
func WithStatusCallback(cb func(StatusResult)) Option {
	return func(cfg *pbConfig) error {
		if cb == nil {
			return nil // no-op for nil callback (safe to call)
		}
		cfg.statusCallbacks = append(cfg.statusCallbacks, cb)
		return nil
	}
}

// WithTitle sets the dashboard title displayed in the browser tab and header.
//
// If not specified, defaults to "PulseBoard".
//
// Example:
//
//	pb, err := pulseboard.New(
//	    pulseboard.WithEndpoint(ep),
//	    pulseboard.WithTitle("Video Channel Healthchecks"),
//	)
func WithTitle(title string) Option {
	return func(cfg *pbConfig) error {
		cfg.title = title
		return nil
	}
}
