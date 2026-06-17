package events_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/stretchr/testify/require"
)

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

	id1 := start.Clone().(flow.EventNode).Definitions()[0].ID()
	id2 := start.Clone().(flow.EventNode).Definitions()[0].ID()

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

	id := start.Clone().(flow.EventNode).Definitions()[0].ID()
	require.Equal(t, sed.ID(), id, "non-message def must stay shared on clone")
}
