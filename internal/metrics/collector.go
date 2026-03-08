package metrics

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultBuckets are the default histogram bucket boundaries in seconds.
// Covers typical HTTP health-check latencies from 5ms to 10s.
var DefaultBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0}

// allStatuses is the fixed set of statuses shown in the endpoint status gauge.
var allStatuses = []string{"up", "down", "degraded", "unknown", "stale"}

// Collector gathers PulseBoard metrics for Prometheus exposition.
//
// All methods are safe for concurrent use. Per-endpoint maps are fully
// initialised at construction and never modified after that; only atomic
// values within them are updated at runtime.
type Collector struct {
	endpoints []string  // ordered for deterministic output, immutable after New
	buckets   []float64 // histogram upper bounds, immutable after New

	// per-endpoint atomics — map keys are fixed at construction
	pollTotal    map[string]*atomic.Int64    // name -> total polls
	errorTotal   map[string]*atomic.Int64    // name -> polls with error
	bucketCounts map[string][]*atomic.Int64  // name -> [len(buckets)+1] (last = +Inf)
	latencySum   map[string]*atomic.Int64    // name -> sum in microseconds
	latencyCount map[string]*atomic.Int64    // name -> total observations

	// current status per endpoint (atomic.Value storing string)
	currentStatus sync.Map // string(name) -> string(status)

	// transition counters — dynamic keys: "name\x00from\x00to" -> *atomic.Int64
	statusChanges sync.Map
}

// NewCollector creates a Collector initialised for the given endpoint names.
// Pass nil buckets to use DefaultBuckets. The slice is copied internally.
func NewCollector(endpointNames []string, buckets []float64) *Collector {
	if buckets == nil {
		buckets = DefaultBuckets
	}
	b := make([]float64, len(buckets))
	copy(b, buckets)

	names := make([]string, len(endpointNames))
	copy(names, endpointNames)

	c := &Collector{
		endpoints:    names,
		buckets:      b,
		pollTotal:    make(map[string]*atomic.Int64, len(names)),
		errorTotal:   make(map[string]*atomic.Int64, len(names)),
		bucketCounts: make(map[string][]*atomic.Int64, len(names)),
		latencySum:   make(map[string]*atomic.Int64, len(names)),
		latencyCount: make(map[string]*atomic.Int64, len(names)),
	}

	for _, name := range names {
		c.pollTotal[name] = new(atomic.Int64)
		c.errorTotal[name] = new(atomic.Int64)
		c.latencySum[name] = new(atomic.Int64)
		c.latencyCount[name] = new(atomic.Int64)

		bucketSlice := make([]*atomic.Int64, len(b)+1) // +1 for +Inf
		for i := range bucketSlice {
			bucketSlice[i] = new(atomic.Int64)
		}
		c.bucketCounts[name] = bucketSlice
	}

	return c
}

// RecordPoll records the outcome of a single poll.
// hasError should be true when result.Error != nil.
// status is the string form of the resulting Status (e.g. "up", "down").
func (c *Collector) RecordPoll(name string, latency time.Duration, status string, hasError bool) {
	if _, ok := c.pollTotal[name]; !ok {
		return
	}

	c.pollTotal[name].Add(1)
	if hasError {
		c.errorTotal[name].Add(1)
	}

	// histogram: increment all buckets whose upper bound >= latency
	secs := latency.Seconds()
	buckets := c.bucketCounts[name]
	for i, bound := range c.buckets {
		if secs <= bound {
			buckets[i].Add(1)
		}
	}
	// always increment +Inf bucket
	buckets[len(c.buckets)].Add(1)

	// store latency sum in microseconds to avoid float64 atomic complexity
	c.latencySum[name].Add(latency.Microseconds())
	c.latencyCount[name].Add(1)

	c.currentStatus.Store(name, status)
}

// RecordStatusChange records a status transition.
// from may be empty ("") on the first poll (transition from unknown).
func (c *Collector) RecordStatusChange(name, from, to string) {
	key := name + "\x00" + from + "\x00" + to
	val, _ := c.statusChanges.LoadOrStore(key, new(atomic.Int64))
	val.(*atomic.Int64).Add(1)
}

// WritePrometheus writes all metrics to w in Prometheus text exposition format 0.0.4.
func (c *Collector) WritePrometheus(w io.Writer) error {
	// pulseboard_info
	if err := fprintln(w, "# HELP pulseboard_info PulseBoard build information"); err != nil {
		return err
	}
	if err := fprintln(w, "# TYPE pulseboard_info gauge"); err != nil {
		return err
	}
	if err := fprintln(w, "pulseboard_info{version=\"dev\"} 1"); err != nil {
		return err
	}
	if err := fprintln(w, ""); err != nil {
		return err
	}

	// pulseboard_endpoint_status
	if err := fprintln(w, "# HELP pulseboard_endpoint_status Current status of each endpoint (1=current, 0=not)"); err != nil {
		return err
	}
	if err := fprintln(w, "# TYPE pulseboard_endpoint_status gauge"); err != nil {
		return err
	}
	for _, name := range c.endpoints {
		current := ""
		if v, ok := c.currentStatus.Load(name); ok {
			current = v.(string)
		}
		for _, st := range allStatuses {
			val := 0
			if st == current {
				val = 1
			}
			if _, err := fmt.Fprintf(w, "pulseboard_endpoint_status{name=%q,status=%q} %d\n", name, st, val); err != nil {
				return err
			}
		}
	}
	if err := fprintln(w, ""); err != nil {
		return err
	}

	// pulseboard_polls_total
	if err := fprintln(w, "# HELP pulseboard_polls_total Total number of polls performed"); err != nil {
		return err
	}
	if err := fprintln(w, "# TYPE pulseboard_polls_total counter"); err != nil {
		return err
	}
	for _, name := range c.endpoints {
		total := c.pollTotal[name].Load()
		errors := c.errorTotal[name].Load()
		success := total - errors
		if _, err := fmt.Fprintf(w, "pulseboard_polls_total{name=%q,result=\"success\"} %d\n", name, success); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "pulseboard_polls_total{name=%q,result=\"error\"} %d\n", name, errors); err != nil {
			return err
		}
	}
	if err := fprintln(w, ""); err != nil {
		return err
	}

	// pulseboard_poll_duration_seconds histogram
	if err := fprintln(w, "# HELP pulseboard_poll_duration_seconds Duration of health check polls"); err != nil {
		return err
	}
	if err := fprintln(w, "# TYPE pulseboard_poll_duration_seconds histogram"); err != nil {
		return err
	}
	for _, name := range c.endpoints {
		buckets := c.bucketCounts[name]
		for i, bound := range c.buckets {
			if _, err := fmt.Fprintf(w, "pulseboard_poll_duration_seconds_bucket{name=%q,le=\"%g\"} %d\n", name, bound, buckets[i].Load()); err != nil {
				return err
			}
		}
		infCount := buckets[len(c.buckets)].Load()
		if _, err := fmt.Fprintf(w, "pulseboard_poll_duration_seconds_bucket{name=%q,le=\"+Inf\"} %d\n", name, infCount); err != nil {
			return err
		}
		sumMicros := c.latencySum[name].Load()
		count := c.latencyCount[name].Load()
		if _, err := fmt.Fprintf(w, "pulseboard_poll_duration_seconds_sum{name=%q} %g\n", name, float64(sumMicros)/1e6); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "pulseboard_poll_duration_seconds_count{name=%q} %d\n", name, count); err != nil {
			return err
		}
	}
	if err := fprintln(w, ""); err != nil {
		return err
	}

	// pulseboard_status_changes_total
	if err := fprintln(w, "# HELP pulseboard_status_changes_total Total number of status transitions"); err != nil {
		return err
	}
	if err := fprintln(w, "# TYPE pulseboard_status_changes_total counter"); err != nil {
		return err
	}
	var writeErr error
	c.statusChanges.Range(func(k, v any) bool {
		key := k.(string)
		count := v.(*atomic.Int64).Load()
		name, from, to, ok := splitKey(key)
		if !ok {
			return true
		}
		_, writeErr = fmt.Fprintf(w, "pulseboard_status_changes_total{name=%q,from=%q,to=%q} %d\n", name, from, to, count)
		return writeErr == nil
	})
	return writeErr
}

// fprintln writes s followed by a newline to w.
func fprintln(w io.Writer, s string) error {
	_, err := fmt.Fprintln(w, s)
	return err
}

// splitKey splits a key of the form "name\x00from\x00to" into three parts.
func splitKey(key string) (name, from, to string, ok bool) {
	name, rest, found := strings.Cut(key, "\x00")
	if !found {
		return "", "", "", false
	}
	from, to, found = strings.Cut(rest, "\x00")
	if !found {
		return "", "", "", false
	}
	return name, from, to, true
}
