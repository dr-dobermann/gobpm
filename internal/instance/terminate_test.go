package instance_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/instance"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/stretchr/testify/require"
)

// TestInstanceTerminateIdempotent covers Instance.Terminate (SRD-030 M1,
// FR-2/FR-6): it forwards to the cancel cascade and is safe to call repeatedly,
// including before Run when the instance context does not yet exist. The
// loop-driven Terminated settlement is covered by the M2 runtime tests.
func TestInstanceTerminateIdempotent(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	s, err := getSnapshot("terminate-idempotent")
	require.NoError(t, err)

	ep := mockeventproc.NewMockEventProducer(t)

	inst, err := instance.New(s, scope.EmptyDataPath, enginert.Default(), ep, nil)
	require.NoError(t, err)

	// Before Run the context does not exist yet; Terminate is a safe no-op, and
	// calling it twice must not panic (idempotent).
	require.NotPanics(t, func() {
		inst.Terminate()
		inst.Terminate()
	})
}
