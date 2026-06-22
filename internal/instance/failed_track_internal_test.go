package instance

import (
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/stretchr/testify/require"
)

// TestFailFromTrackNilErr covers failFromTrack's defensive nil-guard: a
// TrackFailed track with no recorded error must still fault the instance with a
// synthesized error, never store a nil — which would re-open the silent-completion
// hole FIX-008 closes. run() always sets lastErr before TrackFailed, so this
// branch is unreachable through normal execution and is exercised directly here.
func TestFailFromTrackNilErr(t *testing.T) {
	_ = data.CreateDefaultStates()

	s := buildPlainSnapshot(t)

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(),
		mockeventproc.NewMockEventProducer(t), nil)
	require.NoError(t, err)

	tr := &track{instance: inst} // lastErr == nil

	inst.failFromTrack(tr, func() {}) // stopAll is a no-op for this unit test

	require.Error(t, inst.LastErr(),
		"a nil track error must be synthesized, not stored as nil")
}
