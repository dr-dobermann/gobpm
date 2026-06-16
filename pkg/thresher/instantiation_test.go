package thresher_test

import (
	"context"
	"fmt"
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
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

// msgStartConfirmProcess builds "message-start -> confirm(service) -> end". The
// confirm service reads the bound payload (order_in) and pushes it to done.
func msgStartConfirmProcess(
	t *testing.T, done chan<- string,
) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	proc, err := process.New("instantiated")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start",
		events.WithMessageTrigger(
			events.MustMessageEventDefinition(
				bpmncommon.MustMessage("order placed",
					data.MustItemDefinition(values.NewVariable(""),
						foundation.WithID("order_in"))),
				nil)))
	require.NoError(t, err)

	confirmOp, err := gooper.New("confirm-op",
		func(ctx context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			got, err := r.GetDataByID("order_in")
			if err != nil {
				return nil, fmt.Errorf("read order_in: %w", err)
			}

			done <- fmt.Sprintf("%v", got.Value().Get(ctx))

			return nil, nil
		})
	require.NoError(t, err)

	confirm, err := activities.NewServiceTask("confirm", confirmOp,
		activities.WithoutParams())
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, confirm, end} {
		require.NoError(t, proc.Add(e))
	}

	for _, l := range [][2]flow.Element{{start, confirm}, {confirm, end}} {
		_, err := flow.Link(
			l[0].(flow.SequenceSource), l[1].(flow.SequenceTarget))
		require.NoError(t, err)
	}

	return proc
}

// TestEventTriggeredInstantiation is SRD-015 V3 end-to-end: an auto-registered
// process with a message start event spawns a NEW instance when a matching
// message is published; the born instance runs from the start's outgoing flow
// and observes the payload.
func TestEventTriggeredInstantiation(t *testing.T) {
	broker := membroker.New()

	th, err := thresher.New("e2e", thresher.WithMessageBroker(broker))
	require.NoError(t, err)

	done := make(chan string, 1)
	require.NoError(t, th.RegisterProcess(msgStartConfirmProcess(t, done)))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))

	require.NoError(t, broker.Publish(ctx,
		messaging.Envelope{Name: "order placed", Payload: "ORD-42"}))

	select {
	case got := <-done:
		require.Equal(t, "ORD-42", got)
	case <-time.After(3 * time.Second):
		t.Fatal("a published message did not instantiate the process")
	}
}

// recvTaskConfirmProcess builds "instantiate-ReceiveTask -> confirm(service) ->
// end": a no-incoming instantiate ReceiveTask is the entry, and confirm reads
// the bound payload (order_in) and pushes it to done.
func recvTaskConfirmProcess(t *testing.T, done chan<- string) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	proc, err := process.New("recv-instantiated")
	require.NoError(t, err)

	recv, err := activities.NewReceiveTask("await-order",
		bpmncommon.MustMessage("order placed",
			data.MustItemDefinition(values.NewVariable(""),
				foundation.WithID("order_in"))),
		activities.WithoutParams(), activities.WithInstantiate())
	require.NoError(t, err)

	confirmOp, err := gooper.New("confirm-op",
		func(ctx context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			got, err := r.GetDataByID("order_in")
			if err != nil {
				return nil, fmt.Errorf("read order_in: %w", err)
			}

			done <- fmt.Sprintf("%v", got.Value().Get(ctx))

			return nil, nil
		})
	require.NoError(t, err)

	confirm, err := activities.NewServiceTask("confirm", confirmOp,
		activities.WithoutParams())
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{recv, confirm, end} {
		require.NoError(t, proc.Add(e))
	}

	for _, l := range [][2]flow.Element{{recv, confirm}, {confirm, end}} {
		_, err := flow.Link(
			l[0].(flow.SequenceSource), l[1].(flow.SequenceTarget))
		require.NoError(t, err)
	}

	return proc
}

// TestInstantiateReceiveTask is SRD-015 V4: a no-incoming instantiate
// ReceiveTask spawns a new instance when a matching message is published; the
// born instance runs from the receiver's outgoing flow and observes the payload.
func TestInstantiateReceiveTask(t *testing.T) {
	broker := membroker.New()

	th, err := thresher.New("recv-e2e", thresher.WithMessageBroker(broker))
	require.NoError(t, err)

	done := make(chan string, 1)
	require.NoError(t, th.RegisterProcess(recvTaskConfirmProcess(t, done)))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))

	require.NoError(t, broker.Publish(ctx,
		messaging.Envelope{Name: "order placed", Payload: "ORD-77"}))

	select {
	case got := <-done:
		require.Equal(t, "ORD-77", got)
	case <-time.After(3 * time.Second):
		t.Fatal("a published message did not instantiate via the ReceiveTask")
	}
}

// TestManualStartNotInstantiated is SRD-015 FR-9: a WithManualStart process is
// NOT auto-instantiated by a published message; it runs only via StartProcess,
// where its message-start node waits as an in-instance catch until the message
// arrives.
func TestManualStartNotInstantiated(t *testing.T) {
	broker := membroker.New()

	th, err := thresher.New("manual-e2e", thresher.WithMessageBroker(broker))
	require.NoError(t, err)

	done := make(chan string, 1)
	proc := msgStartConfirmProcess(t, done)
	require.NoError(t, th.RegisterProcess(proc, thresher.WithManualStart()))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))

	// A published message must NOT spawn an instance (no persistent starter).
	// The broker buffers it (pull-on-subscribe) for a future subscriber.
	require.NoError(t, broker.Publish(ctx,
		messaging.Envelope{Name: "order placed", Payload: "ORD-1"}))

	select {
	case <-done:
		t.Fatal("manual-start process must not be auto-instantiated")
	case <-time.After(300 * time.Millisecond):
	}

	// Started explicitly, the instance's message-start node waits as an
	// in-instance catch; on subscribe it drains the buffered message and
	// completes — proving manual = start-as-catch, StartProcess-driven.
	require.NoError(t, th.StartProcess(proc.ID()))

	select {
	case got := <-done:
		require.Equal(t, "ORD-1", got)
	case <-time.After(3 * time.Second):
		t.Fatal("manually started instance did not consume the message")
	}
}
