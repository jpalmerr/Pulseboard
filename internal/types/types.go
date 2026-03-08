package types

import "time"

// StatusResult holds the outcome of polling a single endpoint.
// This is the single internal representation used by poller, store, and server.
type StatusResult struct {
	EndpointName   string            `json:"name"`
	URL            string            `json:"url"`
	Status         string            `json:"status"`
	Labels         map[string]string `json:"labels,omitempty"`
	Latency        time.Duration     `json:"-"`
	ResponseTimeMs int64             `json:"response_time_ms"`
	CheckedAt      time.Time         `json:"checked_at"`
	Error          error             `json:"-"`
	ErrorStr       *string           `json:"error,omitempty"`
	RawResponse    []byte            `json:"-"`
	StatusCode     int               `json:"-"`
	// Stale is true if the entry has not been updated within the staleness threshold.
	// Uses omitempty so fresh entries (Stale: false) omit the field from JSON output,
	// preserving backwards compatibility with existing API consumers.
	Stale bool `json:"stale,omitempty"`
}
