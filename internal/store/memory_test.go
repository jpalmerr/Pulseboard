package store

import (
	"sync"
	"testing"
	"time"

	"github.com/jpalmerr/pulseboard/internal/types"
)

func TestNewMemoryStore(t *testing.T) {
	store := NewMemoryStore()
	if store == nil {
		t.Fatal("NewMemoryStore() = nil")
	}

	// should start empty
	if len(store.GetAll()) != 0 {
		t.Errorf("GetAll() = %v items, want 0", len(store.GetAll()))
	}
}

func TestMemoryStore_Update(t *testing.T) {
	store := NewMemoryStore()

	result := types.StatusResult{
		EndpointName:   "Test API",
		URL:            "https://example.com",
		Status:         "up",
		Labels:         map[string]string{"env": "prod"},
		ResponseTimeMs: 100,
		CheckedAt:      time.Now(),
	}

	store.Update(result)

	all := store.GetAll()
	if len(all) != 1 {
		t.Fatalf("GetAll() = %v items, want 1", len(all))
	}

	if all[0].EndpointName != "Test API" {
		t.Errorf("GetAll()[0].EndpointName = %v, want %v", all[0].EndpointName, "Test API")
	}
	if all[0].Status != "up" {
		t.Errorf("GetAll()[0].Status = %v, want %v", all[0].Status, "up")
	}
}

func TestMemoryStore_UpdateOverwrites(t *testing.T) {
	store := NewMemoryStore()

	// first update
	store.Update(types.StatusResult{
		EndpointName: "Test API",
		Status:       "up",
	})

	// second update with same name should overwrite
	store.Update(types.StatusResult{
		EndpointName: "Test API",
		Status:       "down",
	})

	all := store.GetAll()
	if len(all) != 1 {
		t.Fatalf("GetAll() = %v items, want 1", len(all))
	}

	if all[0].Status != "down" {
		t.Errorf("GetAll()[0].Status = %v, want %v", all[0].Status, "down")
	}
}

func TestMemoryStore_MultipleEndpoints(t *testing.T) {
	store := NewMemoryStore()

	store.Update(types.StatusResult{EndpointName: "API 1", Status: "up"})
	store.Update(types.StatusResult{EndpointName: "API 2", Status: "down"})
	store.Update(types.StatusResult{EndpointName: "API 3", Status: "degraded"})

	all := store.GetAll()
	if len(all) != 3 {
		t.Errorf("GetAll() = %v items, want 3", len(all))
	}
}

func TestMemoryStore_Subscribe(t *testing.T) {
	store := NewMemoryStore()

	ch := store.Subscribe()
	if ch == nil {
		t.Fatal("Subscribe() = nil")
	}

	// update should send to subscriber
	go func() {
		store.Update(types.StatusResult{EndpointName: "Test", Status: "up"})
	}()

	select {
	case result := <-ch:
		if result.EndpointName != "Test" {
			t.Errorf("received EndpointName = %v, want %v", result.EndpointName, "Test")
		}
	case <-time.After(1 * time.Second):
		t.Error("Subscribe() channel did not receive update")
	}
}

func TestMemoryStore_MultipleSubscribers(t *testing.T) {
	store := NewMemoryStore()

	ch1 := store.Subscribe()
	ch2 := store.Subscribe()
	ch3 := store.Subscribe()

	// update should fanout to all subscribers
	go func() {
		store.Update(types.StatusResult{EndpointName: "Test", Status: "up"})
	}()

	received := 0
	timeout := time.After(1 * time.Second)

	for received < 3 {
		select {
		case <-ch1:
			received++
		case <-ch2:
			received++
		case <-ch3:
			received++
		case <-timeout:
			t.Fatalf("Only received %d/3 updates", received)
		}
	}
}

func TestMemoryStore_Unsubscribe(t *testing.T) {
	store := NewMemoryStore()

	ch := store.Subscribe()
	store.Unsubscribe(ch)

	// channel should be closed
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("Unsubscribe() channel should be closed")
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Unsubscribe() channel should be closed immediately")
	}
}

func TestMemoryStore_UnsubscribeStopsDelivery(t *testing.T) {
	store := NewMemoryStore()

	ch1 := store.Subscribe()
	ch2 := store.Subscribe()

	// unsubscribe ch1
	store.Unsubscribe(ch1)

	// update should only go to ch2
	go func() {
		store.Update(types.StatusResult{EndpointName: "Test", Status: "up"})
	}()

	select {
	case <-ch2:
		// expected
	case <-time.After(1 * time.Second):
		t.Error("ch2 should still receive updates")
	}
}

func TestMemoryStore_SlowSubscriberDoesNotBlock(t *testing.T) {
	store := NewMemoryStore()

	// create a subscriber but don't read from it
	_ = store.Subscribe()

	// create another subscriber that reads
	ch2 := store.Subscribe()

	done := make(chan bool)

	go func() {
		// this should not block even though ch1 is not being read
		for i := 0; i < 200; i++ {
			store.Update(types.StatusResult{EndpointName: "Test", Status: "up"})
		}
		done <- true
	}()

	// drain ch2
	go func() {
		for range ch2 {
		}
	}()

	select {
	case <-done:
		// expected - updates completed without blocking
	case <-time.After(2 * time.Second):
		t.Error("Update() blocked on slow subscriber")
	}
}

func TestMemoryStore_ConcurrentAccess(t *testing.T) {
	store := NewMemoryStore()

	var wg sync.WaitGroup
	numGoroutines := 10
	numUpdates := 100

	// concurrent updates
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numUpdates; j++ {
				store.Update(types.StatusResult{
					EndpointName: "API",
					Status:       "up",
				})
			}
		}(i)
	}

	// concurrent reads
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numUpdates; j++ {
				_ = store.GetAll()
			}
		}()
	}

	// concurrent subscribe/unsubscribe
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch := store.Subscribe()
			time.Sleep(10 * time.Millisecond)
			store.Unsubscribe(ch)
		}()
	}

	wg.Wait()
}

func TestMemoryStore_GetAllReturnsLatest(t *testing.T) {
	store := NewMemoryStore()

	// update same endpoint multiple times
	store.Update(types.StatusResult{EndpointName: "API", Status: "up", ResponseTimeMs: 100})
	store.Update(types.StatusResult{EndpointName: "API", Status: "degraded", ResponseTimeMs: 200})
	store.Update(types.StatusResult{EndpointName: "API", Status: "down", ResponseTimeMs: 300})

	all := store.GetAll()
	if len(all) != 1 {
		t.Fatalf("GetAll() = %v items, want 1", len(all))
	}

	if all[0].Status != "down" {
		t.Errorf("GetAll()[0].Status = %v, want %v", all[0].Status, "down")
	}
	if all[0].ResponseTimeMs != 300 {
		t.Errorf("GetAll()[0].ResponseTimeMs = %v, want %v", all[0].ResponseTimeMs, 300)
	}
}

func TestMarkStale_marksOldEntriesStale(t *testing.T) {
	s := NewMemoryStore()

	threshold := 1 * time.Minute
	old := time.Now().Add(-2 * time.Minute) // beyond threshold
	fresh := time.Now()                     // within threshold

	s.Update(types.StatusResult{EndpointName: "old-api", Status: "up", CheckedAt: old})
	s.Update(types.StatusResult{EndpointName: "fresh-api", Status: "up", CheckedAt: fresh})

	count := s.MarkStale(threshold)
	if count != 1 {
		t.Errorf("MarkStale() = %d, want 1", count)
	}

	for _, result := range s.GetAll() {
		switch result.EndpointName {
		case "old-api":
			if !result.Stale {
				t.Errorf("MarkStale() old-api Stale = false, want true")
			}
		case "fresh-api":
			if result.Stale {
				t.Errorf("MarkStale() fresh-api Stale = true, want false")
			}
		}
	}
}

func TestMarkStale_leavesRecentEntriesAlone(t *testing.T) {
	s := NewMemoryStore()

	threshold := 5 * time.Minute
	// all entries are within threshold
	s.Update(types.StatusResult{EndpointName: "api-1", Status: "up", CheckedAt: time.Now().Add(-1 * time.Minute)})
	s.Update(types.StatusResult{EndpointName: "api-2", Status: "up", CheckedAt: time.Now().Add(-2 * time.Minute)})
	s.Update(types.StatusResult{EndpointName: "api-3", Status: "up", CheckedAt: time.Now().Add(-4 * time.Minute)})

	count := s.MarkStale(threshold)
	if count != 0 {
		t.Errorf("MarkStale() = %d, want 0 (all entries are fresh)", count)
	}

	for _, result := range s.GetAll() {
		if result.Stale {
			t.Errorf("MarkStale() %s Stale = true, want false (entry is within threshold)", result.EndpointName)
		}
	}
}

func TestMarkStale_notifiesSubscribersForNewlyStale(t *testing.T) {
	s := NewMemoryStore()

	old := time.Now().Add(-10 * time.Minute)
	s.Update(types.StatusResult{EndpointName: "stale-api", Status: "up", CheckedAt: old})

	ch := s.Subscribe()
	defer s.Unsubscribe(ch)

	count := s.MarkStale(1 * time.Minute)
	if count != 1 {
		t.Fatalf("MarkStale() = %d, want 1", count)
	}

	select {
	case result := <-ch:
		if result.EndpointName != "stale-api" {
			t.Errorf("subscriber received EndpointName = %q, want %q", result.EndpointName, "stale-api")
		}
		if !result.Stale {
			t.Errorf("subscriber received Stale = false, want true")
		}
	case <-time.After(1 * time.Second):
		t.Error("subscriber did not receive notification for newly stale entry")
	}
}

func TestMarkStale_idempotent(t *testing.T) {
	s := NewMemoryStore()

	old := time.Now().Add(-10 * time.Minute)
	s.Update(types.StatusResult{EndpointName: "stale-api", Status: "up", CheckedAt: old})

	// first call: marks entry stale
	firstCount := s.MarkStale(1 * time.Minute)
	if firstCount != 1 {
		t.Fatalf("first MarkStale() = %d, want 1", firstCount)
	}

	ch := s.Subscribe()
	defer s.Unsubscribe(ch)

	// second call: already stale, should not re-mark or re-notify
	secondCount := s.MarkStale(1 * time.Minute)
	if secondCount != 0 {
		t.Errorf("second MarkStale() = %d, want 0 (entry already stale)", secondCount)
	}

	select {
	case result := <-ch:
		t.Errorf("subscriber received unexpected notification for already-stale entry: %v", result.EndpointName)
	case <-time.After(100 * time.Millisecond):
		// expected: no notification
	}
}

func TestMarkStale_returnsCorrectCount(t *testing.T) {
	s := NewMemoryStore()

	old := time.Now().Add(-10 * time.Minute)
	s.Update(types.StatusResult{EndpointName: "api-1", Status: "up", CheckedAt: old})
	s.Update(types.StatusResult{EndpointName: "api-2", Status: "up", CheckedAt: old})
	s.Update(types.StatusResult{EndpointName: "api-3", Status: "up", CheckedAt: old})

	count := s.MarkStale(1 * time.Minute)
	if count != 3 {
		t.Errorf("MarkStale() = %d, want 3", count)
	}
}

func TestMarkStale_zeroThresholdIsNoop(t *testing.T) {
	s := NewMemoryStore()

	old := time.Now().Add(-10 * time.Minute)
	s.Update(types.StatusResult{EndpointName: "api", Status: "up", CheckedAt: old})

	count := s.MarkStale(0)
	if count != 0 {
		t.Errorf("MarkStale(0) = %d, want 0", count)
	}

	all := s.GetAll()
	if len(all) != 1 || all[0].Stale {
		t.Errorf("MarkStale(0) modified entry: Stale = %v, want false", all[0].Stale)
	}
}

func TestMarkStale_negativeThresholdIsNoop(t *testing.T) {
	s := NewMemoryStore()

	old := time.Now().Add(-10 * time.Minute)
	s.Update(types.StatusResult{EndpointName: "api", Status: "up", CheckedAt: old})

	count := s.MarkStale(-1 * time.Second)
	if count != 0 {
		t.Errorf("MarkStale(-1s) = %d, want 0", count)
	}

	all := s.GetAll()
	if len(all) != 1 || all[0].Stale {
		t.Errorf("MarkStale(-1s) modified entry: Stale = %v, want false", all[0].Stale)
	}
}

func TestUpdate_clearsStaleFlagOnFreshResult(t *testing.T) {
	s := NewMemoryStore()

	old := time.Now().Add(-10 * time.Minute)
	s.Update(types.StatusResult{EndpointName: "api", Status: "up", CheckedAt: old})

	// mark it stale
	count := s.MarkStale(1 * time.Minute)
	if count != 1 {
		t.Fatalf("MarkStale() = %d, want 1", count)
	}

	// fresh update for the same endpoint with Stale explicitly false
	s.Update(types.StatusResult{
		EndpointName: "api",
		Status:       "up",
		CheckedAt:    time.Now(),
		Stale:        false,
	})

	all := s.GetAll()
	if len(all) != 1 {
		t.Fatalf("GetAll() = %d items, want 1", len(all))
	}
	if all[0].Stale {
		t.Errorf("Update() did not clear Stale flag: Stale = true, want false")
	}
}
