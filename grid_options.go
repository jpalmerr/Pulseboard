package pulseboard

import (
	"errors"
	"fmt"
	"net/http"
	"time"
)

// gridConfig holds configuration during endpoint grid construction.
type gridConfig struct {
	urlTemplate  string
	dimensions   map[string][]string
	staticLabels map[string]string
	headers      map[string]string
	timeout      time.Duration
	extractor    StatusExtractor
	method       string
	interval     time.Duration
}

// GridOption configures endpoint grid generation.
// GridOption implements the functional options pattern for [NewEndpointGrid].
type GridOption func(*gridConfig) error

// WithURLTemplate sets the URL template for endpoint generation.
// The template uses Go's text/template syntax with dimension keys as variables.
//
// Example:
//
//	WithURLTemplate("https://api.example.com/health?env={{.env}}&region={{.region}}")
//
// Returns an error if the template string is empty.
func WithURLTemplate(tmpl string) GridOption {
	return func(cfg *gridConfig) error {
		if tmpl == "" {
			return errors.New("URL template required")
		}
		cfg.urlTemplate = tmpl
		return nil
	}
}

// WithDimensions sets the dimension values for cartesian product expansion.
// Each key in the map becomes a template variable, and the cartesian product
// of all values generates the endpoint combinations.
//
// Example:
//
//	WithDimensions(map[string][]string{
//	    "env":    {"prod", "staging"},
//	    "region": {"us-east", "eu-west"},
//	})
//
// Returns an error if the map is empty, any dimension has no values,
// or any value is an empty string.
func WithDimensions(dims map[string][]string) GridOption {
	return func(cfg *gridConfig) error {
		if len(dims) == 0 {
			return errors.New("at least one dimension required")
		}
		for k, vals := range dims {
			if len(vals) == 0 {
				return fmt.Errorf("dimension '%s' has no values", k)
			}
			for i, v := range vals {
				if v == "" {
					return fmt.Errorf("dimension '%s' contains empty value at index %d", k, i)
				}
			}
		}
		cfg.dimensions = dims
		return nil
	}
}

// WithGridLabels adds static labels to all generated endpoints.
// These labels are merged with auto-generated dimension labels.
// On collision, static labels take precedence over dimension labels.
//
// Accepts variadic key-value pairs. The number of arguments must be even.
//
// Example:
//
//	WithGridLabels("team", "platform", "tier", "critical")
func WithGridLabels(keyValues ...string) GridOption {
	return func(cfg *gridConfig) error {
		if len(keyValues)%2 != 0 {
			return errors.New("WithGridLabels requires an even number of arguments (key-value pairs)")
		}
		if cfg.staticLabels == nil {
			cfg.staticLabels = make(map[string]string)
		}
		for i := 0; i < len(keyValues); i += 2 {
			cfg.staticLabels[keyValues[i]] = keyValues[i+1]
		}
		return nil
	}
}

// WithGridHeaders adds HTTP headers to all generated endpoints.
//
// Accepts variadic key-value pairs. The number of arguments must be even.
//
// Example:
//
//	WithGridHeaders("Authorization", "Bearer token")
func WithGridHeaders(keyValues ...string) GridOption {
	return func(cfg *gridConfig) error {
		if len(keyValues)%2 != 0 {
			return errors.New("WithGridHeaders requires an even number of arguments (key-value pairs)")
		}
		if cfg.headers == nil {
			cfg.headers = make(map[string]string)
		}
		for i := 0; i < len(keyValues); i += 2 {
			cfg.headers[keyValues[i]] = keyValues[i+1]
		}
		return nil
	}
}

// WithGridTimeout sets the HTTP request timeout for all generated endpoints.
//
// Returns an error if the duration is negative.
// A duration of zero is valid and means use the endpoint default.
func WithGridTimeout(d time.Duration) GridOption {
	return func(cfg *gridConfig) error {
		if d < 0 {
			return errors.New("timeout cannot be negative")
		}
		cfg.timeout = d
		return nil
	}
}

// WithGridExtractor sets a custom [StatusExtractor] for all generated endpoints.
// If nil, endpoints use [DefaultExtractor].
func WithGridExtractor(e StatusExtractor) GridOption {
	return func(cfg *gridConfig) error {
		cfg.extractor = e
		return nil
	}
}

// WithGridMethod sets the HTTP method for all generated endpoints.
//
// Supported methods are GET (default), HEAD, and POST.
//
// Returns an error if the method is not GET, HEAD, or POST.
func WithGridMethod(method string) GridOption {
	return func(cfg *gridConfig) error {
		switch method {
		case http.MethodGet, http.MethodHead, http.MethodPost:
			cfg.method = method
			return nil
		default:
			return errors.New("method must be GET, HEAD, or POST")
		}
	}
}

// WithGridInterval sets a custom polling interval for all generated endpoints.
// This overrides the global polling interval set on [PulseBoard].
//
// The interval must be between 1 second and 1 hour.
// A zero duration means use the global polling interval.
//
// Note: The interval is measured from when a poll starts, not when it completes.
// For slow endpoints, effective interval = configured interval + poll duration.
//
// Example:
//
//	WithGridInterval(30 * time.Second)
func WithGridInterval(d time.Duration) GridOption {
	return func(cfg *gridConfig) error {
		if d < 0 {
			return errors.New("interval cannot be negative")
		}
		if d != 0 && d < time.Second {
			return errors.New("interval must be at least 1 second")
		}
		if d > time.Hour {
			return errors.New("interval must not exceed 1 hour")
		}
		cfg.interval = d
		return nil
	}
}
