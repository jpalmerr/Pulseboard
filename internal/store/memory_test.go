package store

import (
	"sync"
	"testing"
	"time"
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

	result := StatusResult{
		Name:           "Test API",
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

	if all[0].Name != "Test API" {
		t.Errorf("GetAll()[0].Name = %v, want %v", all[0].Name, "Test API")
	}
	if all[0].Status != "up" {
		t.Errorf("GetAll()[0].Status = %v, want %v", all[0].Status, "up")
	}
}

func TestMemoryStore_UpdateOverwrites(t *testing.T) {
	store := NewMemoryStore()

	// first update
	store.Update(StatusResult{
		Name:   "Test API",
		Status: "up",
	})

	// second update with same name should overwrite
	store.Update(StatusResult{
		Name:   "Test API",
		Status: "down",
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

	store.Update(StatusResult{Name: "API 1", Status: "up"})
	store.Update(StatusResult{Name: "API 2", Status: "down"})
	store.Update(StatusResult{Name: "API 3", Status: "degraded"})

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
		store.Update(StatusResult{Name: "Test", Status: "up"})
	}()

	select {
	case result := <-ch:
		if result.Name != "Test" {
			t.Errorf("received Name = %v, want %v", result.Name, "Test")
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
		store.Update(StatusResult{Name: "Test", Status: "up"})
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
		store.Update(StatusResult{Name: "Test", Status: "up"})
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
			store.Update(StatusResult{Name: "Test", Status: "up"})
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
				store.Update(StatusResult{
					Name:   "API",
					Status: "up",
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
	store.Update(StatusResult{Name: "API", Status: "up", ResponseTimeMs: 100})
	store.Update(StatusResult{Name: "API", Status: "degraded", ResponseTimeMs: 200})
	store.Update(StatusResult{Name: "API", Status: "down", ResponseTimeMs: 300})

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
