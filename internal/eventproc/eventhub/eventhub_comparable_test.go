package eventhub_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/eventproc/eventhub"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/renv"
)

// keyedProcessor is a POINTER-receiver EventProcessor carrying declared
// correlation keys — a comparable dynamic type, which is what a waiter needs
// to identify its processors by value.
type keyedProcessor struct {
	id   string
	keys []string
}

func newKeyedProcessor(id string, keys ...string) *keyedProcessor {
	return &keyedProcessor{id: id, keys: keys}
}

func (p *keyedProcessor) ID() string { return p.id }

func (p *keyedProcessor) CorrelationKeys() []string {
	return append([]string(nil), p.keys...)
}

func (p *keyedProcessor) ProcessEvent(
	context.Context, flow.EventDefinition,
) error {
	return nil
}

// startedHub returns a started hub over rt, torn down with the test.
func startedHub(t *testing.T, rt renv.EngineRuntime) *eventhub.EventHub {
	t.Helper()

	hub, err := eventhub.New(rt)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, hub.Start(ctx))

	return hub
}

// valueProcessor is an EventProcessor implemented on a STRUCT whose slice
// field makes the type uncomparable — a shape a host can legitimately write,
// since pkg/eventproc.EventProcessor is a public contract.
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
	require.NoError(t, data.CreateDefaultStates())

	hub := startedHub(t, enginert.Default())
	eDef := msgEDef(t, "confirm")

	err := hub.RegisterEvent(valueProcessor{id: "host"}, eDef)
	require.ErrorContains(t, err, "uncomparable type")
	require.ErrorContains(t, err, "register a pointer to it instead")

	require.ErrorContains(t,
		hub.RegisterPersistentEvent(valueProcessor{id: "starter"}, eDef),
		"uncomparable type")

	// the refusal is total: nothing was installed, so the second registration
	// that used to panic never happens.
	require.NoError(t, hub.RegisterEvent(newKeyedProcessor("ok", "a"), eDef))
	require.NoError(t, hub.RegisterEvent(newKeyedProcessor("ok2", "b"), eDef))
}
