package instance

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/internal/eventproc/eventhub"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/stretchr/testify/require"
)

// ebSignalArm builds a signal intermediate catch event (an Event-Based gateway arm)
// plus its end, and returns the catch node, the end node, and the signal definition to
// fire.
func ebSignalArm(
	t *testing.T, name string,
) (flow.Node, flow.Node, flow.EventDefinition) {
	t.Helper()

	sig, err := events.NewSignal(name, nil)
	require.NoError(t, err)

	def, err := events.NewSignalEventDefinition(sig)
	require.NoError(t, err)

	arm, err := events.NewIntermediateCatchEvent("arm-"+name, def)
	require.NoError(t, err)

	end, err := events.NewEndEvent("end-" + name)
	require.NoError(t, err)

	return arm, end, def
}

// ebGateProcess builds start → event-based gate → {arm-A → end-A, arm-B → end-B} where
// each arm is a signal catch event. It returns the process and the four nodes + two
// signal definitions so a test can fire one and assert which path ran.
func ebGateProcess(
	t *testing.T, procID, sigA, sigB string,
) (p *process.Process, nodes [4]flow.Node, defA, defB flow.EventDefinition) {
	t.Helper()

	p, err := process.New(procID)
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	gate, err := gateways.NewEventBasedGateway(
		gateways.WithDirection(gateways.Diverging))
	require.NoError(t, err)

	armA, endA, defA := ebSignalArm(t, sigA)
	armB, endB, defB := ebSignalArm(t, sigB)

	for _, e := range []flow.Element{start, gate, armA, endA, armB, endB} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, gate)
	link(t, gate, armA)
	link(t, armA, endA)
	link(t, gate, armB)
	link(t, armB, endB)

	return p, [4]flow.Node{armA, endA, armB, endB}, defA, defB
}

// registrationCounter wraps the live hub as the instance's parent producer and
// counts successful RegisterEvent calls. It exists because the token projection
// flips to WaitForEvent BEFORE the arms register at the hub (checkNodeType:
// updateState → evWaiting → RegisterEvent — the state must flip first for the
// synchronous-delivery race), so a readiness poll over tokens alone can fire
// into a not-yet-registered hub, where a signal is legitimately dropped
// (non-durable, no waiter → no-op). Waiting for the registrations closes that
// test-harness race deterministically.
type registrationCounter struct {
	inner *eventhub.EventHub
	n     atomic.Int32
}

func (rc *registrationCounter) RegisterEvent(
	p eventproc.EventProcessor, d flow.EventDefinition,
) error {
	if err := rc.inner.RegisterEvent(p, d); err != nil {
		return err
	}

	rc.n.Add(1)

	return nil
}

func (rc *registrationCounter) UnregisterEvent(
	p eventproc.EventProcessor, defID string,
) error {
	return rc.inner.UnregisterEvent(p, defID)
}

func (rc *registrationCounter) PropagateEvent(
	ctx context.Context, d flow.EventDefinition,
) error {
	return rc.inner.PropagateEvent(ctx, d)
}

// startEventGate snapshots p, starts a live EventHub, runs the instance, and waits
// until the gate is parked on its arms AND both arm waiters are registered at the
// hub, so a fire cannot be lost in the park-to-register window. It returns the
// instance + the hub so the test can PropagateEvent. (The hub's goroutines stop on
// ctx cancellation — graceful Shutdown is the deferred SRD-019 slice, so these
// tests don't goroutine-leak-check a still-live hub.)
func startEventGate(
	t *testing.T, ctx context.Context, p *process.Process,
) (*Instance, *eventhub.EventHub) {
	t.Helper()

	s, err := snapshot.New(p)
	require.NoError(t, err)

	rt := enginert.Default()

	eh, err := eventhub.New(rt)
	require.NoError(t, err)
	require.NoError(t, eh.Start(ctx))

	go func() { _ = eh.Run(ctx) }()

	rc := &registrationCounter{inner: eh}

	inst, err := New(s, scope.EmptyDataPath, rt, rc, nil)
	require.NoError(t, err)

	require.NoError(t, inst.Run(ctx))

	require.Eventually(t, func() bool {
		// both arms registered at the hub — a fire now reaches a live waiter…
		if rc.n.Load() < 2 {
			return false
		}

		// …and the gate's token is parked (the loop recorded the wait strictly
		// before the registrations could run — evWaiting precedes RegisterEvent).
		for _, tk := range inst.GetTokens() {
			if tk.State == TokenWaitForEvent {
				return true
			}
		}

		return false
	}, 2*time.Second, 5*time.Millisecond,
		"the gate must be waiting on its registered arms before we fire")

	return inst, eh
}

// visited collects every node id that appears in the instance's token history.
func visited(inst *Instance) map[string]bool {
	v := map[string]bool{}

	for _, tp := range inst.TokenHistory() {
		for _, sv := range tp.Steps {
			v[sv.Node.ID()] = true
		}
	}

	return v
}

func requireCompleted(t *testing.T, inst *Instance) {
	t.Helper()

	require.Eventually(t,
		func() bool { return inst.State() == Completed },
		2*time.Second, 5*time.Millisecond,
		"the instance must complete on the winning arm")
	require.NoError(t, inst.LastErr())
}

// TestAdvanceToArmUnknownEvent covers advanceToArm's defensive miss branch: a gate with
// no matching arm for the fired event appends no step (unreachable in the live flow —
// the gate's ProcessEvent validates the arm first).
func TestAdvanceToArmUnknownEvent(t *testing.T) {
	gate, err := gateways.NewEventBasedGateway(
		gateways.WithDirection(gateways.Diverging))
	require.NoError(t, err)

	sig, err := events.NewSignal("ORPHAN", nil)
	require.NoError(t, err)

	bogus, err := events.NewSignalEventDefinition(sig)
	require.NoError(t, err)

	tr := &track{}
	tr.advanceToArm(gate, gate, bogus) // gate has no arms → ArmFor miss → no step
	require.Empty(t, tr.steps)
}

// TestEventGatewayExclusiveFirstWins: firing arm A's signal routes the token down arm
// A; the instance completes via end-A and arm B (and end-B) never run — the gate
// dropped their subscriptions when it fired (SRD-024 §4.2).
func TestEventGatewayExclusiveFirstWins(t *testing.T) {
	_ = data.CreateDefaultStates()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p, nodes, defA, _ := ebGateProcess(t, "eb-first", "GO_A", "GO_B")
	armA, endA, armB, endB := nodes[0], nodes[1], nodes[2], nodes[3]

	inst, eh := startEventGate(t, ctx, p)
	require.NoError(t, eh.PropagateEvent(ctx, defA))

	requireCompleted(t, inst)

	v := visited(inst)
	require.True(t, v[armA.ID()], "arm A ran")
	require.True(t, v[endA.ID()], "end A reached")
	require.False(t, v[armB.ID()], "arm B must not run")
	require.False(t, v[endB.ID()], "end B must not run")
}

// TestEventGatewayExclusiveSecondWins is the symmetric case: firing arm B wins.
func TestEventGatewayExclusiveSecondWins(t *testing.T) {
	_ = data.CreateDefaultStates()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p, nodes, _, defB := ebGateProcess(t, "eb-second", "GO_A", "GO_B")
	armA, endA, armB, endB := nodes[0], nodes[1], nodes[2], nodes[3]

	inst, eh := startEventGate(t, ctx, p)
	require.NoError(t, eh.PropagateEvent(ctx, defB))

	requireCompleted(t, inst)

	v := visited(inst)
	require.True(t, v[armB.ID()], "arm B ran")
	require.True(t, v[endB.ID()], "end B reached")
	require.False(t, v[armA.ID()], "arm A must not run")
	require.False(t, v[endA.ID()], "end A must not run")
}

// TestEventGatewayConcurrentFires fires both arms' signals concurrently; exactly one
// path wins (the loop serializes the fires; the second finds the track already Ready
// and is a no-op) and the instance completes — exercised under -race.
func TestEventGatewayConcurrentFires(t *testing.T) {
	_ = data.CreateDefaultStates()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p, nodes, defA, defB := ebGateProcess(t, "eb-race", "GO_A", "GO_B")
	armA, endA, armB, endB := nodes[0], nodes[1], nodes[2], nodes[3]

	inst, eh := startEventGate(t, ctx, p)

	var wg sync.WaitGroup

	wg.Add(2)

	for _, d := range []flow.EventDefinition{defA, defB} {
		go func(def flow.EventDefinition) {
			defer wg.Done()

			_ = eh.PropagateEvent(ctx, def)
		}(d)
	}

	wg.Wait()
	requireCompleted(t, inst)

	v := visited(inst)
	ranA := v[armA.ID()] && v[endA.ID()]
	ranB := v[armB.ID()] && v[endB.ID()]
	require.True(t, ranA != ranB, "exactly one arm wins")
}

// TestEventGatewayConcurrentFiresStress is the FIX-007 canary: it loops the concurrent
// two-arm fire many times so the deferred-choice "exactly one arm wins" invariant is
// reliably exercised under -race. Pre-fix (the TOCTOU between the ProcessEvent state
// guard and the state transition) it reproduces the double-win within the loop; post-fix
// (the per-track eventMu serialization) every iteration wins exactly one arm.
func TestEventGatewayConcurrentFiresStress(t *testing.T) {
	_ = data.CreateDefaultStates()

	const iterations = 500

	for i := 0; i < iterations; i++ {
		func() {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			p, nodes, defA, defB := ebGateProcess(
				t, fmt.Sprintf("eb-stress-%d", i), "GO_A", "GO_B")
			armA, endA, armB, endB := nodes[0], nodes[1], nodes[2], nodes[3]

			inst, eh := startEventGate(t, ctx, p)

			var wg sync.WaitGroup

			wg.Add(2)

			for _, d := range []flow.EventDefinition{defA, defB} {
				go func(def flow.EventDefinition) {
					defer wg.Done()

					_ = eh.PropagateEvent(ctx, def)
				}(d)
			}

			wg.Wait()
			requireCompleted(t, inst)

			// Exactly one arm's full path (arm + end) ran. Polled, so a history
			// publish that lags the Completed flip settles rather than flaking; a
			// genuine double-win (both paths) or stuck token (neither) never
			// satisfies the XOR and times out.
			require.Eventuallyf(t, func() bool {
				v := visited(inst)
				ranA := v[armA.ID()] && v[endA.ID()]
				ranB := v[armB.ID()] && v[endB.ID()]

				return ranA != ranB
			}, 2*time.Second, 5*time.Millisecond,
				"iteration %d: exactly one arm must win", i)
		}()
	}
}
