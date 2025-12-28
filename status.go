package pulseboard

import "time"

// Status represents the health state of an endpoint.
//
// Status is a string type that can hold one of four predefined values:
// [StatusUp], [StatusDown], [StatusDegraded], or [StatusUnknown].
// Using a string type allows for easy JSON serialization and human-readable
// logging while maintaining type safety through the defined constants.
type Status string

const (
	// StatusUp indicates the endpoint is healthy and responding normally.
	StatusUp Status = "up"

	// StatusDown indicates the endpoint is unreachable or returning errors.
	StatusDown Status = "down"

	// StatusDegraded indicates the endpoint is partially functional or slow.
	StatusDegraded Status = "degraded"

	// StatusUnknown indicates the status could not be determined.
	// This typically occurs when an extractor cannot parse the response.
	StatusUnknown Status = "unknown"
)

// String returns the string representation of the status.
// This implements the fmt.Stringer interface.
func (s Status) String() string {
	return string(s)
}

// StatusExtractor is a function type that determines the [Status] of an
// endpoint from its HTTP response.
//
// StatusExtractor follows functional programming principles: it is a pure
// function where the same inputs always produce the same output. This makes
// extractors easy to test, compose, and reason about.
//
// Parameters:
//   - body: The HTTP response body as bytes
//   - statusCode: The HTTP status code (e.g., 200, 404, 500)
//
// Returns the determined [Status] value.
//
// Several built-in extractors are provided: [HTTPStatusExtractor],
// [JSONFieldExtractor], [RegexExtractor], and [FirstMatch] for composition.
//
// # Panic Safety
//
// StatusExtractor functions are called within a panic recovery boundary.
// If an extractor panics, the endpoint's status will be set to [StatusDown]
// with an error containing a correlation ID. The full stack trace is logged
// server-side for debugging. This ensures that a misbehaving extractor cannot
// crash the entire PulseBoard server.
type StatusExtractor func(body []byte, statusCode int) Status

// StatusResult holds the outcome of polling a single endpoint.
//
// StatusResult is immutable after creation and contains all information
// about a poll attempt, including the determined status, latency metrics,
// and any error that occurred. The RawResponse field preserves the original
// response body for debugging or custom processing.
type StatusResult struct {
	// EndpointName is the display name of the polled endpoint.
	EndpointName string

	// URL is the target URL that was polled.
	URL string

	// Status is the determined health state of the endpoint.
	Status Status

	// Labels contains the key-value metadata associated with the endpoint.
	Labels map[string]string

	// Latency is the time taken to complete the HTTP request.
	Latency time.Duration

	// CheckedAt is the timestamp when the poll was performed.
	CheckedAt time.Time

	// Error contains any error that occurred during polling.
	// nil indicates the request completed successfully (though Status may still
	// be down, degraded, or unknown based on the response content).
	Error error

	// RawResponse contains the HTTP response body, limited to 1MB.
	RawResponse []byte

	// StatusCode is the HTTP status code returned by the endpoint.
	// Zero if the request failed before receiving a response.
	StatusCode int
}
