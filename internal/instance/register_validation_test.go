package instance_test

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/instance"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/stretchr/testify/require"
)

// TestRegisterEventNilProcessorTerminal pins FIX-010 §3.2.3: on a terminal
// instance, RegisterEvent with a nil processor must return the validation error
// rather than panic building the terminal-state diagnostic from proc.ID().
func TestRegisterEventNilProcessorTerminal(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	s, err := getSnapshot("reg-validation")
	require.NoError(t, err)

	ep := mockeventproc.NewMockEventProducer(t)

	inst, err := instance.New(s, scope.EmptyDataPath, enginert.Default(), ep, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, inst.Run(ctx))

	inst.Cancel()

	select {
	case <-inst.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("instance did not reach a terminal state")
	}

	// Terminal instance + nil processor: the nil guard runs before the
	// terminal-state branch builds proc.ID(), so this is a classified error,
	// never a panic.
	require.NotPanics(t, func() {
		require.Error(t, inst.RegisterEvent(nil, nil))
	})

	// A non-nil processor with a nil event definition is rejected by the eDef
	// guard (which now runs before the state branch too).
	proc := mockeventproc.NewMockEventProcessor(t)
	require.Error(t, inst.RegisterEvent(proc, nil))
}
