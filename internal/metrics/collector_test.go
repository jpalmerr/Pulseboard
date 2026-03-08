package metrics

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// --- Construction ---

func TestNewCollector_ZeroCounters(t *testing.T) {
	c := NewCollector([]string{"api", "db"}, nil)

	var buf strings.Builder
	if err := c.WritePrometheus(&buf); err != nil {
		t.Fatalf("WritePrometheus() error: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, `pulseboard_polls_total{name="api",result="success"} 0`) {
		t.Errorf("expected api success counter = 0, output:\n%s", out)
	}
	if !strings.Contains(out, `pulseboard_polls_total{name="api",result="error"} 0`) {
		t.Errorf("expected api error counter = 0, output:\n%s", out)
	}
	if !strings.Contains(out, `pulseboard_poll_duration_seconds_count{name="api"} 0`) {
		t.Errorf("expected latency count = 0, output:\n%s", out)
	}
}

// --- RecordPoll ---

func TestRecordPoll_PollTotalIncrements(t *testing.T) {
	c := NewCollector([]string{"api"}, nil)

	c.RecordPoll("api", 10*time.Millisecond, "up", false)
	c.RecordPoll("api", 10*time.Millisecond, "up", false)

	if got := c.pollTotal["api"].Load(); got != 2 {
		t.Errorf("pollTotal = %d, want 2", got)
	}
}

func TestRecordPoll_ErrorTotalOnlyWhenHasError(t *testing.T) {
	c := NewCollector([]string{"api"}, nil)

	c.RecordPoll("api", 10*time.Millisecond, "up", false)
	c.RecordPoll("api", 10*time.Millisecond, "down", true)

	if got := c.errorTotal["api"].Load(); got != 1 {
		t.Errorf("errorTotal = %d, want 1", got)
	}
}

func TestRecordPoll_BucketCountsForKnownLatency(t *testing.T) {
	// 75ms should be <= 0.1 but not <= 0.05
	c := NewCollector([]string{"api"}, DefaultBuckets)
	c.RecordPoll("api", 75*time.Millisecond, "up", false)

	buckets := c.bucketCounts["api"]
	idx05 := -1
	idx10 := -1
	for i, b := range DefaultBuckets {
		if b == 0.05 {
			idx05 = i
		}
		if b == 0.1 {
			idx10 = i
		}
	}
	if idx05 < 0 || idx10 < 0 {
		t.Fatal("expected 0.05 and 0.1 in DefaultBuckets")
	}

	if got := buckets[idx05].Load(); got != 0 {
		t.Errorf("bucket[0.05] = %d, want 0 for 75ms latency", got)
	}
	if got := buckets[idx10].Load(); got != 1 {
		t.Errorf("bucket[0.1] = %d, want 1 for 75ms latency", got)
	}
}

func TestRecordPoll_InfBucketEqualsLatencyCount(t *testing.T) {
	c := NewCollector([]string{"api"}, DefaultBuckets)

	for i := range 5 {
		c.RecordPoll("api", time.Duration(i+1)*100*time.Millisecond, "up", false)
	}

	infBucket := c.bucketCounts["api"][len(DefaultBuckets)].Load()
	latencyCount := c.latencyCount["api"].Load()

	if infBucket != latencyCount {
		t.Errorf("+Inf bucket = %d, latencyCount = %d, want equal", infBucket, latencyCount)
	}
}

func TestRecordPoll_BucketCountsNonDecreasing(t *testing.T) {
	// cumulative property: bucket[i] >= bucket[i-1] for a single observation
	c := NewCollector([]string{"api"}, DefaultBuckets)
	c.RecordPoll("api", 75*time.Millisecond, "up", false)

	buckets := c.bucketCounts["api"]
	for i := 1; i < len(buckets); i++ {
		prev := buckets[i-1].Load()
		curr := buckets[i].Load()
		if curr < prev {
			t.Errorf("bucket[%d]=%d < bucket[%d]=%d: not non-decreasing", i, curr, i-1, prev)
		}
	}
}

func TestRecordPoll_LatencySumAndCountIncrement(t *testing.T) {
	c := NewCollector([]string{"api"}, nil)

	c.RecordPoll("api", 100*time.Millisecond, "up", false)
	c.RecordPoll("api", 200*time.Millisecond, "up", false)

	wantCount := int64(2)
	wantSumMicros := int64(300_000) // 100ms + 200ms in microseconds

	if got := c.latencyCount["api"].Load(); got != wantCount {
		t.Errorf("latencyCount = %d, want %d", got, wantCount)
	}
	if got := c.latencySum["api"].Load(); got != wantSumMicros {
		t.Errorf("latencySum = %d µs, want %d µs", got, wantSumMicros)
	}
}

func TestRecordPoll_CurrentStatusReflectsLatest(t *testing.T) {
	c := NewCollector([]string{"api"}, nil)

	c.RecordPoll("api", 10*time.Millisecond, "up", false)
	v, ok := c.currentStatus.Load("api")
	if !ok || v.(string) != "up" {
		t.Errorf("currentStatus = %v, want \"up\"", v)
	}

	c.RecordPoll("api", 10*time.Millisecond, "down", false)
	v, ok = c.currentStatus.Load("api")
	if !ok || v.(string) != "down" {
		t.Errorf("currentStatus = %v, want \"down\"", v)
	}
}

func TestRecordPoll_UnknownEndpointNoPanic(t *testing.T) {
	c := NewCollector([]string{"api"}, nil)
	// must not panic; should be a no-op
	c.RecordPoll("does-not-exist", 10*time.Millisecond, "up", false)
	if got := c.pollTotal["api"].Load(); got != 0 {
		t.Errorf("known endpoint modified by unknown poll: pollTotal = %d", got)
	}
}

// --- RecordStatusChange ---

func TestRecordStatusChange_CreatesAndIncrements(t *testing.T) {
	c := NewCollector([]string{"api"}, nil)
	c.RecordStatusChange("api", "up", "down")
	c.RecordStatusChange("api", "up", "down")

	key := "api\x00up\x00down"
	v, ok := c.statusChanges.Load(key)
	if !ok {
		t.Fatal("expected key to exist in statusChanges")
	}
	if got := v.(*atomic.Int64).Load(); got != 2 {
		t.Errorf("statusChanges[up->down] = %d, want 2", got)
	}
}

func TestRecordStatusChange_EmptyFromNoPanic(t *testing.T) {
	c := NewCollector([]string{"api"}, nil)
	// first poll case: from = ""
	c.RecordStatusChange("api", "", "up")

	key := "api\x00\x00up"
	v, ok := c.statusChanges.Load(key)
	if !ok {
		t.Fatal("expected key to exist for empty from")
	}
	if got := v.(*atomic.Int64).Load(); got != 1 {
		t.Errorf("statusChanges[empty->up] = %d, want 1", got)
	}
}

func TestRecordStatusChange_DistinctPairsTrackedIndependently(t *testing.T) {
	c := NewCollector([]string{"api"}, nil)
	c.RecordStatusChange("api", "up", "down")
	c.RecordStatusChange("api", "down", "up")
	c.RecordStatusChange("api", "up", "down")

	upToDown, _ := c.statusChanges.Load("api\x00up\x00down")
	downToUp, _ := c.statusChanges.Load("api\x00down\x00up")

	if got := upToDown.(*atomic.Int64).Load(); got != 2 {
		t.Errorf("up->down = %d, want 2", got)
	}
	if got := downToUp.(*atomic.Int64).Load(); got != 1 {
		t.Errorf("down->up = %d, want 1", got)
	}
}

// --- WritePrometheus ---

func TestWritePrometheus_AllFamiliesPresent(t *testing.T) {
	c := NewCollector([]string{"api"}, nil)

	var buf strings.Builder
	if err := c.WritePrometheus(&buf); err != nil {
		t.Fatalf("WritePrometheus() error: %v", err)
	}
	out := buf.String()

	families := []string{
		"pulseboard_info",
		"pulseboard_endpoint_status",
		"pulseboard_polls_total",
		"pulseboard_poll_duration_seconds",
		"pulseboard_status_changes_total",
	}
	for _, f := range families {
		if !strings.Contains(out, f) {
			t.Errorf("expected metric family %q in output:\n%s", f, out)
		}
	}
}

func TestWritePrometheus_StatusGaugeOneActive(t *testing.T) {
	c := NewCollector([]string{"api"}, nil)
	c.RecordPoll("api", 10*time.Millisecond, "up", false)

	var buf strings.Builder
	if err := c.WritePrometheus(&buf); err != nil {
		t.Fatalf("WritePrometheus() error: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, `pulseboard_endpoint_status{name="api",status="up"} 1`) {
		t.Errorf("expected up=1, output:\n%s", out)
	}
	for _, st := range []string{"down", "degraded", "unknown", "stale"} {
		want := `pulseboard_endpoint_status{name="api",status="` + st + `"} 0`
		if !strings.Contains(out, want) {
			t.Errorf("expected %s=0, output:\n%s", st, out)
		}
	}
}

func TestWritePrometheus_PollsTotalSuccessEqualsTotal_MinusErrors(t *testing.T) {
	c := NewCollector([]string{"api"}, nil)
	c.RecordPoll("api", 10*time.Millisecond, "up", false)
	c.RecordPoll("api", 10*time.Millisecond, "up", false)
	c.RecordPoll("api", 10*time.Millisecond, "down", true)

	var buf strings.Builder
	if err := c.WritePrometheus(&buf); err != nil {
		t.Fatalf("WritePrometheus() error: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, `pulseboard_polls_total{name="api",result="success"} 2`) {
		t.Errorf("expected success=2, output:\n%s", out)
	}
	if !strings.Contains(out, `pulseboard_polls_total{name="api",result="error"} 1`) {
		t.Errorf("expected error=1, output:\n%s", out)
	}
}

func TestWritePrometheus_InfBucketMatchesLatencyCount(t *testing.T) {
	c := NewCollector([]string{"api"}, DefaultBuckets)

	for i := range 7 {
		c.RecordPoll("api", time.Duration(i)*50*time.Millisecond, "up", false)
	}

	var buf strings.Builder
	if err := c.WritePrometheus(&buf); err != nil {
		t.Fatalf("WritePrometheus() error: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, `pulseboard_poll_duration_seconds_bucket{name="api",le="+Inf"} 7`) {
		t.Errorf("expected +Inf bucket = 7, output:\n%s", out)
	}
	if !strings.Contains(out, `pulseboard_poll_duration_seconds_count{name="api"} 7`) {
		t.Errorf("expected count = 7, output:\n%s", out)
	}
}

func TestWritePrometheus_ContentTypeString(t *testing.T) {
	// documents the expected Content-Type for the /metrics handler
	const want = "text/plain; version=0.0.4; charset=utf-8"
	if !strings.Contains(want, "text/plain") || !strings.Contains(want, "version=0.0.4") {
		t.Errorf("content-type %q does not match Prometheus exposition format spec", want)
	}
}

// --- Concurrency ---

func TestRecordPoll_Concurrent_NoDataRace(t *testing.T) {
	t.Parallel()
	c := NewCollector([]string{"api"}, nil)

	const goroutines = 10
	const polls = 100

	var wg sync.WaitGroup
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range polls {
				c.RecordPoll("api", time.Duration(j)*time.Millisecond, "up", false)
			}
		}()
	}
	wg.Wait()

	want := int64(goroutines * polls)
	if got := c.pollTotal["api"].Load(); got != want {
		t.Errorf("pollTotal = %d, want %d", got, want)
	}
}

// --- splitKey ---

func TestSplitKey_ValidKey(t *testing.T) {
	tests := []struct {
		key        string
		wantName   string
		wantFrom   string
		wantTo     string
	}{
		{"api\x00up\x00down", "api", "up", "down"},
		{"my-service\x00\x00up", "my-service", "", "up"}, // first poll (from = "")
		{"svc\x00degraded\x00up", "svc", "degraded", "up"},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			name, from, to, ok := splitKey(tt.key)
			if !ok {
				t.Fatalf("splitKey(%q) ok = false, want true", tt.key)
			}
			if name != tt.wantName {
				t.Errorf("name = %q, want %q", name, tt.wantName)
			}
			if from != tt.wantFrom {
				t.Errorf("from = %q, want %q", from, tt.wantFrom)
			}
			if to != tt.wantTo {
				t.Errorf("to = %q, want %q", to, tt.wantTo)
			}
		})
	}
}

func TestSplitKey_MissingFirstSeparator(t *testing.T) {
	_, _, _, ok := splitKey("noseperator")
	if ok {
		t.Error("splitKey() ok = true for key with no separator, want false")
	}
}

func TestSplitKey_MissingSecondSeparator(t *testing.T) {
	_, _, _, ok := splitKey("api\x00onlyone")
	if ok {
		t.Error("splitKey() ok = true for key with only one separator, want false")
	}
}

func TestSplitKey_EmptyString(t *testing.T) {
	_, _, _, ok := splitKey("")
	if ok {
		t.Error("splitKey(\"\") ok = true, want false")
	}
}

// --- WritePrometheus error path ---

// failWriter is an io.Writer that always returns an error.
type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, fmt.Errorf("forced write error") }

func TestWritePrometheus_WriterError(t *testing.T) {
	c := NewCollector([]string{"api"}, nil)
	c.RecordPoll("api", 10*time.Millisecond, "up", false)

	err := c.WritePrometheus(failWriter{})
	if err == nil {
		t.Error("WritePrometheus() with failing writer: expected error, got nil")
	}
}
