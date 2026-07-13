package observability

import (
	"log/slog"
	"testing"
)

func TestLoggable(t *testing.T) {
	tests := []struct {
		name string
		kind Kind
		want bool
	}{
		{"lifecycle kind echoes", KindInstanceState, true},
		{"flow kind echoes", KindNodeProgress, true},
		{"data change is stream-only", KindDataChange, false},
		{"unknown kind still echoes", Kind("Whatever"), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := loggable(tt.kind); got != tt.want {
				t.Errorf("loggable(%q) = %v, want %v", tt.kind, got, tt.want)
			}
		})
	}
}

func TestEchoLevel(t *testing.T) {
	tests := []struct {
		name  string
		kind  Kind
		phase Phase
		want  slog.Level
	}{
		{"lifecycle default is info", KindEngineState, PhaseStarted, slog.LevelInfo},
		{"flow default is debug", KindNodeProgress, PhaseEntered, slog.LevelDebug},
		{"instance failed escalates to error", KindInstanceState, PhaseFailed, slog.LevelError},
		{"uncaught fault escalates to error", KindFault, PhaseUncaught, slog.LevelError},
		{"caught fault stays debug", KindFault, PhaseCaught, slog.LevelDebug},
		{"retries exhausted warns", KindJobState, PhaseRetriesExhausted, slog.LevelWarn},
		{"lock reclaimed warns", KindJobState, PhaseLockReclaimed, slog.LevelWarn},
		{"ordinary job phase stays debug", KindJobState, PhaseEnqueued, slog.LevelDebug},
		{"unclassified kind surfaces at error", Kind("Mystery"), Phase("X"), slog.LevelError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := echoLevel(tt.kind, tt.phase); got != tt.want {
				t.Errorf("echoLevel(%q, %q) = %v, want %v",
					tt.kind, tt.phase, got, tt.want)
			}
		})
	}
}
