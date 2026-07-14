package scope

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetSource(t *testing.T) {
	root := mustPath(t, "/proc")

	t.Run("RUNTIME source resolves and rejects", func(t *testing.T) {
		p, err := New(root, &stubSupplier{t: t})
		require.NoError(t, err)

		d, err := p.GetSource(RuntimeVarsSegment, "alive")
		require.NoError(t, err)
		require.Equal(t, "alive", d.Name())

		// the provider rejects an unknown address verbatim
		_, err = p.GetSource(RuntimeVarsSegment, "ghost")
		require.Error(t, err)
	})

	t.Run("unknown source is an error", func(t *testing.T) {
		p, err := New(root, &stubSupplier{t: t})
		require.NoError(t, err)

		_, err = p.GetSource("BUSINESS", "order")
		require.Error(t, err)
	})

	t.Run("no RUNTIME source without a supplier", func(t *testing.T) {
		p, err := New(root, nil)
		require.NoError(t, err)

		_, err = p.GetSource(RuntimeVarsSegment, "alive")
		require.Error(t, err)
	})
}

func TestRuntimeSourceAdapter(t *testing.T) {
	src := runtimeSource{rt: &stubSupplier{t: t}}

	require.Equal(t, []string{"alive"}, src.Names())

	d, err := src.Get("alive")
	require.NoError(t, err)
	require.Equal(t, "alive", d.Name())
}

func TestDiscovery(t *testing.T) {
	root := mustPath(t, "/proc")

	t.Run("sources listed only when a supplier is configured", func(t *testing.T) {
		with, err := New(root, &stubSupplier{t: t})
		require.NoError(t, err)
		require.Equal(t, []string{RuntimeVarsSegment}, with.GetSources())

		without, err := New(root, nil)
		require.NoError(t, err)
		require.Empty(t, without.GetSources())
	})

	t.Run("List enumerates default scope and sources", func(t *testing.T) {
		p, err := New(root, &stubSupplier{t: t})
		require.NoError(t, err)

		require.NoError(t, errOf(p.Commit(p.Root(), testData(t, "b", 1))))
		require.NoError(t, errOf(p.Commit(p.Root(), testData(t, "a", 2))))

		// default scope, sorted
		names, err := p.List("")
		require.NoError(t, err)
		require.Equal(t, []string{"a", "b"}, names)

		// a named source returns its provider's names
		rt, err := p.List(RuntimeVarsSegment)
		require.NoError(t, err)
		require.Equal(t, []string{"alive"}, rt)

		// an unknown source is an error
		_, err = p.List("BUSINESS")
		require.Error(t, err)
	})
}
