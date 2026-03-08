package pulseboard

import "time"

// StatusChange represents a transition between two statuses for a single endpoint.
//
// StatusChange is passed to callbacks registered with [WithStatusChangeCallback]
// and is the payload sent by [WebhookNotifier].
type StatusChange struct {
	// EndpointName is the display name of the endpoint.
	EndpointName string `json:"endpoint_name"`

	// URL is the endpoint's URL.
	URL string `json:"url"`

	// Labels are the endpoint's key-value metadata.
	Labels map[string]string `json:"labels,omitempty"`

	// PreviousStatus is the status before the change.
	// Empty string ("") indicates this is the first poll result for this endpoint.
	PreviousStatus Status `json:"previous_status"`

	// CurrentStatus is the status after the change.
	CurrentStatus Status `json:"current_status"`

	// LatencyMs is the HTTP request duration in milliseconds.
	LatencyMs int64 `json:"latency_ms"`

	// CheckedAt is when the poll completed.
	CheckedAt time.Time `json:"checked_at"`

	// Error is the error string from the poll, if any. Empty string means no error.
	Error string `json:"error,omitempty"`
}

// Status represents the health state of an endpoint.
//
// Status is a string type that can hold one of five predefined values:
// [StatusUp], [StatusDown], [StatusDegraded], [StatusUnknown], or [StatusError].
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

	// StatusError indicates the check mechanism itself failed (extractor panic,
	// JSON parse error, regex mismatch, missing field). The service status is
	// unknown because the check could not complete.
	// This is distinct from [StatusDown] (service unreachable).
	StatusError Status = "error"
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
// If an extractor panics, the endpoint's status will be set to [StatusError]
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
