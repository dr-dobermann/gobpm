package instance

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// SRD-029 M3b — boundary watchers: arm on arrival, fire (interrupting), tear down.
// The deterministic unit tests drive applyEvent / the boundary helpers directly with
// bare tracks; the end-to-end test runs a real instance and fires through the watch's
// own ProcessEvent (exactly what the hub does on a fire).

var errRegRejected = errors.New("registration rejected")

// recordingProducer is a plain (non-reflecting) EventProducer: it records boundary
// registrations/unregistrations without a mock matcher reflecting over the live host
// track (see the failEventProducer note in message_flow_test.go). regErr, when set,
// fails every registration to exercise the arm-failure fault path.
type recordingProducer struct {
	mu       sync.Mutex
	watch    *boundaryWatch
	regIDs   []string
	unreg    []string
	regErr   error
	unregErr error
}

func (r *recordingProducer) RegisterEvent(
	p eventproc.EventProcessor, d flow.EventDefinition,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.regErr != nil {
		return r.regErr
	}

	if w, ok := p.(*boundaryWatch); ok {
		r.watch = w
		r.regIDs = append(r.regIDs, d.ID())
	}

	return nil
}

func (r *recordingProducer) UnregisterEvent(
	_ eventproc.EventProcessor, defID string,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.unreg = append(r.unreg, defID)

	return r.unregErr
}

func (r *recordingProducer) PropagateEvent(
	context.Context, flow.EventDefinition,
) error {
	return nil
}

// capturedWatch returns the last registered boundaryWatch (nil until a boundary arms).
func (r *recordingProducer) capturedWatch() *boundaryWatch {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.watch
}

func (r *recordingProducer) registeredDefs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]string{}, r.regIDs...)
}

func (r *recordingProducer) unregisteredDefs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]string{}, r.unreg...)
}

// guardedHostInstance builds an instance over start -> host(ReceiveTask) -> end with an
// interrupting signal boundary on host routing to excEnd, plus any extra boundaries the
// caller attaches via more. It returns the instance and the MODEL host/boundary/excEnd
// nodes (the direct tests arm against these; their identity is what the watch carries).
func guardedHostInstance(
	t *testing.T,
	ep eventproc.EventProducer,
	more func(host flow.ActivityNode, p *process.Process),
) (inst *Instance, host flow.ActivityNode, beA flow.BoundaryEvent, excEndA flow.Node, sigDefA flow.EventDefinition) {
	t.Helper()

	_ = data.CreateDefaultStates()

	p, err := process.New("srd029-m3b")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	rt, err := activities.NewReceiveTask("host",
		bpmncommon.MustMessage("m",
			data.MustItemDefinition(values.NewVariable(1))))
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	sig, err := events.NewSignal("sigA", nil)
	require.NoError(t, err)

	def, err := events.NewSignalEventDefinition(sig)
	require.NoError(t, err)

	be, err := events.NewBoundaryEvent("bndA", rt, def, true)
	require.NoError(t, err)

	exc, err := events.NewEndEvent("excA")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, rt, end, be, exc} {
		require.NoError(t, p.Add(e))
	}

	_, err = flow.Link(start, rt)
	require.NoError(t, err)
	_, err = flow.Link(rt, end)
	require.NoError(t, err)
	_, err = flow.Link(be, exc)
	require.NoError(t, err)

	if more != nil {
		more(rt, p)
	}

	s, err := snapshot.New(p)
	require.NoError(t, err)

	inst, err = New(s, scope.EmptyDataPath, enginert.Default(), ep, nil)
	require.NoError(t, err)

	return inst, rt, be, exc, def
}

// bareTrack builds an un-spawned track positioned on node, so a direct applyEvent test
// drives the loop helpers without a run() goroutine competing for state.
func bareTrack(t *testing.T, inst *Instance, node flow.Node) *track {
	t.Helper()

	be, err := foundation.NewBaseElement()
	require.NoError(t, err)

	return &track{
		BaseElement: *be,
		instance:    inst,
		steps:       []*stepInfo{{node: node, state: StepCreated}},
		state:       TrackReady,
		evtCh:       make(chan flow.EventDefinition, eventBufferDepth),
	}
}

func TestBoundaryEventKindString(t *testing.T) {
	require.Equal(t, "boundary", evBoundary.String())
}

// TestArmDisarmBoundaryWatch (T-12): arming registers a watch for every boundary —
// interrupting and non-interrupting alike (M3c) — each watch carries a unique
// (host, boundary, def) identity, and disarming unregisters them all and clears the
// entry.
func TestArmDisarmBoundaryWatch(t *testing.T) {
	ep := &recordingProducer{}

	var defB flow.EventDefinition

	// host carries an interrupting signal-A boundary plus a non-interrupting signal-B one.
	inst, host, beA, _, sigDefA := guardedHostInstance(t, ep,
		func(h flow.ActivityNode, p *process.Process) {
			sigB, err := events.NewSignal("sigB", nil)
			require.NoError(t, err)

			defB, err = events.NewSignalEventDefinition(sigB)
			require.NoError(t, err)

			beB, err := events.NewBoundaryEvent("bndB", h, defB, false) // non-interrupting
			require.NoError(t, err)

			excB, err := events.NewEndEvent("excB")
			require.NoError(t, err)

			require.NoError(t, p.Add(beB))
			require.NoError(t, p.Add(excB))
			_, err = flow.Link(beB, excB)
			require.NoError(t, err)
		})

	tr := bareTrack(t, inst, host)
	ls := newLoopState(inst)

	ls.armBoundaries(tr, host)

	require.Len(t, ls.watchers[tr.ID()], 2,
		"both the interrupting and non-interrupting boundaries are armed")
	require.ElementsMatch(t, []string{sigDefA.ID(), defB.ID()}, ep.registeredDefs(),
		"both boundary definitions are registered")

	// the interrupting watch carries the (host, boundary, def) identity.
	var wA *boundaryWatch
	for _, w := range ls.watchers[tr.ID()] {
		if w.boundary.ID() == beA.ID() {
			wA = w
		}
	}
	require.NotNil(t, wA)
	require.Equal(t,
		"boundary-watch:"+tr.ID()+":"+beA.ID()+":"+sigDefA.ID(), wA.ID(),
		"the watch identity is unique per (host, boundary, def)")

	ls.disarmBoundaries(tr.ID())

	require.NotContains(t, ls.watchers, tr.ID(), "disarm clears the track's entry")
	require.ElementsMatch(t, []string{sigDefA.ID(), defB.ID()}, ep.unregisteredDefs(),
		"disarm unregisters every armed watch")
}

// TestDisarmBoundariesUnregisterErrorIsLogged (FIX-022 A6): a boundary whose
// hub UnregisterEvent errors (the idempotent "already gone" case) is best-effort
// — disarm logs it at Debug and still clears the track's watcher entry, rather
// than bare-discarding the error (ADR-022 v.1 §2.3(2)).
func TestDisarmBoundariesUnregisterErrorIsLogged(t *testing.T) {
	ep := &recordingProducer{unregErr: errRegRejected}
	inst, host, _, _, _ := guardedHostInstance(t, ep, nil)
	inst.tracks = map[string]*track{}

	tr := bareTrack(t, inst, host)
	ls := newLoopState(inst)
	ls.armBoundaries(tr, host)
	require.NotEmpty(t, ls.watchers[tr.ID()], "the boundary armed")

	ls.disarmBoundaries(tr.ID()) // UnregisterEvent errors → the Debug branch

	require.NotContains(t, ls.watchers, tr.ID(),
		"disarm clears the entry despite the unregister error")
}

// TestFireBoundaryInterrupts (T-4 core): an interrupting fire cancels the guarded track,
// continues the instance on the boundary's exception flow, and tears the watch down.
func TestFireBoundaryInterrupts(t *testing.T) {
	ep := &recordingProducer{}
	inst, host, beA, excEndA, _ := guardedHostInstance(t, ep, nil)
	inst.tracks = map[string]*track{} // only ls-spawned tracks in the registry

	tr := bareTrack(t, inst, host)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tr.ctx = ctx
	tr.cancel = cancel

	ls := newLoopState(inst)
	ls.armBoundaries(tr, host)
	require.Len(t, ls.watchers[tr.ID()], 1)

	before := trackIDSet(inst)

	ls.fireBoundary(t.Context(),
		trackEvent{kind: evBoundary, track: tr, node: beA})

	require.Error(t, ctx.Err(), "the guarded track is cancelled")
	require.NotContains(t, ls.watchers, tr.ID(), "the watch is torn down on fire")

	forked := newTrackIDs(before, inst)
	require.Len(t, forked, 1, "the exception flow spawns one continuation track")
	require.Equal(t, excEndA.ID(), ls.position[forked[0]].ID(),
		"the continuation starts on the boundary's exception target")

	// the continuation really runs now — drain its terminal event so its
	// goroutine exits (the loop is absent in this direct-drive test).
	drainUntilEnd(t, inst, forked[0])
}

// TestFireBoundaryRaceDropped (FR-8): a fire that arrives after the host already
// completed (its watch torn down) loses the race and is dropped — no cancel, no spawn.
func TestFireBoundaryRaceDropped(t *testing.T) {
	ep := &recordingProducer{}
	inst, host, beA, _, _ := guardedHostInstance(t, ep, nil)
	inst.tracks = map[string]*track{} // only ls-spawned tracks in the registry

	tr := bareTrack(t, inst, host)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tr.ctx = ctx
	tr.cancel = cancel

	ls := newLoopState(inst)
	ls.armBoundaries(tr, host)

	// the host completed first — its watch is gone before the fire is applied.
	ls.disarmBoundaries(tr.ID())

	before := trackIDSet(inst)
	ls.fireBoundary(t.Context(),
		trackEvent{kind: evBoundary, track: tr, node: beA})

	require.NoError(t, ctx.Err(), "a lost fire does not cancel the (completed) host")
	require.Empty(t, newTrackIDs(before, inst),
		"a lost fire spawns no exception flow")
}

// TestNonInterruptingBoundaryFires (T-7/T-8): a non-interrupting fire spawns a parallel
// token on the boundary's flow WITHOUT cancelling the host, keeps the watch armed so it
// can fire again (multi-shot), and is dropped once the host completes and disarms.
func TestNonInterruptingBoundaryFires(t *testing.T) {
	ep := &recordingProducer{}

	var (
		beN  flow.BoundaryEvent
		excN flow.Node
	)

	// host carries the default interrupting beA plus a non-interrupting beN -> excN.
	inst, host, _, _, _ := guardedHostInstance(t, ep,
		func(h flow.ActivityNode, p *process.Process) {
			sigN, err := events.NewSignal("sigN", nil)
			require.NoError(t, err)

			defN, err := events.NewSignalEventDefinition(sigN)
			require.NoError(t, err)

			b, err := events.NewBoundaryEvent("bndN", h, defN, false) // non-interrupting
			require.NoError(t, err)

			exc, err := events.NewEndEvent("excN")
			require.NoError(t, err)

			require.NoError(t, p.Add(b))
			require.NoError(t, p.Add(exc))
			_, err = flow.Link(b, exc)
			require.NoError(t, err)

			beN, excN = b, exc
		})

	inst.tracks = map[string]*track{} // only ls-spawned tracks in the registry

	tr := bareTrack(t, inst, host)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tr.ctx = ctx
	tr.cancel = cancel

	ls := newLoopState(inst)
	ls.armBoundaries(tr, host)
	require.Len(t, ls.watchers[tr.ID()], 2, "both boundaries arm")

	// fire fires beN and returns the continuation tracks it spawned; each
	// spawned continuation really runs, so its terminal event is drained.
	fire := func() []string {
		before := trackIDSet(inst)

		ls.fireBoundary(t.Context(),
			trackEvent{kind: evBoundary, track: tr, node: beN})

		forked := newTrackIDs(before, inst)
		for _, id := range forked {
			drainUntilEnd(t, inst, id)
		}

		return forked
	}

	forked := fire()
	require.NoError(t, ctx.Err(), "a non-interrupting fire does not cancel the host")
	require.Contains(t, ls.watchers, tr.ID(), "the watch stays armed (multi-shot)")
	require.Len(t, ls.watchers[tr.ID()], 2, "no watch is torn down on a non-interrupting fire")
	require.Len(t, forked, 1, "a parallel token is spawned on the boundary's flow")
	require.Equal(t, excN.ID(), inst.tracks[forked[0]].currentStep().node.ID())

	require.Len(t, fire(), 1, "a non-interrupting boundary fires again")
	require.NoError(t, ctx.Err())

	// once the host completes its window closes (disarm); a later fire is dropped.
	ls.disarmBoundaries(tr.ID())
	require.Empty(t, fire(), "a fire after the host completed is dropped")
}

// TestArmBoundaryRegisterFailureFaults: a boundary that cannot register can't honor its
// declared interruption, so arming faults the instance (lastErr set, stopAll called).
func TestArmBoundaryRegisterFailureFaults(t *testing.T) {
	ep := &recordingProducer{regErr: errRegRejected}
	inst, host, _, _, _ := guardedHostInstance(t, ep, nil)

	// stopAll walks the registry; the New-seeded tracks never went through
	// ls.spawn (no cancel func) — clear it.
	inst.tracks = map[string]*track{}

	tr := bareTrack(t, inst, host)
	ls := newLoopState(inst)

	ls.armBoundaries(tr, host)

	require.True(t, ls.stopping, "an arm failure stops the instance")
	require.Equal(t, Terminating, inst.State())
	require.Error(t, inst.LastErr(), "an arm failure is recorded as the instance error")
	require.NotContains(t, ls.watchers, tr.ID(), "no watch is armed on a failed registration")
}

// TestArmBoundariesSkipsNonActivity: a non-activity node (a plain event) carries no
// boundaries, so arming it is a no-op (covers the early return).
func TestArmBoundariesSkipsNonActivity(t *testing.T) {
	ep := &recordingProducer{}
	inst, _, _, excEndA, _ := guardedHostInstance(t, ep, nil)

	tr := bareTrack(t, inst, excEndA) // an end event, not an activity
	ls := newLoopState(inst)

	ls.armBoundaries(tr, excEndA)

	require.Empty(t, ls.watchers, "a non-activity node arms nothing")
	require.Empty(t, ep.registeredDefs())
}

// TestInterruptingBoundaryFireEndToEnd (T-4): a running ReceiveTask parks on its message;
// its interrupting signal boundary fires through the watch's own ProcessEvent (as the hub
// would); the host is cancelled and the instance completes via the exception flow.
func TestInterruptingBoundaryFireEndToEnd(t *testing.T) {
	ep := &recordingProducer{}
	inst, _, _, excEndA, sigDefA := guardedHostInstance(t, ep, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, inst.Run(ctx))

	// wait until the host is parked AND its boundary is armed.
	require.Eventually(t, func() bool {
		return ep.capturedWatch() != nil && parkedTrack(inst) != nil
	}, 2*time.Second, 5*time.Millisecond,
		"the host must park and arm its boundary")

	// fire the boundary exactly as the hub would on the signal.
	require.NoError(t,
		ep.capturedWatch().ProcessEvent(ctx, sigDefA))

	require.Eventually(t,
		func() bool { return inst.State() == Completed },
		2*time.Second, 5*time.Millisecond,
		"the instance completes via the exception flow after the interrupt")
	require.NoError(t, inst.LastErr())

	require.True(t, hasCanceledTrack(inst), "the guarded host track is cancelled")
	require.True(t, reachedNode(inst, excEndA.ID()),
		"a track ran the boundary's exception end event")
}

// parkedTrack returns a track waiting for an event, or nil.
func parkedTrack(inst *Instance) *track {
	snap := inst.tracksSnap.Load()
	if snap == nil {
		return nil
	}

	for _, tr := range *snap {
		if tr.inState(TrackWaitForEvent) {
			return tr
		}
	}

	return nil
}

func hasCanceledTrack(inst *Instance) bool {
	snap := inst.tracksSnap.Load()
	if snap == nil {
		return false
	}

	for _, tr := range *snap {
		if tr.inState(TrackCanceled) {
			return true
		}
	}

	return false
}

// reachedNode reports whether any track's recorded path visited the node id.
func reachedNode(inst *Instance, nodeID string) bool {
	snap := inst.tracksSnap.Load()
	if snap == nil {
		return false
	}

	for _, tr := range *snap {
		for _, step := range tr.path().Steps {
			if step.Node.ID() == nodeID {
				return true
			}
		}
	}

	return false
}
