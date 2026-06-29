package thresher_test

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/messaging"
	"github.com/dr-dobermann/gobpm/pkg/messaging/membroker"
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
	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

// ebMsgArm builds a Message IntermediateCatchEvent arm named name, waiting for msgName.
func ebMsgArm(
	t *testing.T, name, msgName string,
) *events.IntermediateCatchEvent {
	t.Helper()

	def := events.MustMessageEventDefinition(
		bpmncommon.MustMessage(msgName,
			data.MustItemDefinition(values.NewVariable(""),
				foundation.WithID(name+"_in"))),
		nil)

	ice, err := events.NewIntermediateCatchEvent(name, def)
	require.NoError(t, err)

	return ice
}

// ebMarkerService builds a ServiceTask that pushes marker onto done when it runs —
// a probe for "which arm's path executed".
func ebMarkerService(
	t *testing.T, name, marker string, done chan<- string,
) *activities.ServiceTask {
	t.Helper()

	op, err := gooper.New(name+"-op",
		func(
			_ context.Context, _ service.DataReader, _ *data.ItemDefinition,
		) (*data.ItemDefinition, error) {
			done <- marker

			return nil, nil
		})
	require.NoError(t, err)

	st, err := activities.NewServiceTask(name, op, activities.WithoutParams())
	require.NoError(t, err)

	return st
}

// instExclusiveGateProcess builds a process started by an instantiating Exclusive
// Event-Based gateway (no incoming, WithInstantiate) with two message arms, each
// arm → a marker ServiceTask → end (SRD-025 §4.2):
//
//	(instantiate gate) ─┬→ catch("order A") → confirmA[A] → endA
//	                    └→ catch("order B") → confirmB[B] → endB
func instExclusiveGateProcess(
	t *testing.T, done chan<- string,
) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	proc, err := process.New("inst-eb-exclusive")
	require.NoError(t, err)

	gate, err := gateways.NewEventBasedGateway(gateways.WithInstantiate())
	require.NoError(t, err)

	armA := ebMsgArm(t, "armA", "order A")
	armB := ebMsgArm(t, "armB", "order B")
	confA := ebMarkerService(t, "confirmA", "A", done)
	confB := ebMarkerService(t, "confirmB", "B", done)

	endA, err := events.NewEndEvent("endA")
	require.NoError(t, err)

	endB, err := events.NewEndEvent("endB")
	require.NoError(t, err)

	for _, e := range []flow.Element{
		gate, armA, armB, confA, confB, endA, endB,
	} {
		require.NoError(t, proc.Add(e))
	}

	for _, l := range [][2]flow.Element{
		{gate, armA}, {armA, confA}, {confA, endA},
		{gate, armB}, {armB, confB}, {confB, endB},
	} {
		_, err := flow.Link(
			l[0].(flow.SequenceSource), l[1].(flow.SequenceTarget))
		require.NoError(t, err)
	}

	return proc
}

// TestEventGatewayExclusiveStart: a process whose only start is an instantiating
// Exclusive Event-Based gateway. Publishing one arm's message spawns a new instance
// that runs THAT arm's path; the other arm never runs (born from the firing arm,
// SRD-025 §4.2).
func TestEventGatewayExclusiveStart(t *testing.T) {
	broker := membroker.New()

	th, err := thresher.New("eb-excl-start", thresher.WithMessageBroker(broker))
	require.NoError(t, err)

	done := make(chan string, 4)
	_, err = th.RegisterProcess(instExclusiveGateProcess(t, done))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))

	require.NoError(t, broker.Publish(ctx,
		messaging.Envelope{Name: "order A", Payload: "ORD-42"}))

	select {
	case got := <-done:
		require.Equal(t, "A", got, "the firing arm's path runs")
	case <-time.After(3 * time.Second):
		t.Fatal("publishing arm A did not instantiate and run arm A")
	}

	// Arm B never fired → its path must not run (Exclusive: born from arm A only).
	select {
	case got := <-done:
		t.Fatalf("unexpected extra run %q — only arm A should run", got)
	case <-time.After(300 * time.Millisecond):
	}
}

// TestEventGatewayExclusiveStartEachEventNewInstance: each arm event spawns its own
// instance — the gate is a multi-alternative instantiator (BPMN §10.5.6).
func TestEventGatewayExclusiveStartEachEventNewInstance(t *testing.T) {
	broker := membroker.New()

	th, err := thresher.New("eb-excl-multi", thresher.WithMessageBroker(broker))
	require.NoError(t, err)

	done := make(chan string, 4)
	_, err = th.RegisterProcess(instExclusiveGateProcess(t, done))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))

	require.NoError(t, broker.Publish(ctx,
		messaging.Envelope{Name: "order A", Payload: "ORD-1"}))
	require.NoError(t, broker.Publish(ctx,
		messaging.Envelope{Name: "order B", Payload: "ORD-2"}))

	got := map[string]bool{}

	for range 2 {
		select {
		case m := <-done:
			got[m] = true
		case <-time.After(3 * time.Second):
			t.Fatalf("expected two instances (one per event), got %v", got)
		}
	}

	require.True(t, got["A"] && got["B"],
		"each event spawned its own instance running its arm: %v", got)
}
