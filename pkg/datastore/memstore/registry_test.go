package memstore_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/datastore"
	"github.com/dr-dobermann/gobpm/pkg/datastore/memstore"
	"github.com/stretchr/testify/require"
)

func TestRegistry(t *testing.T) {
	t.Run("register then resolve", func(t *testing.T) {
		reg := memstore.NewRegistry()
		s := memstore.New()
		require.NoError(t, reg.Register("orders", s))

		got, err := reg.Store("orders")
		require.NoError(t, err)
		require.Same(t, s, got)
	})

	t.Run("unknown ref fails loud", func(t *testing.T) {
		reg := memstore.NewRegistry()
		_, err := reg.Store("absent")
		require.Error(t, err)
	})

	t.Run("empty ref rejected", func(t *testing.T) {
		reg := memstore.NewRegistry()
		require.Error(t, reg.Register("", memstore.New()))
	})

	t.Run("nil store rejected", func(t *testing.T) {
		reg := memstore.NewRegistry()
		require.Error(t, reg.Register("x", nil))
	})

	t.Run("register replaces", func(t *testing.T) {
		reg := memstore.NewRegistry()
		first := memstore.New()
		last := memstore.New()
		require.NoError(t, reg.Register("k", first))
		require.NoError(t, reg.Register("k", last))

		got, _ := reg.Store("k")
		require.Same(t, last, got)
	})

	// interface conformance.
	var _ datastore.Registry = memstore.NewRegistry()
}
