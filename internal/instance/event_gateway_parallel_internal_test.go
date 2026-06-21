package instance

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/eventproc/eventhub"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/stretchr/testify/require"
)

// ebMsgArm builds a Message IntermediateCatchEvent arm (waiting for msg) and returns it
// together with its event definition, so a test can pass that definition to withBornEvent.
func ebMsgArm(
	t *testing.T, name, msg string,
) (*events.IntermediateCatchEvent, flow.EventDefinition) {
	t.Helper()

	def, err := events.NewMessageEventDefinition(
		bpmncommon.MustMessage(msg, data.MustItemDefinition(
			values.NewVariable(""), foundation.WithID(name+"_in"))), nil)
	require.NoError(t, err)

	ice, err := events.NewIntermediateCatchEvent(name, def)
	require.NoError(t, err)

	return ice, def
}

func ebPrintTask(t *testing.T, id string) *activities.ServiceTask {
	t.Helper()

	op, err := gooper.New(id,
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			return nil, nil
		})
	require.NoError(t, err)

	st, err := activities.NewServiceTask(id, op, activities.WithoutParams())
	require.NoError(t, err)

	return st
}

// parallelGateProc builds a process whose only start is a Parallel instantiating
// Event-Based gateway with two message arms, each arm → a service task → an end:
//
//	(parallel gate) ─┬→ armA → svcA → endA
//	                 └→ armB → svcB → endB
//
// It returns the process, the gate and the two arm nodes, and arm A's definition (the
// instantiating event for withBornEvent).
func parallelGateProc(t *testing.T) (
	p *process.Process, gate, armA, armB flow.Node, armADef flow.EventDefinition,
) {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New("eb-par-internal")
	require.NoError(t, err)

	g, err := gateways.NewEventBasedGateway(
		gateways.WithInstantiate(),
		gateways.WithEventGatewayType(gateways.ParallelEvents))
	require.NoError(t, err)

	aArm, aDef := ebMsgArm(t, "armA", "order A")
	bArm, _ := ebMsgArm(t, "armB", "order B")
	svcA := ebPrintTask(t, "svcA")
	svcB := ebPrintTask(t, "svcB")

	endA, err := events.NewEndEvent("endA")
	require.NoError(t, err)

	endB, err := events.NewEndEvent("endB")
	require.NoError(t, err)

	for _, e := range []flow.Element{g, aArm, bArm, svcA, svcB, endA, endB} {
		require.NoError(t, p.Add(e))
	}

	link(t, g, aArm)
	link(t, aArm, svcA)
	link(t, svcA, endA)
	link(t, g, bArm)
	link(t, bArm, svcB)
	link(t, svcB, endB)

	return p, g, aArm, bArm, aDef
}

// TestSeedParallelStartSeedsArms drives the Parallel-start born path in-package: New with
// withBornEvent on the gate runs createTracks -> seedParallelStart, which must pre-fire
// the instantiating arm (a track onto its continuation) and re-arm the other arm (a
// waiting track at the arm node) — SRD-025 §4.3.
func TestSeedParallelStartSeedsArms(t *testing.T) {
	p, gate, armA, armB, armADef := parallelGateProc(t)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	// the re-armed arm registers its waiter during New, so a live EventHub is needed.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rt := enginert.Default()

	eh, err := eventhub.New(rt)
	require.NoError(t, err)
	require.NoError(t, eh.Start(ctx))

	go func() { _ = eh.Run(ctx) }()

	inst, err := New(s, scope.EmptyDataPath, rt, eh, nil,
		withBornEvent(gate.ID(), armADef))
	require.NoError(t, err)

	// arm A's continuation (its outgoing target) is pre-fired; arm B re-arms AT its node.
	preFired := armA.Outgoing()[0].Target().Node().ID()

	var onPreFired, atArmB bool

	for _, tk := range inst.tracks {
		switch tk.steps[0].node.ID() {
		case preFired:
			onPreFired = true
		case armB.ID():
			atArmB = true
		}
	}

	require.True(t, onPreFired, "the instantiating arm is pre-fired onto its continuation")
	require.True(t, atArmB, "the other arm re-arms as a waiting track")
}

// TestSeedParallelStartNoArm covers the firing-arm guard: a born event whose message
// matches no arm cannot resolve a firing arm, so the seed fails loudly.
func TestSeedParallelStartNoArm(t *testing.T) {
	p, gate, _, _, _ := parallelGateProc(t)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(),
		mockeventproc.NewMockEventProducer(t), nil)
	require.NoError(t, err)

	alien, err := events.NewMessageEventDefinition(
		bpmncommon.MustMessage("nope", data.MustItemDefinition(
			values.NewVariable(""), foundation.WithID("nope_in"))), nil)
	require.NoError(t, err)

	require.ErrorContains(t,
		inst.seedParallelStart(inst.s.Nodes[gate.ID()], alien),
		"has no arm for the instantiating event")
}

// TestSeedParallelStartNonRouter covers the router type-assert guard: a born start that
// does not resolve arms (here a catch event, not the gate) is rejected.
func TestSeedParallelStartNonRouter(t *testing.T) {
	p, _, armA, _, _ := parallelGateProc(t)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(),
		mockeventproc.NewMockEventProducer(t), nil)
	require.NoError(t, err)

	require.ErrorContains(t,
		inst.seedParallelStart(inst.s.Nodes[armA.ID()], nil),
		"does not resolve arms")
}
