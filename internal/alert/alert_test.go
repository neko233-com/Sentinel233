package alert

import (
	"testing"
)

func TestStateString(t *testing.T) {
	tests := []struct {
		state    State
		expected string
	}{
		{StateInactive, "inactive"},
		{StatePending, "pending"},
		{StateFiring, "firing"},
		{State(99), "unknown"},
	}
	for _, tt := range tests {
		if tt.state.String() != tt.expected {
			t.Errorf("State(%d).String() = %q, want %q", tt.state, tt.state.String(), tt.expected)
		}
	}
}

func TestParsePromDuration(t *testing.T) {
	tests := []struct {
		input string
		want  int64 // milliseconds
	}{
		{"1m", 60000},
		{"5m", 300000},
		{"1h", 3600000},
		{"30s", 30000},
		{"1d", 86400000},
	}
	for _, tt := range tests {
		d, err := parsePromDuration(tt.input)
		if err != nil {
			t.Errorf("parsePromDuration(%q) error: %v", tt.input, err)
			continue
		}
		if d.Milliseconds() != tt.want {
			t.Errorf("parsePromDuration(%q) = %dms, want %dms", tt.input, d.Milliseconds(), tt.want)
		}
	}
}
