package store

import (
	"sync"
	"time"

	"github.com/jpalmerr/pulseboard/internal/types"
)

// MemoryStore is an in-memory implementation of [Store].
//
// MemoryStore provides thread-safe storage with a publish-subscribe mechanism
// for real-time updates. Status results are keyed by endpoint name, with new
// results replacing previous values.
//
// Subscribers receive updates via buffered channels (buffer size 100). Updates
// are sent non-blocking; if a subscriber's buffer is full, the update is dropped
// for that subscriber to prevent blocking the entire system.
type MemoryStore struct {
	mu          sync.RWMutex
	statuses    map[string]types.StatusResult
	subscribers map[chan types.StatusResult]struct{}
	subMu       sync.RWMutex
}

// NewMemoryStore creates a new in-memory [Store] implementation.
//
// The store is immediately ready for use. No cleanup is required when done.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		statuses:    make(map[string]types.StatusResult),
		subscribers: make(map[chan types.StatusResult]struct{}),
	}
}

// Update stores a [types.StatusResult] and notifies all subscribers.
//
// The result is stored using its EndpointName as the key. Subsequent updates with
// the same name replace the previous value. All subscribers receive the
// update (unless their buffer is full).
func (m *MemoryStore) Update(result types.StatusResult) {
	m.mu.Lock()
	m.statuses[result.EndpointName] = result
	m.mu.Unlock()

	m.notifySubscribers(result)
}

// GetAll returns a snapshot of all currently stored status results.
//
// The returned slice is a copy; modifications do not affect the store.
// Order is not guaranteed.
func (m *MemoryStore) GetAll() []types.StatusResult {
	m.mu.RLock()
	defer m.mu.RUnlock()

	results := make([]types.StatusResult, 0, len(m.statuses))
	for _, status := range m.statuses {
		results = append(results, status)
	}
	return results
}

// Subscribe creates a new subscription and returns a channel for receiving updates.
//
// The returned channel has a buffer of 100 messages. If the buffer fills
// (slow consumer), new updates are dropped for this subscriber.
//
// Caller must call [MemoryStore.Unsubscribe] when done to prevent resource leaks.
func (m *MemoryStore) Subscribe() <-chan types.StatusResult {
	ch := make(chan types.StatusResult, 100)

	m.subMu.Lock()
	m.subscribers[ch] = struct{}{}
	m.subMu.Unlock()

	return ch
}

// Unsubscribe removes a subscription and closes its channel.
//
// After calling Unsubscribe, the channel will be closed and no further
// updates will be sent. Safe to call multiple times or with an unknown channel.
func (m *MemoryStore) Unsubscribe(ch <-chan types.StatusResult) {
	m.subMu.Lock()
	defer m.subMu.Unlock()

	// find and delete the channel (need to convert to the right type)
	for subCh := range m.subscribers {
		if subCh == ch {
			delete(m.subscribers, subCh)
			close(subCh)
			break
		}
	}
}

// notifySubscribers sends the result to all active subscribers.
//
// This is non-blocking: if a subscriber's channel buffer is full, the message
// is dropped for that subscriber rather than blocking the update path.
func (m *MemoryStore) notifySubscribers(result types.StatusResult) {
	m.subMu.RLock()
	defer m.subMu.RUnlock()

	for ch := range m.subscribers {
		select {
		case ch <- result:
		default:
			// subscriber is slow, drop the message
		}
	}
}

// MarkStale marks entries whose CheckedAt is older than threshold as stale.
//
// An entry is considered stale if its CheckedAt is before (now - threshold).
// Stale entries have their Stale field set to true and subscribers are notified.
// Already-stale entries are not re-processed — this method is idempotent: calling
// it repeatedly does not re-notify subscribers for entries already marked stale.
// If threshold is zero or negative, this is a no-op and returns 0 without acquiring
// any lock.
//
// Returns the number of entries newly marked stale in this call.
//
// Safe for concurrent access.
func (m *MemoryStore) MarkStale(threshold time.Duration) int {
	if threshold <= 0 {
		return 0
	}

	now := time.Now()
	cutoff := now.Add(-threshold)
	var staleCount int

	m.mu.Lock()
	defer m.mu.Unlock()

	for name, status := range m.statuses {
		if !status.Stale && status.CheckedAt.Before(cutoff) {
			status.Stale = true
			m.statuses[name] = status
			staleCount++
			m.notifySubscribers(status)
		}
	}

	return staleCount
}
