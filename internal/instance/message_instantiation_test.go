package instance

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/eventproc/eventhub"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/stretchr/testify/require"
)

// msgStartSnapshot builds a "message-start -> end" process and returns its
// snapshot, the start node, and a fired event definition carrying the payload
// (as the message waiter's fireDefinition would build it).
func msgStartSnapshot(
	t *testing.T, payload string,
) (*snapshot.Snapshot, flow.Node, flow.EventDefinition) {
	t.Helper()

	_ = data.CreateDefaultStates()

	med := events.MustMessageEventDefinition(
		bpmncommon.MustMessage("order placed",
			data.MustItemDefinition(values.NewVariable(""),
				foundation.WithID("order_in"))),
		nil)

	p, err := process.New("born-flow")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start", events.WithMessageTrigger(med))
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	require.NoError(t, p.Add(start))
	require.NoError(t, p.Add(end))
	_, err = flow.Link(start, end)
	require.NoError(t, err)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	item := med.Message().Item()
	datum := data.MustParameter(item.ID(),
		data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable(payload),
				foundation.WithID(item.ID())),
			data.ReadyDataState))

	firedDef, err := med.CloneEvent([]data.Data{datum})
	require.NoError(t, err)

	return s, start, firedDef
}

// TestNewFromEventBornInstance is SRD-015 V3: an instance born from a message
// start event runs from the start node's outgoing flow (the start node is NOT
// parked as a waiter) with the payload bound into the root scope. No message is
// published after construction — the instance completes purely because the start
// node was treated as already fired.
func TestNewFromEventBornInstance(t *testing.T) {
	s, start, firedDef := msgStartSnapshot(t, "ORD-99")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rt := enginert.Default()

	eh, err := eventhub.New(rt)
	require.NoError(t, err)
	require.NoError(t, eh.Start(ctx))

	go func() { _ = eh.Run(ctx) }()

	inst, err := NewFromEvent(
		s, scope.EmptyDataPath, rt, eh, nil, start.ID(), firedDef, "", "")
	require.NoError(t, err)

	require.NoError(t, inst.Run(ctx))

	require.Eventually(t,
		func() bool { return inst.State() == Completed },
		2*time.Second, 5*time.Millisecond,
		"the born-from-event instance must run from the start's outgoing flow")
	require.NoError(t, inst.LastErr())

	// the payload was bound into the root scope under the message item id.
	d, err := inst.dataPlane.GetDataByID(inst.rootScope, "order_in")
	require.NoError(t, err)
	require.Equal(t, "ORD-99", d.Value().Get(ctx))
}

// TestNewFromEventSeedError covers the createTracks/seedTracks error path: when
// the born start's outgoing target is an event node whose registration fails,
// seeding the initial track fails and NewFromEvent surfaces the error.
//
//	message-start -> catch (registration fails) -> end
func TestNewFromEventSeedError(t *testing.T) {
	_ = data.CreateDefaultStates()

	med := events.MustMessageEventDefinition(
		bpmncommon.MustMessage("order placed",
			data.MustItemDefinition(values.NewVariable(""),
				foundation.WithID("order_in"))),
		nil)

	p, err := process.New("born-fail")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start", events.WithMessageTrigger(med))
	require.NoError(t, err)

	catch, err := events.NewIntermediateCatchEvent("catch",
		events.MustMessageEventDefinition(
			bpmncommon.MustMessage("next",
				data.MustItemDefinition(values.NewVariable(""),
					foundation.WithID("next_in"))),
			nil))
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, catch, end} {
		require.NoError(t, p.Add(e))
	}

	for _, l := range [][2]flow.Element{{start, catch}, {catch, end}} {
		_, err := flow.Link(
			l[0].(flow.SequenceSource), l[1].(flow.SequenceTarget))
		require.NoError(t, err)
	}

	s, err := snapshot.New(p)
	require.NoError(t, err)

	_, err = NewFromEvent(s, scope.EmptyDataPath, enginert.Default(),
		failEventProducer{}, nil, start.ID(), med, "", "")
	require.Error(t, err)
}

// TestNewFromEventBuildError covers NewFromEvent's build-error wrap: a snapshot
// with a blank process name makes the shared construction fail.
func TestNewFromEventBuildError(t *testing.T) {
	s, start, firedDef := msgStartSnapshot(t, "x")
	s.ProcessName = "   "

	_, err := NewFromEvent(s, scope.EmptyDataPath, enginert.Default(),
		failEventProducer{}, nil, start.ID(), firedDef, "", "")
	require.Error(t, err)
}

// TestNewEntryEventRegistrationFails covers createTracks' entry-node error path:
// an entry event node (a no-incoming message start) whose event registration
// fails makes seeding the initial tracks fail at New.
func TestNewEntryEventRegistrationFails(t *testing.T) {
	s, _, _ := msgStartSnapshot(t, "x")

	_, err := New(s, scope.EmptyDataPath, enginert.Default(),
		failEventProducer{}, nil)
	require.Error(t, err)
}

// TestBindEventPayloadNoItems covers the no-payload branch of bindEventPayload:
// an event definition carrying no items binds nothing and returns nil.
func TestBindEventPayloadNoItems(t *testing.T) {
	_ = data.CreateDefaultStates()

	// a plain none-start process: New needs no functional event producer (the
	// start has no event to register), so the instance builds and we can call
	// bindEventPayload directly.
	p, err := process.New("nop")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	require.NoError(t, p.Add(start))
	require.NoError(t, p.Add(end))
	_, err = flow.Link(start, end)
	require.NoError(t, err)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(),
		failEventProducer{}, nil)
	require.NoError(t, err)

	// a signal definition carries no items.
	require.NoError(t,
		inst.bindEventPayload(events.MustSignalEventDefinition(&events.Signal{})))
}

// TestNewBlankProcessName covers New's loadProperties error path: a snapshot
// whose process name is blank makes the root-scope path append fail.
func TestNewBlankProcessName(t *testing.T) {
	_ = data.CreateDefaultStates()

	s, _, _ := msgStartSnapshot(t, "x")
	s.ProcessName = "   " // root.Append trims to empty and fails

	_, err := New(s, scope.EmptyDataPath, enginert.Default(),
		failEventProducer{}, nil)
	require.Error(t, err)
}

// TestNewFromEventValidation covers NewFromEvent's input guards.
func TestNewFromEventValidation(t *testing.T) {
	s, start, firedDef := msgStartSnapshot(t, "x")

	rt := enginert.Default()
	eh, err := eventhub.New(rt)
	require.NoError(t, err)

	// empty start node id.
	_, err = NewFromEvent(s, scope.EmptyDataPath, rt, eh, nil, "   ", firedDef, "", "")
	require.Error(t, err)

	// nil event definition.
	_, err = NewFromEvent(s, scope.EmptyDataPath, rt, eh, nil, start.ID(), nil, "", "")
	require.Error(t, err)

	// start node not in the snapshot.
	_, err = NewFromEvent(
		s, scope.EmptyDataPath, rt, eh, nil, "no-such-node", firedDef, "", "")
	require.Error(t, err)
}
