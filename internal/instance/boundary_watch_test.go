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
	mu     sync.Mutex
	watch  *boundaryWatch
	regIDs []string
	unreg  []string
	regErr error
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

	return nil
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

// TestArmDisarmBoundaryWatch (T-12): arming registers one watch for the interrupting
// boundary (the non-interrupting one is skipped — M3c), the watch carries a unique
// (host, boundary, def) identity, and disarming unregisters it and clears the entry.
func TestArmDisarmBoundaryWatch(t *testing.T) {
	ep := &recordingProducer{}

	// host carries an interrupting signal-A boundary plus a non-interrupting signal-B one.
	inst, host, beA, _, sigDefA := guardedHostInstance(t, ep,
		func(h flow.ActivityNode, p *process.Process) {
			sigB, err := events.NewSignal("sigB", nil)
			require.NoError(t, err)

			defB, err := events.NewSignalEventDefinition(sigB)
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
	watchers := map[string][]*boundaryWatch{}

	inst.armBoundaries(tr, host, watchers, func() {})

	require.Len(t, watchers[tr.ID()], 1, "only the interrupting boundary is armed")
	require.Equal(t, []string{sigDefA.ID()}, ep.registeredDefs(),
		"the non-interrupting boundary is not registered (M3c)")

	w := watchers[tr.ID()][0]
	require.Equal(t, beA.ID(), w.boundary.ID())
	require.Equal(t,
		"boundary-watch:"+tr.ID()+":"+beA.ID()+":"+sigDefA.ID(), w.ID(),
		"the watch identity is unique per (host, boundary, def)")

	inst.disarmBoundaries(tr.ID(), watchers)

	require.NotContains(t, watchers, tr.ID(), "disarm clears the track's entry")
	require.Equal(t, []string{sigDefA.ID()}, ep.unregisteredDefs(),
		"disarm unregisters the armed watch")
}

// TestFireBoundaryInterrupts (T-4 core): an interrupting fire cancels the guarded track,
// continues the instance on the boundary's exception flow, and tears the watch down.
func TestFireBoundaryInterrupts(t *testing.T) {
	ep := &recordingProducer{}
	inst, host, beA, excEndA, _ := guardedHostInstance(t, ep, nil)

	tr := bareTrack(t, inst, host)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tr.ctx = ctx
	tr.cancel = cancel

	watchers := map[string][]*boundaryWatch{}
	inst.armBoundaries(tr, host, watchers, func() {})
	require.Len(t, watchers[tr.ID()], 1)

	var spawned []*track
	spawn := func(nt *track) { spawned = append(spawned, nt) }

	inst.fireBoundary(
		trackEvent{kind: evBoundary, track: tr, node: beA},
		watchers, spawn, func() {}, false)

	require.Error(t, ctx.Err(), "the guarded track is cancelled")
	require.NotContains(t, watchers, tr.ID(), "the watch is torn down on fire")

	require.Len(t, spawned, 1, "the exception flow spawns one continuation track")
	require.Equal(t, excEndA.ID(), spawned[0].currentStep().node.ID(),
		"the continuation starts on the boundary's exception target")
}

// TestFireBoundaryRaceDropped (FR-8): a fire that arrives after the host already
// completed (its watch torn down) loses the race and is dropped — no cancel, no spawn.
func TestFireBoundaryRaceDropped(t *testing.T) {
	ep := &recordingProducer{}
	inst, host, beA, _, _ := guardedHostInstance(t, ep, nil)

	tr := bareTrack(t, inst, host)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tr.ctx = ctx
	tr.cancel = cancel

	watchers := map[string][]*boundaryWatch{}
	inst.armBoundaries(tr, host, watchers, func() {})

	// the host completed first — its watch is gone before the fire is applied.
	inst.disarmBoundaries(tr.ID(), watchers)

	var spawned []*track
	inst.fireBoundary(
		trackEvent{kind: evBoundary, track: tr, node: beA},
		watchers, func(nt *track) { spawned = append(spawned, nt) },
		func() {}, false)

	require.NoError(t, ctx.Err(), "a lost fire does not cancel the (completed) host")
	require.Empty(t, spawned, "a lost fire spawns no exception flow")
}

// TestArmBoundaryRegisterFailureFaults: a boundary that cannot register can't honor its
// declared interruption, so arming faults the instance (lastErr set, stopAll called).
func TestArmBoundaryRegisterFailureFaults(t *testing.T) {
	ep := &recordingProducer{regErr: errRegRejected}
	inst, host, _, _, _ := guardedHostInstance(t, ep, nil)

	tr := bareTrack(t, inst, host)
	watchers := map[string][]*boundaryWatch{}

	stopCalled := false
	inst.armBoundaries(tr, host, watchers, func() { stopCalled = true })

	require.True(t, stopCalled, "an arm failure stops the instance")
	require.Error(t, inst.LastErr(), "an arm failure is recorded as the instance error")
	require.NotContains(t, watchers, tr.ID(), "no watch is armed on a failed registration")
}

// TestArmBoundariesSkipsNonActivity: a non-activity node (a plain event) carries no
// boundaries, so arming it is a no-op (covers the early return).
func TestArmBoundariesSkipsNonActivity(t *testing.T) {
	ep := &recordingProducer{}
	inst, _, _, excEndA, _ := guardedHostInstance(t, ep, nil)

	tr := bareTrack(t, inst, excEndA) // an end event, not an activity
	watchers := map[string][]*boundaryWatch{}

	inst.armBoundaries(tr, excEndA, watchers, func() {})

	require.Empty(t, watchers, "a non-activity node arms nothing")
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
