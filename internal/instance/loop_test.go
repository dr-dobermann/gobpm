package instance

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// newBareLoopInstance builds the minimal Instance a direct loop-method call
// needs: the loop channels and an empty tracks registry, no engine wiring.
func newBareLoopInstance() *Instance {
	return &Instance{
		events:   make(chan trackEvent, 1),
		taskReq:  make(chan taskRequest),
		jobReq:   make(chan jobRequest),
		tracks:   map[string]*track{},
		loopDone: make(chan struct{}),
	}
}

// TestLoopNoInitialTracksCompletes: a loop started with zero initial tracks
// settles the instance to Completed immediately and closes Done().
func TestLoopNoInitialTracksCompletes(t *testing.T) {
	inst := newBareLoopInstance()

	inst.loop(t.Context(), nil)

	require.Equal(t, Completed, inst.State())

	select {
	case <-inst.Done():
	default:
		t.Fatal("loop exit should close the Done channel")
	}
}

// TestOnWaitingStoppingDrops: once the loop is stopping, a late evWaiting is
// dropped — the track is not recorded as parked-and-undelivered (no delivery
// will ever target it; its evtCh is being closed by stopAll).
func TestOnWaitingStoppingDrops(t *testing.T) {
	inst := newBareLoopInstance()
	waiting := map[string]struct{}{}
	msgIdx := map[string]*track{}

	inst.onWaiting(trackEvent{}, true, waiting, msgIdx)

	require.Empty(t, waiting)
	require.Empty(t, msgIdx)
}

// TestFireOrJoinUnknownSurvivor: a join fire whose survivor id is no longer in
// the tracks registry is a no-op, not a nil dereference — the survivor may
// have been torn down between the recheck and the fire.
func TestFireOrJoinUnknownSurvivor(t *testing.T) {
	inst := newBareLoopInstance()

	require.NotPanics(t, func() {
		inst.fireOrJoin("ghost", nil,
			map[string]flow.Node{}, map[string]flow.Node{})
	})
}
