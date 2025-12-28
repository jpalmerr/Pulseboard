package main

import (
	"encoding/json"
	"log/slog"
	"math/rand"
	"net/http"
	"sync"
	"time"
)

// mockState tracks status and next change time for a single endpoint.
type mockState struct {
	statusIdx    int
	nextChangeAt time.Time
}

// StartMockHealthServer runs a mock health endpoint that cycles through statuses.
// Each endpoint changes status every 20-60 seconds.
// Call this in a goroutine before creating PulseBoard endpoints.
func StartMockHealthServer(addr string) {
	var (
		states = make(map[string]*mockState)
		mu     sync.Mutex
	)
	statuses := []string{"ok", "degraded", "down"}

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		svc := r.URL.Query().Get("svc")
		env := r.URL.Query().Get("env")
		key := svc + "-" + env

		// simulate small latency variance
		time.Sleep(time.Duration(50+rand.Intn(150)) * time.Millisecond)

		mu.Lock()
		state, exists := states[key]
		if !exists {
			// first change in 20-60 seconds
			state = &mockState{
				statusIdx:    0,
				nextChangeAt: time.Now().Add(time.Duration(20+rand.Intn(41)) * time.Second),
			}
			states[key] = state
		}

		// change status when scheduled time is reached
		if time.Now().After(state.nextChangeAt) {
			oldStatus := statuses[state.statusIdx]
			state.statusIdx = (state.statusIdx + 1) % len(statuses)
			// schedule next change in 20-60 seconds
			state.nextChangeAt = time.Now().Add(time.Duration(20+rand.Intn(41)) * time.Second)
			slog.Info("status change", "endpoint", key, "from", oldStatus, "to", statuses[state.statusIdx])
		}
		status := statuses[state.statusIdx]
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		resp := map[string]string{
			"svc":    svc,
			"env":    env,
			"status": status,
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			slog.Error("failed to write response", "error", err)
		}
	})

	if err := http.ListenAndServe(addr, nil); err != nil {
		slog.Error("mock server error", "error", err)
	}
}
