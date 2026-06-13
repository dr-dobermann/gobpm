package instance

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/stretchr/testify/require"
)

// TestTrackEntersProcessStepResults proves the ADR-001 §4.2 results-processing
// stage is real and observable: after a linear run, the track's raw state
// history contains TrackProcessStepResults between execution and the terminal
// state — not just a declared-but-unentered constant (audit 1.6 / FIX-003 D).
func TestTrackEntersProcessStepResults(t *testing.T) {
	_ = data.CreateDefaultStates()

	s := buildLinearSnapshot(t)
	ep := mockeventproc.NewMockEventProducer(t)

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(), ep, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, inst.Run(ctx))
	require.Eventually(t,
		func() bool { return inst.State() == Completed },
		2*time.Second, 5*time.Millisecond)

	snap := inst.tracksSnap.Load()
	require.NotNil(t, snap)
	require.NotEmpty(t, *snap)

	// the raw transition history records the stage on every executed node.
	seenResults := false
	seenExecuting := false

	for _, tr := range *snap {
		h := tr.hist.Load()
		require.NotNil(t, h)

		for _, u := range *h {
			switch u.state {
			case TrackExecutingStep:
				seenExecuting = true
			case TrackProcessStepResults:
				seenResults = true
			}
		}
	}

	require.True(t, seenExecuting, "track must record the executing stage")
	require.True(t, seenResults,
		"track must enter TrackProcessStepResults (ADR-001 §4.2)")
}
