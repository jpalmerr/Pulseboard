package pulseboard

import (
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// pbConfig holds mutable state during PulseBoard construction.
type pbConfig struct {
	title                string
	endpoints            []Endpoint
	pollingInterval      time.Duration
	port                 int
	maxConcurrency       int
	logger               *slog.Logger
	statusCallbacks       []func(StatusResult)
	statusChangeCallbacks []func(StatusChange)
	blockPrivateNetworks  bool
	allowedHosts         []string
	middleware           []func(http.Handler) http.Handler
	staleThreshold       time.Duration
	staleThresholdSet    bool
	metricsEnabled       bool

	// Server TLS
	tlsCertFile string
	tlsKeyFile  string

	// Client TLS
	clientTLSInsecure   bool
	clientTLSMinVersion uint16
	clientTLSCertFile   string
	clientTLSKeyFile    string
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

// WithStatusChangeCallback registers a function called only when an endpoint's
// status transitions from one state to another. The first poll for each endpoint
// is always considered a transition from an empty (unknown) previous status.
//
// Multiple callbacks may be registered; they execute in registration order.
// Panics within callbacks are recovered and logged; they do not affect polling.
//
// Nil callbacks return an error.
func WithStatusChangeCallback(cb func(StatusChange)) Option {
	return func(cfg *pbConfig) error {
		if cb == nil {
			return errors.New("status change callback must not be nil")
		}
		cfg.statusChangeCallbacks = append(cfg.statusChangeCallbacks, cb)
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

// WithBlockPrivateNetworks enables SSRF protection by blocking requests to
// RFC1918 private addresses, loopback, link-local, and cloud metadata CIDRs.
//
// Blocked ranges: 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 127.0.0.0/8,
// 169.254.0.0/16 (cloud metadata), ::1/128, fc00::/7, fe80::/10.
//
// Both the initial URL and any redirect targets are validated. An endpoint
// whose hostname resolves to a blocked IP will fail with a clear error.
//
// Example:
//
//	pb, err := pulseboard.New(
//	    pulseboard.WithEndpoint(ep),
//	    pulseboard.WithBlockPrivateNetworks(),
//	)
func WithBlockPrivateNetworks() Option {
	return func(cfg *pbConfig) error {
		cfg.blockPrivateNetworks = true
		return nil
	}
}

// WithAllowedHosts restricts polling to only the listed hostnames.
//
// When set, any endpoint whose host is not in the list will fail with an error.
// This is an allowlist mode: hosts not explicitly listed are blocked regardless
// of whether they are public or private.
//
// Can be combined with [WithBlockPrivateNetworks] for defence-in-depth.
//
// Example:
//
//	pb, err := pulseboard.New(
//	    pulseboard.WithEndpoints(ep1, ep2),
//	    pulseboard.WithAllowedHosts("api.example.com", "status.example.com"),
//	)
func WithAllowedHosts(hosts ...string) Option {
	return func(cfg *pbConfig) error {
		cfg.allowedHosts = append(cfg.allowedHosts, hosts...)
		return nil
	}
}

// WithMiddleware adds an HTTP middleware to the dashboard server.
//
// Middleware wraps all handlers (dashboard, API, SSE). Multiple middleware
// functions may be registered by calling WithMiddleware multiple times.
// The first middleware added becomes the outermost wrapper.
//
// Example:
//
//	pb, err := pulseboard.New(
//	    pulseboard.WithEndpoint(ep),
//	    pulseboard.WithMiddleware(pulseboard.BasicAuth(func(u, p string) bool {
//	        return u == "admin" && p == "secret"
//	    })),
//	)
//
// Returns an error if the middleware is nil.
func WithMiddleware(mw func(http.Handler) http.Handler) Option {
	return func(cfg *pbConfig) error {
		if mw == nil {
			return errors.New("middleware cannot be nil")
		}
		cfg.middleware = append(cfg.middleware, mw)
		return nil
	}
}

// WithTLS enables HTTPS for the dashboard server.
// Both certFile and keyFile must be non-empty.
// Files are validated at server start time (not at option-apply time).
//
// Example:
//
//	pb, err := pulseboard.New(
//	    pulseboard.WithEndpoint(ep),
//	    pulseboard.WithTLS("/path/to/cert.pem", "/path/to/key.pem"),
//	)
func WithTLS(certFile, keyFile string) Option {
	return func(cfg *pbConfig) error {
		if certFile == "" || keyFile == "" {
			return errors.New("both cert and key files are required for TLS")
		}
		cfg.tlsCertFile = certFile
		cfg.tlsKeyFile = keyFile
		return nil
	}
}

// WithInsecureSkipVerify disables TLS certificate verification for polled endpoints.
// WARNING: Vulnerable to man-in-the-middle attacks. Only use in development or
// for internal services with self-signed certificates.
//
// Example:
//
//	pb, err := pulseboard.New(
//	    pulseboard.WithEndpoint(ep),
//	    pulseboard.WithInsecureSkipVerify(),
//	)
func WithInsecureSkipVerify() Option {
	return func(cfg *pbConfig) error {
		cfg.clientTLSInsecure = true
		return nil
	}
}

// WithTLSMinVersion sets the minimum TLS version for polling connections.
// Use crypto/tls.VersionTLS12 or crypto/tls.VersionTLS13.
// Default when any client TLS option is active: TLS 1.2.
//
// Returns an error if version is not a recognised TLS version constant.
//
// Example:
//
//	pb, err := pulseboard.New(
//	    pulseboard.WithEndpoint(ep),
//	    pulseboard.WithTLSMinVersion(tls.VersionTLS13),
//	)
func WithTLSMinVersion(version uint16) Option {
	return func(cfg *pbConfig) error {
		switch version {
		case tls.VersionTLS10, tls.VersionTLS11, tls.VersionTLS12, tls.VersionTLS13:
			cfg.clientTLSMinVersion = version
			return nil
		default:
			return fmt.Errorf("unknown TLS version 0x%04x", version)
		}
	}
}

// WithClientCert sets a client certificate for mutual TLS when polling endpoints.
// Both certFile and keyFile must be non-empty.
// The certificate pair is loaded at server start time (not at option-apply time).
//
// Example:
//
//	pb, err := pulseboard.New(
//	    pulseboard.WithEndpoint(ep),
//	    pulseboard.WithClientCert("/path/to/client-cert.pem", "/path/to/client-key.pem"),
//	)
func WithClientCert(certFile, keyFile string) Option {
	return func(cfg *pbConfig) error {
		if certFile == "" || keyFile == "" {
			return errors.New("both cert and key files are required for client TLS")
		}
		cfg.clientTLSCertFile = certFile
		cfg.clientTLSKeyFile = keyFile
		return nil
	}
}

// WithMetrics enables the /metrics endpoint with Prometheus-compatible metrics.
//
// The endpoint serves Prometheus text exposition format at /metrics.
// Disabled by default — no /metrics route is registered without this option.
//
// Example:
//
//	pb, err := pulseboard.New(
//	    pulseboard.WithEndpoint(ep),
//	    pulseboard.WithMetrics(),
//	)
func WithMetrics() Option {
	return func(cfg *pbConfig) error {
		cfg.metricsEnabled = true
		return nil
	}
}

// WithStaleThreshold sets the duration after which an endpoint with no updates
// is considered stale. The default is 3x the polling interval.
//
// Pass 0 to disable staleness detection entirely — no checker goroutine will run
// and entries will never be marked stale regardless of how long since their last update.
//
// When not called at all, the default threshold of 3x the polling interval applies.
// For example, with the default polling interval of 15 seconds, the default threshold
// is 45 seconds: any endpoint not updated within 45 seconds will be marked stale.
//
// Example:
//
//	pb, err := pulseboard.New(
//	    pulseboard.WithEndpoint(ep),
//	    pulseboard.WithStaleThreshold(2 * time.Minute),
//	)
//
// Returns an error if the duration is negative (zero is allowed to disable the feature).
func WithStaleThreshold(d time.Duration) Option {
	return func(cfg *pbConfig) error {
		if d < 0 {
			return errors.New("stale threshold must be non-negative")
		}
		cfg.staleThreshold = d
		cfg.staleThresholdSet = true
		return nil
	}
}
