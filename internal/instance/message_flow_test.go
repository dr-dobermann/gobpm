package instance

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/eventproc"
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
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/stretchr/testify/require"
)

// failEventProducer rejects every registration. It is a plain struct (not a
// reflect-based mock) so it never captures the track argument — passing a
// mutex-bearing track to a mock whose matcher reflects over it would race the
// live track (see the RemoveWaiter doc on eventhub.EventHub).
type failEventProducer struct{}

func (failEventProducer) RegisterEvent(
	eventproc.EventProcessor, flow.EventDefinition,
) error {
	return fmt.Errorf("registration rejected")
}

func (failEventProducer) UnregisterEvent(
	eventproc.EventProcessor, string,
) error {
	return nil
}

func (failEventProducer) PropagateEvent(
	context.Context, flow.EventDefinition,
) error {
	return nil
}

// TestSendReceiveMidFlow is SRD-013 V4: a SendTask publishes a message that a
// mid-flow ReceiveTask waits for and binds. It exercises the full task half of
// ADR-014 — SendTask -> broker -> MessageWaiter -> track.ProcessEvent ->
// ReceiveTask.ProcessEvent -> resume -> Exec -> scope — and the mid-flow event
// registration (checkFlows -> checkNodeType): the receive has an incoming flow,
// so it is not a start node and must register its event only when the token
// reaches it.
//
//	start -> send-order -> receive-order -> end
func TestSendReceiveMidFlow(t *testing.T) {
	_ = data.CreateDefaultStates()

	p, err := process.New("msg-flow",
		data.WithProperties(
			data.MustProperty("order_out",
				data.MustItemDefinition(values.NewVariable("ORD-7"),
					foundation.WithID("order_out")),
				data.ReadyDataState)))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	send, err := activities.NewSendTask("send-order",
		bpmncommon.MustMessage("order placed",
			data.MustItemDefinition(values.NewVariable(""),
				foundation.WithID("order_out"))),
		activities.WithoutParams())
	require.NoError(t, err)

	outParam := data.MustParameter("received order",
		data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable(""),
				foundation.WithID("order_in")),
			data.UnavailableDataState))

	receive, err := activities.NewReceiveTask("receive-order",
		bpmncommon.MustMessage("order placed",
			data.MustItemDefinition(values.NewVariable(""),
				foundation.WithID("order_in"))),
		activities.WithParameters(data.Output, outParam))
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, send, receive, end} {
		require.NoError(t, p.Add(e))
	}

	for _, l := range [][2]flow.Element{
		{start, send}, {send, receive}, {receive, end},
	} {
		_, err := flow.Link(l[0].(flow.SequenceSource), l[1].(flow.SequenceTarget))
		require.NoError(t, err)
	}

	s, err := snapshot.New(p)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rt := enginert.Default()

	eh, err := eventhub.New(rt)
	require.NoError(t, err)
	require.NoError(t, eh.Start(ctx))

	go func() { _ = eh.Run(ctx) }()

	inst, err := New(s, scope.EmptyDataPath, rt, eh, nil)
	require.NoError(t, err)

	require.NoError(t, inst.Run(ctx))

	require.Eventually(t,
		func() bool { return inst.State() == Completed },
		2*time.Second, 5*time.Millisecond,
		"the instance must complete the send/receive round-trip")
	require.NoError(t, inst.LastErr())

	// the received payload reached the container scope on the receive's commit.
	d, err := inst.dataPlane.GetDataByID(inst.rootScope, "order_in")
	require.NoError(t, err)
	require.Equal(t, "ORD-7", d.Value().Get(ctx))
}

// TestMidFlowEventRegistrationFailureFailsTrack covers the error path of
// mid-flow event registration: when registering an intermediate event node's
// event fails, checkNodeType (via checkFlows) propagates the error and the
// track fails rather than running the node unregistered.
//
//	start -> receive (registration fails) -> end
func TestMidFlowEventRegistrationFailureFailsTrack(t *testing.T) {
	_ = data.CreateDefaultStates()

	p, err := process.New("msg-flow-fail")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	receive, err := activities.NewReceiveTask("receive-order",
		bpmncommon.MustMessage("order placed",
			data.MustItemDefinition(values.NewVariable(""),
				foundation.WithID("order_in"))),
		activities.WithoutParams())
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, receive, end} {
		require.NoError(t, p.Add(e))
	}

	for _, l := range [][2]flow.Element{{start, receive}, {receive, end}} {
		_, err := flow.Link(l[0].(flow.SequenceSource), l[1].(flow.SequenceTarget))
		require.NoError(t, err)
	}

	s, err := snapshot.New(p)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// the EventProducer rejects the mid-flow registration.
	inst, err := New(s, scope.EmptyDataPath, enginert.Default(),
		failEventProducer{}, nil)
	require.NoError(t, err)

	require.NoError(t, inst.Run(ctx))

	require.Eventually(t, func() bool {
		snap := inst.tracksSnap.Load()
		if snap == nil {
			return false
		}

		for _, tr := range *snap {
			if tr.inState(TrackFailed) {
				return true
			}
		}

		return false
	}, 2*time.Second, 5*time.Millisecond,
		"the failed mid-flow registration must fail the track")
}

// TestSendToIntermediateCatchEvent is SRD-014 V4: a SendTask publishes a
// message that a mid-flow IntermediateCatchEvent waits for and binds into scope
// (the event-shaped peer of ReceiveTask; closes WS-C3).
//
//	start -> send-order -> catch-order -> end
func TestSendToIntermediateCatchEvent(t *testing.T) {
	_ = data.CreateDefaultStates()

	p, err := process.New("msg-event-flow",
		data.WithProperties(
			data.MustProperty("order_out",
				data.MustItemDefinition(values.NewVariable("ORD-7"),
					foundation.WithID("order_out")),
				data.ReadyDataState)))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	send, err := activities.NewSendTask("send-order",
		bpmncommon.MustMessage("order placed",
			data.MustItemDefinition(values.NewVariable(""),
				foundation.WithID("order_out"))),
		activities.WithoutParams())
	require.NoError(t, err)

	catch, err := events.NewIntermediateCatchEvent("catch-order",
		events.MustMessageEventDefinition(
			bpmncommon.MustMessage("order placed",
				data.MustItemDefinition(values.NewVariable(""),
					foundation.WithID("order_in"))),
			nil))
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, send, catch, end} {
		require.NoError(t, p.Add(e))
	}

	for _, l := range [][2]flow.Element{
		{start, send}, {send, catch}, {catch, end},
	} {
		_, err := flow.Link(l[0].(flow.SequenceSource), l[1].(flow.SequenceTarget))
		require.NoError(t, err)
	}

	s, err := snapshot.New(p)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rt := enginert.Default()

	eh, err := eventhub.New(rt)
	require.NoError(t, err)
	require.NoError(t, eh.Start(ctx))

	go func() { _ = eh.Run(ctx) }()

	inst, err := New(s, scope.EmptyDataPath, rt, eh, nil)
	require.NoError(t, err)

	require.NoError(t, inst.Run(ctx))

	require.Eventually(t,
		func() bool { return inst.State() == Completed },
		2*time.Second, 5*time.Millisecond,
		"the send/intermediate-catch round-trip must complete")
	require.NoError(t, inst.LastErr())

	d, err := inst.dataPlane.GetDataByID(inst.rootScope, "order_in")
	require.NoError(t, err)
	require.Equal(t, "ORD-7", d.Value().Get(ctx))
}
