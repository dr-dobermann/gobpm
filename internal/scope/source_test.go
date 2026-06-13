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
