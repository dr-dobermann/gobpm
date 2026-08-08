package thresher_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// SRD-085 M1 (ADR-006 v.5 §2.9.1/§2.9.2): one signal broadcast wakes
// EVERY parallel-MI iteration waiting at the SHARED catch node — the
// hub fans out to each iteration's own track processor, and with the
// node-resident payload slot gone each delivery is captured on its
// receiving track (raced writes to the shared node made exactly this
// scenario a -race failure before). The frame-carried BIND itself is
// pinned at unit level (catchevent_internal_test, the ReceiveTask
// suite) and end-to-end by the message-path suites; a signal catch
// declares no payload output, so nothing lands in scope here.
func TestParallelMISignalPayloadPerDelivery(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	const key = "dp-mi"

	sigItem := data.MustItemDefinition(values.NewVariable(""),
		foundation.WithID("sig_item"))

	sig, err := events.NewSignal("dp-go", sigItem)
	require.NoError(t, err)

	p, err := process.New(key, foundation.WithID(key),
		data.WithProperties(
			data.MustProperty("items",
				data.MustItemDefinition(values.NewArray("a", "b"),
					foundation.WithID("items")),
				data.ReadyDataState)))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start",
		foundation.WithID(key+"-start"))
	require.NoError(t, err)

	mi, err := activities.NewMultiInstance(
		activities.WithInputCollection("items", "item"))
	require.NoError(t, err)

	body, err := activities.NewSubProcess("body",
		activities.WithLoop(mi), foundation.WithID(key+"-body"))
	require.NoError(t, err)

	sDef, err := events.NewSignalEventDefinition(sig,
		foundation.WithID(key+"-sdef"))
	require.NoError(t, err)

	bStart, err := events.NewStartEvent("b-start",
		foundation.WithID(key+"-b-start"))
	require.NoError(t, err)

	catch, err := events.NewIntermediateCatchEvent("wait", sDef,
		foundation.WithID(key+"-catch"))
	require.NoError(t, err)

	bEnd, err := events.NewEndEvent("b-end",
		foundation.WithID(key+"-b-end"))
	require.NoError(t, err)

	for _, e := range []flow.Element{bStart, catch, bEnd} {
		require.NoError(t, body.Add(e))
	}

	link(t, bStart, catch)
	link(t, catch, bEnd)

	end, err := events.NewEndEvent("end", foundation.WithID(key+"-end"))
	require.NoError(t, err)

	for _, e := range []flow.Element{start, body, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, body)
	link(t, body, end)

	th, cancel := runEngine(t, p)
	defer cancel()

	h, err := th.StartLatest(p.ID())
	require.NoError(t, err)

	// both iterations must be parked at the shared catch before the
	// broadcast, or a late registration misses it (subscribe-before-
	// publish, ADR-006 §2.4).
	time.Sleep(200 * time.Millisecond)

	firedSig, err := events.NewSignal("dp-go",
		data.MustItemDefinition(values.NewVariable("PAY-7"),
			foundation.WithID("sig_item")))
	require.NoError(t, err)

	fired, err := events.NewSignalEventDefinition(firedSig)
	require.NoError(t, err)

	require.NoError(t, th.PropagateEvent(context.Background(), fired))

	ctx, cc := context.WithTimeout(context.Background(), 3*time.Second)
	defer cc()

	st, err := h.WaitCompletion(ctx)
	require.NoError(t, err)
	require.Equal(t, thresher.StateCompleted, st,
		"ONE broadcast must wake BOTH iterations at the shared catch")
}
