package pulseboard

import (
	"fmt"
	"testing"
)

func TestStatus_String(t *testing.T) {
	tests := []struct {
		status Status
		want   string
	}{
		{StatusUp, "up"},
		{StatusDown, "down"},
		{StatusDegraded, "degraded"},
		{StatusUnknown, "unknown"},
		{StatusError, "error"},
		{Status("custom"), "custom"},
		{Status(""), ""},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.String(); got != tt.want {
				t.Errorf("Status(%q).String() = %q, want %q", string(tt.status), got, tt.want)
			}
		})
	}
}

func TestStatus_ImplementsFmtStringer(t *testing.T) {
	// Compile-time assertion: Status must satisfy fmt.Stringer.
	var _ fmt.Stringer = StatusUp
}
