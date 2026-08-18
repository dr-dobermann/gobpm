package thresher_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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
	"github.com/dr-dobermann/gobpm/pkg/repository/memrepo"
)

// TestSequentialMIReceiveTaskConsumesOnePerPass is #313's acceptance test
// (SRD-090.B T-2), and it could not be written before this slice: the model
// was refused at registration.
//
// A sequential Multi-Instance ReceiveTask over a two-item collection must
// consume TWO messages — one per pass — and complete after the second. The
// defect #313 names is that it completed after the FIRST: a wait is armed
// when a token ARRIVES at a node, an in-place iteration never re-arrives, so
// the second pass ran with no subscription at all and did not wait.
//
// The lane AFTER the activity is the assertion. It is reachable only once
// both passes have consumed a message, so a regression that lets a pass run
// through shows up as the flow finishing early rather than as a count nobody
// checks.
func TestSequentialMIReceiveTaskConsumesOnePerPass(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var passed atomic.Bool

	broker := membroker.New()

	mi, err := activities.NewMultiInstance(activities.WithSequential(),
		activities.WithInputCollection("items", "item"))
	require.NoError(t, err)

	p, err := process.New("mi-recv", foundation.WithID("mi-recv"),
		data.WithProperties(
			data.MustProperty("items",
				data.MustItemDefinition(values.NewArray("a", "b"),
					foundation.WithID("mi-recv-items")),
				data.ReadyDataState)))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start",
		foundation.WithID("mi-recv-start"))
	require.NoError(t, err)

	recv, err := activities.NewReceiveTask("await",
		bpmncommon.MustMessage("confirm", data.MustItemDefinition(
			values.NewVariable(""), foundation.WithID("mi-recv-confirm"))),
		activities.WithoutParams(), activities.WithLoop(mi),
		foundation.WithID("mi-recv-await"))
	require.NoError(t, err)

	lane := pinnedLane(t, "mi-recv-lane", &passed)

	end, err := events.NewEndEvent("end", foundation.WithID("mi-recv-end"))
	require.NoError(t, err)

	for _, e := range []flow.Element{start, recv, lane, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, recv)
	link(t, recv, lane)
	link(t, lane, end)

	th, _, cancel := msgEngine(t, "engine-MIR", memrepo.New(), broker, p)
	defer cancel()

	_, err = th.StartLatest(p.ID())
	require.NoError(t, err,
		"a sequential MI over a ReceiveTask builds — it was refused at "+
			"registration before SRD-090.B")

	ctx := context.Background()

	// the FIRST message: pass 0 consumes it, pass 1 arms and waits.
	require.NoError(t, broker.Publish(ctx, messaging.Envelope{
		Name: "confirm", Payload: "one"}))

	require.Never(t, passed.Load, 500*time.Millisecond, 20*time.Millisecond,
		"one message is not enough for two passes — this is the assertion "+
			"#313 was opened for")

	// the SECOND: pass 1 consumes it and the activity follows its outgoing
	// flow, once, however many passes ran.
	require.NoError(t, broker.Publish(ctx, messaging.Envelope{
		Name: "confirm", Payload: "two"}))

	require.Eventually(t, passed.Load, 5*time.Second, 10*time.Millisecond,
		"both passes consumed a message and the activity exited")
}
