package instance

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
)

// TestBoundaryPlanRestoredNotReevaluated covers SRD-071 T-15 (FR-9a) at the
// seam that decides it: an arm that follows a restore takes the RECORDED
// deadline, and an ordinary arm resolves its own.
//
// Everything about a boundary is reconstructible from the model except WHEN it
// fires. "Escalate 24 hours from now" re-evaluated at restore means 24 hours
// from the restore — the deadline walks forward on every rebuild and the
// escalation never arrives. This is the trap SRD-070 §4.2 closed for a track's
// own timer, and it was still open for the boundary guarding it.
func TestBoundaryPlanRestoredNotReevaluated(t *testing.T) {
	recorded := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)

	inst := &Instance{}
	inst.seedBoundaryPlans([]checkpoint.BoundaryRecord{
		{
			HostTrack:  "track-1",
			BoundaryID: "escalate",
			DefIndex:   0,
			Timer:      &checkpoint.TimerDescriptor{Deadline: recorded, CyclesLeft: 2},
		},
		// a non-timer arm records no plan: it re-arms from the model, so
		// there is nothing a restore could pin.
		{HostTrack: "track-1", BoundaryID: "cancel-msg", DefIndex: 1},
	})

	require.Len(t, inst.boundaryPlans, 1,
		"only an arm with a resolved deadline carries a plan")

	key := boundaryKey{
		trackID: "track-1", boundaryID: "escalate", defIndex: 0,
	}

	got, ok := inst.takeBoundaryPlan(key)
	require.True(t, ok)
	require.True(t, recorded.Equal(got.Deadline),
		"the restored arm fires at the deadline the checkpoint recorded")
	require.Equal(t, 2, got.CyclesLeft)

	// CONSUMED: the plan describes the one window the checkpoint captured.
	// Entering the same activity again is a new window with its own deadline —
	// otherwise a looping activity would inherit a deadline from a previous
	// pass forever.
	_, again := inst.takeBoundaryPlan(key)
	require.False(t, again, "a restored plan is used once, not pinned forever")

	_, unknown := inst.takeBoundaryPlan(boundaryKey{trackID: "track-9"})
	require.False(t, unknown, "an arm with no record resolves its own deadline")
}

// TestBoundaryHintOnlyWhenRestored: the DeadlineHinter capability answers only
// for a restored arm. A freshly armed boundary must hint NOTHING, or the waiter
// would pin a deadline the model never asked for.
func TestBoundaryHintOnlyWhenRestored(t *testing.T) {
	fresh := &boundaryWatch{deadline: time.Now(), cycles: 1}

	_, _, ok := fresh.TimerDeadlineHint("edef-1")
	require.False(t, ok,
		"a freshly resolved plan is not a hint — the waiter evaluates")

	when := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)
	restored := &boundaryWatch{deadline: when, cycles: 3, hinted: true}

	got, cycles, ok := restored.TimerDeadlineHint("edef-1")
	require.True(t, ok)
	require.True(t, when.Equal(got))
	require.Equal(t, 3, cycles)
}

// TestSeedBoundaryPlansEmpty: a Schema-1 document carries no boundaries, and an
// instance restored from one behaves exactly as it did before FR-9a.
func TestSeedBoundaryPlansEmpty(t *testing.T) {
	inst := &Instance{}
	inst.seedBoundaryPlans(nil)

	require.Nil(t, inst.boundaryPlans)

	_, ok := inst.takeBoundaryPlan(boundaryKey{trackID: "t"})
	require.False(t, ok, "a lookup against no plans is a plain miss")
}

// TestHoldBoundaryDeclines covers the paths on which a boundary arm takes NO
// hold (SRD-071 FR-9a). Each one costs residency and never a lost deadline:
// the instance simply stays in memory, where the ordinary hub waiter fires.
func TestHoldBoundaryDeclines(t *testing.T) {
	sig, err := events.NewSignal("break", nil)
	require.NoError(t, err)

	sdef, err := events.NewSignalEventDefinition(sig)
	require.NoError(t, err)

	t.Run("no holder registry", func(t *testing.T) {
		ls := &loopState{inst: &Instance{}}

		require.False(t, ls.holdBoundary(&boundaryWatch{def: sdef}),
			"an engine without holders holds nothing")
	})

	t.Run("a loop-owned conditional boundary", func(t *testing.T) {
		inst := &Instance{waitHolders: newFakeHolders()}
		ls := &loopState{inst: inst}

		// Its trigger is the instance's OWN data commits, so nothing outside
		// the loop could ever evaluate it — the same reason a Conditional
		// catch is unholdable.
		require.False(t,
			ls.holdBoundary(&boundaryWatch{def: sdef, loopOwned: true}),
			"a loop-owned watch cannot be held by anything external")
	})

	t.Run("a timer with no resolved deadline", func(t *testing.T) {
		inst := &Instance{waitHolders: newFakeHolders()}
		ls := &loopState{inst: inst}

		texpr, err := goexpr.New(nil,
			data.MustItemDefinition(values.NewVariable(time.Time{})),
			func(context.Context, data.Source) (data.Value, error) {
				return values.NewVariable(time.Now().Add(time.Hour)), nil
			})
		require.NoError(t, err)

		tdef, err := events.NewTimerEventDefinition(texpr, nil, nil)
		require.NoError(t, err)

		// the watch carries no resolved plan, so there is nothing to hand to
		// the timer service.
		require.False(t, ls.holdBoundary(&boundaryWatch{def: tdef}),
			"there is no deadline to hand to the timer service")
	})
}
