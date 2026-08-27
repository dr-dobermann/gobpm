package instance

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/pkg/interactor"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// fanOutTrack builds a track already running an iterated activity, with the
// decorator installed as its executor — the state a parallel fan-out is in
// while its instances hold approvals.
func fanOutTrack(t *testing.T) (*loopState, *track, *iterDecorator) {
	t.Helper()

	inst := newBareLoopInstance()
	inst.td = interactor.NopDistributor()
	inst.now = time.Now
	inst.s = &snapshot.Snapshot{ProcessID: "p"}

	mi, err := activities.NewMultiInstance(
		activities.WithInputCollection("items", "item"))
	require.NoError(t, err)

	ut, err := activities.NewUserTask("approve",
		activities.WithCandidateUsers("alice"),
		activities.WithOutput("result", "string", true),
		activities.WithoutParams(), activities.WithLoop(mi),
		foundation.WithID("approve"))
	require.NoError(t, err)

	step := &stepInfo{node: ut}
	tr := &track{
		instance: inst,
		steps:    []*stepInfo{step},
		evtCh:    make(chan flow.EventDefinition, eventBufferDepth),
	}

	d := &iterDecorator{t: tr, step: step, eventSubs: newEventSubs("inst", ut.ID())}
	tr.exec.Store(&execHandle{e: d, node: ut})

	return newLoopState(inst), tr, d
}

// TestACompletionGoesToTheINSTANCEThatOwnsIt (ADR-020 §2.12, SRD-090.B FR-3):
// N instances of one activity park at once, so a completion names WHICH of
// them did the work — it is not the track's.
//
// The track leaves the parked set only when the last of them is done: that one
// entry stands for all N, so flipping it on the first delivery would drop
// every later occurrence at the gate that reads it.
func TestACompletionGoesToTheINSTANCEThatOwnsIt(t *testing.T) {
	ls, tr, d := fanOutTrack(t)
	def := sigDefN(t, "done")

	d.parking(0)
	d.parking(1)
	ls.waiting[tr.ID()] = struct{}{}

	ls.deliverCompletion(taskEntry{track: tr, ord: 1}, def)

	held := d.takeDeliveries()
	require.Len(t, held, 1,
		"one completion, for one instance — NOT its sibling, who is still "+
			"holding an approval nobody has done")
	require.Equal(t, 1, held[0].ord, "the instance that owns it")
	require.Equal(t, def.ID(), held[0].def.ID())

	require.Contains(t, ls.waiting, tr.ID(),
		"the track stays parked while any instance still holds work")

	require.True(t, d.busy(),
		"and reads busy until that instance takes its envelope, so a "+
			"dehydration cannot take the track away mid-handover")

	ls.deliverCompletion(taskEntry{track: tr, ord: 0}, def)
	require.NotContains(t, ls.waiting, tr.ID(),
		"with the last approval delivered, nobody holds work any more")
}

// TestALoneTaskKeepsTheTrackChannel: an activity with one instance has no
// decorator, so its completion travels the track's own channel and the track
// flips out immediately. The fan-out work must not change that path.
func TestALoneTaskKeepsTheTrackChannel(t *testing.T) {
	inst := newBareLoopInstance()
	inst.td = interactor.NopDistributor()
	inst.now = time.Now
	inst.s = &snapshot.Snapshot{ProcessID: "p"}

	ut, err := activities.NewUserTask("approve",
		activities.WithCandidateUsers("alice"),
		activities.WithOutput("result", "string", true),
		activities.WithoutParams(), foundation.WithID("lone"))
	require.NoError(t, err)

	ls := newLoopState(inst)
	tr := &track{
		instance: inst,
		steps:    []*stepInfo{{node: ut}},
		evtCh:    make(chan flow.EventDefinition, eventBufferDepth),
	}

	ls.waiting[tr.ID()] = struct{}{}
	def := sigDefN(t, "done")

	ls.deliverCompletion(taskEntry{track: tr}, def)

	select {
	case got := <-tr.evtCh:
		require.Equal(t, def.ID(), got.ID())
	default:
		t.Fatal("a lone task's completion must reach its track")
	}

	require.NotContains(t, ls.waiting, tr.ID(),
		"one instance, one wait, and it is over")
}

// TestACompletionForAFanOutThatIsNotRunningIsHeld: a restored fan-out is
// rebuilt by the very action being applied to it, so the ordinary case is that
// the first completion reaches the loop before the track's goroutine has
// started the decorator.
//
// It cannot be refused — the instance is resident, so there is no hydration to
// replay after — and it must not resolve an executor lazily either: that
// builds one nobody runs and loses the completion inside it, marking the work
// performed and withdrawing the task. Held on the TRACK, which outlives every
// decorator run.
func TestACompletionForAFanOutThatIsNotRunningIsHeld(t *testing.T) {
	ls, tr, _ := fanOutTrack(t)
	def := sigDefN(t, "done")

	// the decorator's run has ended — the state a released activity is in.
	tr.exec.Store(nil)

	require.False(t, tr.instancesBusy(),
		"with no executor there is no instance working, so the loop is free "+
			"to release the track")

	ls.deliverCompletion(
		taskEntry{track: tr, node: tr.steps[0].node, ord: 1}, def)

	held := tr.takePendingCompletions()
	require.Len(t, held, 1)
	require.Equal(t, def.ID(), held[1].ID(),
		"kept for the instance whose work it is")

	require.Nil(t, tr.takePendingCompletions(),
		"and taken ONCE: they belong to the decorator that took them, and a "+
			"second run must not re-deliver work already completed")
}
