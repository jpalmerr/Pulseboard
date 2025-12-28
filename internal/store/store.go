package store

import "time"

// StatusResult represents the current status of an endpoint in storage.
//
// StatusResult is the storage representation of endpoint status, optimized
// for JSON serialization (used by the REST API and SSE). It is decoupled
// from the poller's internal types to allow independent evolution.
type StatusResult struct {
	// Name is the endpoint's display name.
	Name string `json:"name"`

	// URL is the target URL that was polled.
	URL string `json:"url"`

	// Status is the determined health status (e.g., "up", "down", "degraded").
	Status string `json:"status"`

	// Labels contains key-value metadata for grouping and filtering.
	Labels map[string]string `json:"labels"`

	// ResponseTimeMs is the request latency in milliseconds.
	ResponseTimeMs int64 `json:"response_time_ms"`

	// CheckedAt is the timestamp of the last poll.
	CheckedAt time.Time `json:"checked_at"`

	// Error contains the error message if the poll failed.
	// nil indicates no error (though status may still be "down").
	Error *string `json:"error"`
}

// Store defines the interface for storing and subscribing to status updates.
//
// Store implementations must be safe for concurrent access. The pub/sub
// mechanism allows real-time updates to be pushed to connected clients
// (e.g., via Server-Sent Events).
type Store interface {
	// Update stores a new status result and notifies all subscribers.
	// The result is keyed by Name, so subsequent updates replace previous values.
	Update(result StatusResult)

	// GetAll returns all currently stored status results.
	// The returned slice is a snapshot; modifications do not affect the store.
	GetAll() []StatusResult

	// Subscribe returns a channel that receives status updates.
	// The returned channel has a buffer; slow consumers may miss updates.
	// Caller must call Unsubscribe when done to prevent resource leaks.
	Subscribe() <-chan StatusResult

	// Unsubscribe removes a subscription and closes the channel.
	// Safe to call with a channel that was already unsubscribed.
	Unsubscribe(ch <-chan StatusResult)
}
