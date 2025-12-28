package pulseboard

import (
	"errors"
	"net/http"
	"time"
)

// endpointConfig holds mutable state during endpoint construction.
type endpointConfig struct {
	labels    map[string]string
	headers   map[string]string
	timeout   time.Duration
	extractor StatusExtractor
	method    string
	interval  time.Duration
}

// EndpointOption is a function that configures an [Endpoint] during construction.
//
// EndpointOption implements the functional options pattern, allowing optional
// configuration to be passed to [NewEndpoint] in a type-safe, extensible way.
// Options return an error if validation fails.
//
// Built-in options: [WithLabels], [WithHeaders], [WithTimeout], [WithExtractor].
type EndpointOption func(*endpointConfig) error

// WithLabels adds metadata labels to the endpoint for grouping and filtering.
//
// Labels are key-value pairs that appear in the dashboard and can be used
// to categorize endpoints (e.g., by environment, team, or service).
//
// Accepts variadic key-value pairs. The number of arguments must be even.
//
// Example:
//
//	ep, err := pulseboard.NewEndpoint("API", url,
//	    pulseboard.WithLabels("env", "production", "team", "platform"),
//	)
//
// Returns an error if an odd number of arguments is provided.
func WithLabels(keyValues ...string) EndpointOption {
	return func(cfg *endpointConfig) error {
		if len(keyValues)%2 != 0 {
			return errors.New("WithLabels requires an even number of arguments (key-value pairs)")
		}
		for i := 0; i < len(keyValues); i += 2 {
			cfg.labels[keyValues[i]] = keyValues[i+1]
		}
		return nil
	}
}

// WithHeaders adds custom HTTP headers to poll requests for this endpoint.
//
// Use this for endpoints that require authentication or custom headers.
// Headers are sent with every poll request to this endpoint.
//
// Accepts variadic key-value pairs. The number of arguments must be even.
//
// Example:
//
//	ep, err := pulseboard.NewEndpoint("API", url,
//	    pulseboard.WithHeaders("Authorization", "Bearer token123"),
//	)
//
// Returns an error if an odd number of arguments is provided.
func WithHeaders(keyValues ...string) EndpointOption {
	return func(cfg *endpointConfig) error {
		if len(keyValues)%2 != 0 {
			return errors.New("WithHeaders requires an even number of arguments (key-value pairs)")
		}
		for i := 0; i < len(keyValues); i += 2 {
			cfg.headers[keyValues[i]] = keyValues[i+1]
		}
		return nil
	}
}

// WithTimeout sets the HTTP request timeout for this endpoint.
//
// If the endpoint does not respond within this duration, the poll is
// considered failed and the endpoint status is set to [StatusDown].
// Defaults to 10 seconds if not specified.
//
// Example:
//
//	ep, err := pulseboard.NewEndpoint("Slow API", url,
//	    pulseboard.WithTimeout(30 * time.Second),
//	)
//
// Returns an error if the duration is zero or negative.
func WithTimeout(d time.Duration) EndpointOption {
	return func(cfg *endpointConfig) error {
		if d <= 0 {
			return errors.New("timeout must be positive")
		}
		cfg.timeout = d
		return nil
	}
}

// WithExtractor sets a custom [StatusExtractor] for this endpoint.
//
// The extractor determines how to interpret the HTTP response as a [Status].
// If not specified, the endpoint uses [DefaultExtractor], which tries
// to extract a JSON "status" field, then falls back to HTTP status code.
//
// Example:
//
//	ep, err := pulseboard.NewEndpoint("API", url,
//	    pulseboard.WithExtractor(pulseboard.JSONFieldExtractor("data.health")),
//	)
func WithExtractor(e StatusExtractor) EndpointOption {
	return func(cfg *endpointConfig) error {
		cfg.extractor = e
		return nil
	}
}

// WithMethod sets the HTTP method for health check requests.
//
// Supported methods are GET (default), HEAD, and POST. Use HEAD for
// endpoints where you only need to check reachability without downloading
// the response body. Use POST for health endpoints that require it.
//
// If not specified, GET is used.
//
// Example:
//
//	ep, err := pulseboard.NewEndpoint("API", url,
//	    pulseboard.WithMethod("HEAD"),
//	)
//
// Returns an error if the method is not GET, HEAD, or POST.
func WithMethod(method string) EndpointOption {
	return func(cfg *endpointConfig) error {
		switch method {
		case http.MethodGet, http.MethodHead, http.MethodPost:
			cfg.method = method
			return nil
		default:
			return errors.New("method must be GET, HEAD, or POST")
		}
	}
}

// WithInterval sets a custom polling interval for this endpoint.
//
// When set, this endpoint is polled at the specified interval instead of
// the global polling interval. Use this to poll critical endpoints more
// frequently or less important endpoints less frequently.
//
// The interval must be at least 1 second and at most 1 hour.
// Returns an error if the interval is outside these bounds.
//
// If not specified, the endpoint uses the global polling interval
// configured via [WithPollingInterval].
//
// Note: The interval is measured from when a poll starts, not when it completes.
// For slow endpoints, effective interval = configured interval + poll duration.
//
// Example:
//
//	critical, _ := pulseboard.NewEndpoint("Critical API", url,
//	    pulseboard.WithInterval(5 * time.Second),  // poll every 5s
//	)
//
//	lowPriority, _ := pulseboard.NewEndpoint("Background Job", url,
//	    pulseboard.WithInterval(60 * time.Second), // poll every minute
//	)
func WithInterval(d time.Duration) EndpointOption {
	return func(cfg *endpointConfig) error {
		if d < time.Second {
			return errors.New("interval must be at least 1 second")
		}
		if d > time.Hour {
			return errors.New("interval must not exceed 1 hour")
		}
		cfg.interval = d
		return nil
	}
}
