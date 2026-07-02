package events_test

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/stretchr/testify/require"
)

// newTimerEDef builds a date-only TimerEventDefinition for the clone tests.
func newTimerEDef(t *testing.T) *events.TimerEventDefinition {
	t.Helper()

	return events.MustTimerEventDefinition(
		goexpr.Must(nil,
			data.MustItemDefinition(
				values.NewVariable(time.Now().Add(time.Second))),
			func(_ context.Context, _ data.Source) (data.Value, error) {
				return values.NewVariable(time.Now().Add(time.Second)), nil
			}),
		nil, nil)
}

// TestTimerEventDefinitionCloneForInstance verifies CloneForInstance yields a
// fresh id on each call (FIX-004) while sharing the timer expression by
// reference.
func TestTimerEventDefinitionCloneForInstance(t *testing.T) {
	data.CreateDefaultStates()

	ted := newTimerEDef(t)

	c1 := ted.CloneForInstance()
	c2 := ted.CloneForInstance()

	require.NotEqual(t, ted.ID(), c1.ID(), "clone must have a fresh id")
	require.NotEqual(t, c1.ID(), c2.ID(), "each clone must have a distinct id")

	// the immutable timer expression is shared by reference, not copied.
	require.Same(t, ted.Time(), c1.(*events.TimerEventDefinition).Time(),
		"timer expression must be shared with the template")
}

// TestTimerReceiverPerInstanceClone verifies that cloning a timer-catch node
// gives each clone a distinct timer-eDef id, so concurrent instances register
// distinct EventHub waiters (no shared-waiter broadcast — FIX-004).
func TestTimerReceiverPerInstanceClone(t *testing.T) {
	data.CreateDefaultStates()

	ted := newTimerEDef(t)

	start, err := events.NewStartEvent("start", events.WithTimerTrigger(ted))
	require.NoError(t, err)

	cn1, err := start.Clone()
	require.NoError(t, err)
	id1 := cn1.(flow.EventNode).Definitions()[0].ID()

	cn2, err := start.Clone()
	require.NoError(t, err)
	id2 := cn2.(flow.EventNode).Definitions()[0].ID()

	require.NotEqual(t, ted.ID(), id1, "cloned timer eDef must have a fresh id")
	require.NotEqual(t, id1, id2, "two instances must get distinct timer eDef ids")
}

// TestMessageEventDefinitionCloneForInstance verifies CloneForInstance yields a
// fresh id on each call (SRD-017 §4.3) while sharing the message by reference.
func TestMessageEventDefinitionCloneForInstance(t *testing.T) {
	data.CreateDefaultStates()

	med := events.MustMessageEventDefinition(
		bpmncommon.MustMessage("order placed", data.MustItemDefinition(nil)), nil)

	c1 := med.CloneForInstance()
	c2 := med.CloneForInstance()

	require.NotEqual(t, med.ID(), c1.ID(), "clone must have a fresh id")
	require.NotEqual(t, c1.ID(), c2.ID(), "each clone must have a distinct id")
}

// TestMessageReceiverPerInstanceClone verifies that cloning a message-catch node
// gives each clone a distinct message-eDef id, so concurrent instances register
// distinct EventHub waiters (no shared-waiter broadcast).
func TestMessageReceiverPerInstanceClone(t *testing.T) {
	data.CreateDefaultStates()

	med := events.MustMessageEventDefinition(
		bpmncommon.MustMessage("order placed", data.MustItemDefinition(nil)), nil)

	start, err := events.NewStartEvent("start", events.WithMessageTrigger(med))
	require.NoError(t, err)

	cn1, err := start.Clone()
	require.NoError(t, err)
	id1 := cn1.(flow.EventNode).Definitions()[0].ID()

	cn2, err := start.Clone()
	require.NoError(t, err)
	id2 := cn2.(flow.EventNode).Definitions()[0].ID()

	require.NotEqual(t, med.ID(), id1, "cloned message eDef must have a fresh id")
	require.NotEqual(t, id1, id2, "two instances must get distinct message eDef ids")
}

// TestNonMessageDefSharedOnClone verifies the else branch of cloneDefsForInstance:
// a non-message event definition stays shared by reference (same id) on clone —
// only message receivers are isolated per instance in SRD-017.
func TestNonMessageDefSharedOnClone(t *testing.T) {
	data.CreateDefaultStates()

	sed := events.MustSignalEventDefinition(&events.Signal{})

	start, err := events.NewStartEvent("start", events.WithSignalTrigger(sed))
	require.NoError(t, err)

	cn, err := start.Clone()
	require.NoError(t, err)
	id := cn.(flow.EventNode).Definitions()[0].ID()
	require.Equal(t, sed.ID(), id, "non-message def must stay shared on clone")
}
