package pulseboard

import (
	"errors"
	"net/url"
	"time"
)

const defaultEndpointTimeout = 10 * time.Second

// Endpoint represents a target URL to monitor for health status.
//
// Endpoint is immutable after creation via [NewEndpoint]. All fields are
// private with getter methods that return copies of mutable data (maps),
// ensuring the endpoint cannot be modified after construction.
//
// Endpoints are configured using the functional options pattern with
// [EndpointOption] functions such as [WithLabels], [WithHeaders],
// [WithTimeout], [WithExtractor], [WithMethod], and [WithInterval].
type Endpoint struct {
	name      string
	url       string
	labels    map[string]string
	headers   map[string]string
	timeout   time.Duration
	extractor StatusExtractor
	method    string
	interval  time.Duration
}

// Name returns the endpoint's display name.
// The name is used for identification in the dashboard and logs.
func (e Endpoint) Name() string {
	return e.name
}

// URL returns the endpoint's target URL as a string.
// This is the URL that will be polled for health checks.
func (e Endpoint) URL() string {
	return e.url
}

// Labels returns a copy of the endpoint's labels.
// Labels are key-value metadata used for grouping and filtering endpoints
// in the dashboard. Returns nil if no labels are set.
func (e Endpoint) Labels() map[string]string {
	return copyMap(e.labels)
}

// Headers returns a copy of the endpoint's custom HTTP headers.
// These headers are sent with every poll request to this endpoint.
// Returns nil if no custom headers are set.
func (e Endpoint) Headers() map[string]string {
	return copyMap(e.headers)
}

// Timeout returns the endpoint's HTTP request timeout.
// Defaults to 10 seconds if not explicitly set via [WithTimeout].
func (e Endpoint) Timeout() time.Duration {
	return e.timeout
}

// Extractor returns the endpoint's [StatusExtractor] function.
// Returns nil if no custom extractor was specified. When nil, the polling
// layer applies [DefaultExtractor].
func (e Endpoint) Extractor() StatusExtractor {
	return e.extractor
}

// Method returns the HTTP method for health check requests.
// Returns empty string if not explicitly set, which means GET will be used.
func (e Endpoint) Method() string {
	return e.method
}

// Interval returns the endpoint's custom polling interval.
// Returns 0 if no custom interval was specified, meaning the global
// polling interval configured via [WithPollingInterval] should be used.
func (e Endpoint) Interval() time.Duration {
	return e.interval
}

// NewEndpoint creates an [Endpoint] with the given name, URL, and options.
//
// The name parameter is a human-readable identifier displayed in the dashboard.
// The rawURL parameter must be a valid URL with a scheme (http:// or https://).
//
// Options are applied in order using the functional options pattern.
// See [WithLabels], [WithHeaders], [WithTimeout], and [WithExtractor].
//
// Returns an error if the name is empty or the URL is invalid.
//
// Example:
//
//	ep, err := pulseboard.NewEndpoint("API Health", "https://api.example.com/health",
//	    pulseboard.WithLabels("env", "prod"),
//	    pulseboard.WithTimeout(5 * time.Second),
//	)
func NewEndpoint(name, rawURL string, opts ...EndpointOption) (Endpoint, error) {
	if name == "" {
		return Endpoint{}, errors.New("endpoint name cannot be empty")
	}

	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return Endpoint{}, errors.New("invalid URL: " + err.Error())
	}
	if parsedURL.Scheme == "" {
		return Endpoint{}, errors.New("URL must have a scheme (http:// or https://)")
	}

	cfg := &endpointConfig{
		labels:  make(map[string]string),
		headers: make(map[string]string),
		timeout: defaultEndpointTimeout,
	}

	for _, opt := range opts {
		if err := opt(cfg); err != nil {
			return Endpoint{}, err
		}
	}

	return Endpoint{
		name:      name,
		url:       rawURL,
		labels:    cfg.labels,
		headers:   cfg.headers,
		timeout:   cfg.timeout,
		extractor: cfg.extractor,
		method:    cfg.method,
		interval:  cfg.interval,
	}, nil
}

// copyMap returns a shallow copy of the map.
func copyMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	cp := make(map[string]string, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}
