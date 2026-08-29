package instance_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/instance"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/stretchr/testify/require"
)

// TestInstanceRunRejectsNonCreated covers Instance.Run's state guard: Run is
// only valid from the Created state, so a second Run (after the first moved the
// instance out of Created) is rejected with an invalid-state error.
func TestInstanceRunRejectsNonCreated(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	s, err := getSnapshot("run_rejects_non_created")
	require.NoError(t, err)

	ep := mockeventproc.NewMockEventProducer(t)
	inst, err := instance.New(s, scope.EmptyDataPath, enginert.Default(), ep, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-canceled: the first Run settles terminally, out of Created

	require.NoError(t, inst.Run(ctx))
	require.Error(t, inst.Run(context.Background()))
}

// TestInstancePropagateEventNotActive covers Instance.PropagateEvent's active
// guard: propagating a thrown event is only valid while the instance is Active,
// so a freshly-created (not yet run) instance rejects it.
func TestInstancePropagateEventNotActive(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	s, err := getSnapshot("propagate_not_active")
	require.NoError(t, err)

	ep := mockeventproc.NewMockEventProducer(t)
	inst, err := instance.New(s, scope.EmptyDataPath, enginert.Default(), ep, nil)
	require.NoError(t, err)

	term, err := events.NewTerminateEventDefinition()
	require.NoError(t, err)

	require.Error(t, inst.PropagateEvent(context.Background(), term))
}
