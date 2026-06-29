package instance

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
)

// TestExecEnv covers the per-execution environment surface (SRD-007 FR-3):
// data calls delegate to the frame; Find serves expression evaluation with
// the same resolution.
func TestExecEnv(t *testing.T) {
	_ = data.CreateDefaultStates()

	inst, err := New(buildForkSnapshot(t), scope.EmptyDataPath,
		enginert.Default(), mockeventproc.NewMockEventProducer(t), nil)
	require.NoError(t, err)

	f, err := scope.NewFrame("track-x", "node-x",
		inst.sc.plane.Root(), inst.sc.plane)
	require.NoError(t, err)

	env := newExecEnv(inst, f)

	ctx := context.Background()

	x, err := data.NewParameter("x",
		data.MustItemAwareElement(
			data.MustItemDefinition(
				values.NewVariable(42),
				foundation.WithID("x-id")),
			data.ReadyDataState))
	require.NoError(t, err)

	require.NoError(t, env.Put(x))

	d, err := env.GetData("x")
	require.NoError(t, err)
	require.Equal(t, 42, d.Value().Get(ctx))

	d, err = env.GetDataByID("x-id")
	require.NoError(t, err)
	require.Equal(t, "x", d.Name())

	// Find (data.Source for expressions) resolves identically.
	d, err = env.Find(ctx, "x")
	require.NoError(t, err)
	require.Equal(t, 42, d.Value().Get(ctx))

	_, err = env.Find(ctx, "ghost")
	require.Error(t, err)

	// identity and services delegate to the instance.
	require.Equal(t, inst.ID(), env.InstanceID())
}
