package instance

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/stretchr/testify/require"
)

// rtStub is a minimal scope.RuntimeVarsSupplier serving one runtime variable
// named "alive", used to exercise the execEnv data surface in isolation.
type rtStub struct {
	t *testing.T
}

func (s rtStub) RuntimeVar(name string) (data.Data, error) {
	if name != "alive" {
		return nil, errs.New(errs.M("unknown runtime variable %q", name))
	}

	id := data.MustItemDefinition(values.NewVariable(true))
	iae := data.MustItemAwareElement(id, data.ReadyDataState)

	p, err := data.NewParameter(name, iae)
	require.NoError(s.t, err)

	return p, nil
}

func (s rtStub) RuntimeVarNames() []string {
	return []string{"alive"}
}

func TestExecEnvDataSurface(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	root, err := scope.NewDataPath("/proc")
	require.NoError(t, err)

	plane, err := scope.New(root, rtStub{t: t})
	require.NoError(t, err)

	f, err := scope.NewFrame("track-1", "node-1", plane.Root(), plane)
	require.NoError(t, err)

	// the embedded *Instance is unused by the data-surface methods.
	ee := newExecEnv(&Instance{}, f, nil)

	t.Run("discovery delegates to the frame", func(t *testing.T) {
		require.Equal(t,
			[]string{scope.RuntimeVarsSegment}, ee.GetSources())

		names, err := ee.List("")
		require.NoError(t, err)
		require.Empty(t, names)

		rt, err := ee.List(scope.RuntimeVarsSegment)
		require.NoError(t, err)
		require.Equal(t, []string{"alive"}, rt)
	})

	t.Run("a runtime variable resolves by path (FR-6)", func(t *testing.T) {
		addr := scope.RuntimeVarsSegment + scope.PathSeparator + "alive"

		// the data.Source path used by condition/gateway evaluation
		d, err := ee.Find(context.Background(), addr)
		require.NoError(t, err)
		require.Equal(t, "alive", d.Name())

		d, err = ee.GetData(addr)
		require.NoError(t, err)
		require.Equal(t, "alive", d.Name())
	})
}
