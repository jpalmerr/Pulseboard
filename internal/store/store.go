package store

import (
	"time"

	"github.com/jpalmerr/pulseboard/internal/types"
)

// Store defines the interface for storing and subscribing to status updates.
//
// Store implementations must be safe for concurrent access. The pub/sub
// mechanism allows real-time updates to be pushed to connected clients
// (e.g., via Server-Sent Events).
type Store interface {
	// Update stores a new status result and notifies all subscribers.
	// The result is keyed by EndpointName, so subsequent updates replace previous values.
	Update(result types.StatusResult)

	// GetAll returns all currently stored status results.
	// The returned slice is a snapshot; modifications do not affect the store.
	GetAll() []types.StatusResult

	// Subscribe returns a channel that receives status updates.
	// The returned channel has a buffer; slow consumers may miss updates.
	// Caller must call Unsubscribe when done to prevent resource leaks.
	Subscribe() <-chan types.StatusResult

	// Unsubscribe removes a subscription and closes the channel.
	// Safe to call with a channel that was already unsubscribed.
	Unsubscribe(ch <-chan types.StatusResult)

	// MarkStale marks entries whose CheckedAt is older than threshold as stale.
	// An entry is stale if its CheckedAt is before (now - threshold).
	// Returns the number of entries newly marked stale in this call.
	// Already-stale entries are not re-marked and do not trigger subscriber notifications (idempotent).
	// If threshold is zero or negative, this is a no-op and returns 0.
	MarkStale(threshold time.Duration) int
}
