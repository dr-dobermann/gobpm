package scope

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/stretchr/testify/require"
)

// stubSupplier serves a single runtime variable named "alive".
type stubSupplier struct {
	t *testing.T
}

func (s *stubSupplier) RuntimeVar(name string) (data.Data, error) {
	if name != "alive" {
		return nil,
			errs.New(
				errs.M("unknown runtime variable %q", name))
	}

	return testData(s.t, name, true), nil
}

func (s *stubSupplier) RuntimeVarNames() []string {
	return []string{"alive"}
}

func TestPlaneRuntimeVars(t *testing.T) {
	root := mustPath(t, "/proc")
	rtPath := mustPath(t, "/proc/"+RuntimeVarsSegment)

	t.Run("supplier serves the reserved path", func(t *testing.T) {
		p, err := New(root, &stubSupplier{t: t})
		require.NoError(t, err)

		d, err := p.GetData(rtPath, "alive")
		require.NoError(t, err)
		require.Equal(t, "alive", d.Name())

		_, err = p.GetData(rtPath, "ghost")
		require.Error(t, err)
	})

	t.Run("reserved path is read-only with a supplier", func(t *testing.T) {
		p, err := New(root, &stubSupplier{t: t})
		require.NoError(t, err)

		require.Error(t, errOf(p.Commit(rtPath, testData(t, "x", 1))))
		require.Error(t, p.OpenScope(rtPath))
	})

	t.Run("reserved path is read-only without a supplier",
		func(t *testing.T) {
			p, err := New(root, nil)
			require.NoError(t, err)

			require.Error(t, errOf(p.Commit(rtPath, testData(t, "x", 1))))
			require.Error(t, p.OpenScope(rtPath))

			// nothing is served either — the lookup falls through to the
			// ordinary (empty) walk.
			_, err = p.GetData(rtPath, "alive")
			require.Error(t, err)
		})

	t.Run("subtree under the reserved path is protected too",
		func(t *testing.T) {
			p, err := New(root, nil)
			require.NoError(t, err)

			sub := mustPath(t, "/proc/"+RuntimeVarsSegment+"/deep")
			require.Error(t, errOf(p.Commit(sub, testData(t, "x", 1))))
			require.Error(t, p.OpenScope(sub))
		})
}
