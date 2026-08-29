package instance

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
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
// iteration restored from one behaves exactly as it did before FR-9a.
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

// TestBoundaryRecordsCapture covers the capture side of SRD-071 FR-9a: the
// armed boundaries of LIVE tracks ride the checkpoint, carrying the resolved
// deadline that a restore must not recompute.
func TestBoundaryRecordsCapture(t *testing.T) {
	inst, tr, ls := userTaskArmed(t)

	when := inst.now().Add(24 * time.Hour)

	bev := aBoundary(t)

	ls.watchers[tr.ID()] = []*boundaryWatch{
		// a timer arm: its deadline is the whole point of the record.
		{host: tr, boundary: bev, def: bev.Definitions()[0],
			deadline: when, cycles: 2},
		// a second arm with no resolved deadline: recorded, but with no
		// timer — it re-arms from the model, so there is nothing to pin.
		{host: tr, boundary: bev, def: bev.Definitions()[0], defIndex: 1},
	}

	recs := ls.boundaryRecords()
	require.Len(t, recs, 2, "every armed watch of a live track is recorded")

	byIndex := map[int]checkpoint.BoundaryRecord{}
	for _, r := range recs {
		byIndex[r.DefIndex] = r
	}

	timed := byIndex[0]
	require.Equal(t, tr.ID(), timed.HostTrack)
	require.Equal(t, bev.ID(), timed.BoundaryID)
	require.NotNil(t, timed.Timer)
	require.True(t, when.Equal(timed.Timer.Deadline))
	require.Equal(t, 2, timed.Timer.CyclesLeft)

	require.Nil(t, byIndex[1].Timer,
		"an arm with no resolved deadline records none")

	// A watch whose host is no longer live guards nothing, so it is not
	// written: the capture records what a restore must rebuild.
	tr.updateState(TrackEnded)

	require.Empty(t, ls.boundaryRecords(),
		"a dead track's boundaries are not captured")
}

// aBoundary builds a timer boundary over a throwaway activity — the capture
// reads only the boundary's identity and the watch's own fields.
func aBoundary(t *testing.T) flow.BoundaryEvent {
	t.Helper()

	host, err := activities.NewUserTask("guarded",
		activities.WithCandidateUsers("op"),
		activities.WithOutput("result", "string", true),
		activities.WithoutParams())
	require.NoError(t, err)

	texpr, err := goexpr.New(nil,
		data.MustItemDefinition(values.NewVariable(time.Time{})),
		func(context.Context, data.Source) (data.Value, error) {
			return values.NewVariable(time.Now().Add(time.Hour)), nil
		})
	require.NoError(t, err)

	tdef, err := events.NewTimerEventDefinition(texpr, nil, nil)
	require.NoError(t, err)

	bev, err := events.NewBoundaryEvent("escalate", host, tdef, true)
	require.NoError(t, err)

	return bev
}

// TestPlanBoundary covers the arm-time plan (SRD-071 FR-9a): a RESTORED plan
// wins and is marked as a hint; otherwise a timer resolves its own deadline
// and a non-timer resolves nothing.
func TestPlanBoundary(t *testing.T) {
	inst, tr, ls := userTaskArmed(t)
	bev := aBoundary(t)

	t.Run("a restored plan wins and hints", func(t *testing.T) {
		when := inst.now().Add(48 * time.Hour)

		w := &boundaryWatch{host: tr, boundary: bev, def: bev.Definitions()[0]}

		inst.seedBoundaryPlans([]checkpoint.BoundaryRecord{{
			HostTrack:  tr.ID(),
			BoundaryID: bev.ID(),
			Timer:      &checkpoint.TimerDescriptor{Deadline: when, CyclesLeft: 1},
		}})

		ls.planBoundary(w)

		require.True(t, w.hinted, "a restored arm pins its recorded deadline")
		require.True(t, when.Equal(w.deadline))
		require.Equal(t, 1, w.cycles)
	})

	t.Run("a fresh timer resolves its own", func(t *testing.T) {
		w := &boundaryWatch{host: tr, boundary: bev, def: bev.Definitions()[0]}

		ls.planBoundary(w)

		require.False(t, w.hinted, "a freshly resolved plan is not a hint")
		require.False(t, w.deadline.IsZero(),
			"the definition resolves a deadline at arm time")
	})

	t.Run("a non-timer resolves nothing", func(t *testing.T) {
		sig, err := events.NewSignal("break", nil)
		require.NoError(t, err)

		sdef, err := events.NewSignalEventDefinition(sig)
		require.NoError(t, err)

		w := &boundaryWatch{host: tr, boundary: bev, def: sdef}

		ls.planBoundary(w)

		require.True(t, w.deadline.IsZero(), "only a timer carries a deadline")
	})
}

// TestHoldBoundaryTakes covers the accepted holds: a timer arm hands its
// deadline to the engine, a signal arm its subscription. Both make the guarded
// instance releasable (SRD-071 FR-9a).
func TestHoldBoundaryTakes(t *testing.T) {
	inst, tr, ls := userTaskArmed(t)

	holders := newFakeHolders()
	inst.waitHolders = holders

	bev := aBoundary(t)

	timed := &boundaryWatch{
		host: tr, boundary: bev, def: bev.Definitions()[0],
		deadline: inst.now().Add(24 * time.Hour),
	}

	require.True(t, ls.holdBoundary(timed),
		"a timer boundary hands its deadline to the engine")

	sig, err := events.NewSignal("break", nil)
	require.NoError(t, err)

	sdef, err := events.NewSignalEventDefinition(sig)
	require.NoError(t, err)

	signalled := &boundaryWatch{host: tr, boundary: bev, def: sdef, defIndex: 1}

	require.True(t, ls.holdBoundary(signalled),
		"a signal boundary hands its subscription to the engine")
	require.Equal(t, 1, holders.subCount())
}
