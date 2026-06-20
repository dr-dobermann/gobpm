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
	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

// signalArmCatch builds an intermediate catch event on a freshly-named signal.
func signalArmCatch(t *testing.T, name string) *events.IntermediateCatchEvent {
	t.Helper()

	sig, err := events.NewSignal(name, nil)
	require.NoError(t, err)
	def, err := events.NewSignalEventDefinition(sig)
	require.NoError(t, err)
	ice, err := events.NewIntermediateCatchEvent("catch-"+name, def)
	require.NoError(t, err)

	return ice
}

// TestEventGatewaySignalDeferredChoice is the end-to-end deferred choice: a gate
// arms two signal catch events; throwing one signal routes the token down that arm
// and the instance completes through it — the loser arm never runs.
func TestEventGatewaySignalDeferredChoice(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	for _, tc := range []struct {
		name string
		fire string // signal name thrown
		want string // recordTask label expected to run
	}{
		{"first arm wins", "EGA", "a"},
		{"second arm wins", "EGB", "b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := make(chan string, 8)

			proc, err := process.New("eb-sig-" + tc.fire)
			require.NoError(t, err)

			start, err := events.NewStartEvent("start")
			require.NoError(t, err)
			gate, err := gateways.NewEventBasedGateway(
				gateways.WithDirection(gateways.Diverging))
			require.NoError(t, err)

			armA := signalArmCatch(t, "EGA")
			armB := signalArmCatch(t, "EGB")
			taskA := recordTask(t, "a", rec)
			taskB := recordTask(t, "b", rec)
			endA, err := events.NewEndEvent("endA")
			require.NoError(t, err)
			endB, err := events.NewEndEvent("endB")
			require.NoError(t, err)

			for _, e := range []flow.Element{
				start, gate, armA, armB, taskA, taskB, endA, endB,
			} {
				require.NoError(t, proc.Add(e))
			}

			link(t, start, gate)
			link(t, gate, armA)
			link(t, gate, armB)
			link(t, armA, taskA)
			link(t, armB, taskB)
			link(t, taskA, endA)
			link(t, taskB, endB)

			th, cancel := runEngine(t, proc)
			defer cancel()

			thrower := signalThrowProcess(t, "throw-"+tc.fire, tc.fire)
			require.NoError(t, th.RegisterProcess(thrower))

			h, err := th.StartProcess(proc.ID())
			require.NoError(t, err)

			time.Sleep(150 * time.Millisecond) // gate parks on both arms

			_, err = th.StartProcess(thrower.ID()) // throws tc.fire
			require.NoError(t, err)

			ctx, cc := context.WithTimeout(context.Background(), 3*time.Second)
			defer cc()
			st, err := h.WaitCompletion(ctx)
			require.NoError(t, err)
			require.Equal(t, thresher.StateCompleted, st)

			require.Equal(t, []string{tc.want}, drain(rec),
				"only the fired arm runs; the other is dropped")
		})
	}
}

// TestEventGatewayReceiveTaskArm covers a Receive-Task arm mixed with a signal arm
// (WithMixedArms): a published message routes the token down the Receive-Task arm
// end-to-end. Exercises the message delivery path the in-package M2 tests could not.
func TestEventGatewayReceiveTaskArm(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	rec := make(chan string, 8)
	broker := membroker.New()

	th, err := thresher.New("eb-recv", thresher.WithMessageBroker(broker))
	require.NoError(t, err)

	proc, err := process.New("eb-recv-proc")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	gate, err := gateways.NewEventBasedGateway(
		gateways.WithDirection(gateways.Diverging),
		gateways.WithMixedArms())
	require.NoError(t, err)

	recv, err := activities.NewReceiveTask("await-pay",
		bpmncommon.MustMessage("payment", data.MustItemDefinition(
			values.NewVariable(""), foundation.WithID("pay_in"))),
		activities.WithoutParams())
	require.NoError(t, err)

	cancelArm := signalArmCatch(t, "EG-CANCEL")
	paid := recordTask(t, "paid", rec)
	canceled := recordTask(t, "canceled", rec)
	endP, err := events.NewEndEvent("endP")
	require.NoError(t, err)
	endC, err := events.NewEndEvent("endC")
	require.NoError(t, err)

	for _, e := range []flow.Element{
		start, gate, recv, cancelArm, paid, canceled, endP, endC,
	} {
		require.NoError(t, proc.Add(e))
	}

	link(t, start, gate)
	link(t, gate, recv)
	link(t, gate, cancelArm)
	link(t, recv, paid)
	link(t, cancelArm, canceled)
	link(t, paid, endP)
	link(t, canceled, endC)

	require.NoError(t, th.RegisterProcess(proc))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))

	h, err := th.StartProcess(proc.ID())
	require.NoError(t, err)

	time.Sleep(150 * time.Millisecond) // gate parks on both arms

	require.NoError(t, broker.Publish(ctx, messaging.Envelope{
		Name: "payment", Payload: "PAID-1"}))

	wc, cc := context.WithTimeout(context.Background(), 3*time.Second)
	defer cc()
	st, err := h.WaitCompletion(wc)
	require.NoError(t, err)
	require.Equal(t, thresher.StateCompleted, st)

	require.Equal(t, []string{"paid"}, drain(rec),
		"the Receive-Task arm runs; the signal arm is dropped")
}
