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

// TestAdHocEchoLevel pins the Ad-Hoc routing kind to the operator log at Info
// (SRD-074 §3.6): a routing decision is a human-steered milestone, so it must
// neither drop to flow-tracing Debug nor fall through to the unclassified-kind
// Error, and it is never stream-only.
func TestAdHocEchoLevel(t *testing.T) {
	for _, phase := range []Phase{
		PhaseOffered, PhaseActivated, PhaseCompleted, PhaseCanceled,
	} {
		if got := echoLevel(KindAdHoc, phase); got != slog.LevelInfo {
			t.Errorf("echoLevel(%q, %q) = %v, want %v",
				KindAdHoc, phase, got, slog.LevelInfo)
		}
	}

	if !loggable(KindAdHoc) {
		t.Error("KindAdHoc must reach the operator log, not the stream alone")
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
		{"rules failure warns", KindRules, PhaseFailed, slog.LevelWarn},
		{"runtime deploy is an info milestone", KindRules, PhaseDeployed, slog.LevelInfo},
		{"rules invocation stays debug", KindRules, PhaseInvoked, slog.LevelDebug},
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
