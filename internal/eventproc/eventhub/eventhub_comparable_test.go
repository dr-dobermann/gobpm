package eventhub_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// valueProcessor is an EventProcessor implemented on a STRUCT whose slice
// field makes the type uncomparable — a shape a host can legitimately write,
// since pkg/eventproc.EventProcessor is a public contract. The slice field is
// the point: remove it and the type becomes comparable and the guard has
// nothing to refuse.
type valueProcessor struct {
	id   string
	keys []string
}

func (p valueProcessor) ID() string { return p.id }

func (valueProcessor) ProcessEvent(context.Context, flow.EventDefinition) error {
	return nil
}

// TestUncomparableProcessorRefused pins the guard at the boundary: a waiter
// identifies its processors by value, and Go PANICS rather than reporting
// false when two interface values of one uncomparable dynamic type are
// compared. Without the check the hub crashed on the SECOND registration for
// a definition, inside the waiter — so the refusal has to name the type at the
// call that can still act on it.
func TestUncomparableProcessorRefused(t *testing.T) {
	hub := startedHub(t, enginert.Default().MessageBroker())
	eDef := msgEDef(t, "confirm")

	err := hub.RegisterEvent(valueProcessor{id: "host"}, eDef)
	require.ErrorContains(t, err, "uncomparable type")
	require.ErrorContains(t, err, "register a pointer to it instead")

	require.ErrorContains(t,
		hub.RegisterPersistentEvent(valueProcessor{id: "starter"}, eDef),
		"uncomparable type")

	// the refusal is total: nothing was installed, so the second registration
	// that used to panic never happens.
	require.NoError(t, hub.RegisterEvent(
		&hubKeyedProcessor{id: "ok", keys: []string{"a"}}, eDef))
	require.NoError(t, hub.RegisterEvent(
		&hubKeyedProcessor{id: "ok2", keys: []string{"b"}}, eDef))
}
