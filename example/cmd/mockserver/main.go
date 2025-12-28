// Standalone mock server for testing the CLI.
//
// Usage:
//
//	go run ./example/cmd/mockserver
//
// Then in another terminal:
//
//	go run ./cmd/pulseboard serve -c example/config.yaml
package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"sync"
	"time"
)

func main() {
	fmt.Println("Mock health server starting on :9999")
	fmt.Println("Endpoints cycle through: ok → degraded → down")
	fmt.Println("Press Ctrl+C to stop")
	fmt.Println()

	var (
		states   = make(map[string]*mockState)
		mu       sync.Mutex
		statuses = []string{"ok", "degraded", "down"}
	)

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		svc := r.URL.Query().Get("svc")
		env := r.URL.Query().Get("env")
		key := svc + "-" + env

		time.Sleep(time.Duration(50+rand.Intn(150)) * time.Millisecond)

		mu.Lock()
		state, exists := states[key]
		if !exists {
			state = &mockState{
				statusIdx:    0,
				nextChangeAt: time.Now().Add(time.Duration(20+rand.Intn(41)) * time.Second),
			}
			states[key] = state
		}

		if time.Now().After(state.nextChangeAt) {
			oldStatus := statuses[state.statusIdx]
			state.statusIdx = (state.statusIdx + 1) % len(statuses)
			state.nextChangeAt = time.Now().Add(time.Duration(20+rand.Intn(41)) * time.Second)
			slog.Info("status change", "endpoint", key, "from", oldStatus, "to", statuses[state.statusIdx])
		}
		status := statuses[state.statusIdx]
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"svc":    svc,
			"env":    env,
			"status": status,
		})
	})

	if err := http.ListenAndServe(":9999", nil); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}

type mockState struct {
	statusIdx    int
	nextChangeAt time.Time
}
