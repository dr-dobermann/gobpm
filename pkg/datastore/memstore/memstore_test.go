package memstore_test

import (
	"context"
	"sync"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/datastore"
	"github.com/dr-dobermann/gobpm/pkg/datastore/memstore"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/stretchr/testify/require"
)

// datum builds a ready item-aware datum holding v (a data.Data value).
func datum(t *testing.T, v any) data.Data {
	t.Helper()

	return data.MustItemAwareElement(
		data.MustItemDefinition(values.NewVariable(v)),
		data.ReadyDataState)
}

func TestInMemoryDataStore(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()

	t.Run("put then get", func(t *testing.T) {
		s := memstore.New()
		require.NoError(t, s.Put(ctx, "k", datum(t, 42)))

		d, ok, err := s.Get(ctx, "k")
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, 42, d.Value().Get(ctx))
	})

	t.Run("missing key", func(t *testing.T) {
		s := memstore.New()
		d, ok, err := s.Get(ctx, "absent")
		require.NoError(t, err)
		require.False(t, ok)
		require.Nil(t, d)
	})

	t.Run("put replaces", func(t *testing.T) {
		s := memstore.New()
		require.NoError(t, s.Put(ctx, "k", datum(t, 1)))
		require.NoError(t, s.Put(ctx, "k", datum(t, 2)))

		d, _, _ := s.Get(ctx, "k")
		require.Equal(t, 2, d.Value().Get(ctx))
	})

	t.Run("empty name rejected", func(t *testing.T) {
		s := memstore.New()
		require.Error(t, s.Put(ctx, "", datum(t, 1)))
		_, _, err := s.Get(ctx, "")
		require.Error(t, err)
	})

	t.Run("nil datum rejected", func(t *testing.T) {
		s := memstore.New()
		require.Error(t, s.Put(ctx, "k", nil))
	})

	t.Run("capacity is advisory (over-capacity Put succeeds)",
		func(t *testing.T) {
			s := memstore.New(memstore.WithCapacity(1))
			require.Equal(t, 1, s.Capacity())
			require.False(t, s.IsUnlimited())

			require.NoError(t, s.Put(ctx, "a", datum(t, 1)))
			// exceeds the nominal capacity — accepted, not rejected (ADR-030 §2.6).
			require.NoError(t, s.Put(ctx, "b", datum(t, 2)))
		})

	t.Run("unlimited by default", func(t *testing.T) {
		s := memstore.New()
		require.True(t, s.IsUnlimited())
		require.Equal(t, memstore.Unlimited, s.Capacity())
	})

	t.Run("non-positive capacity stays unlimited", func(t *testing.T) {
		s := memstore.New(memstore.WithCapacity(-5))
		require.True(t, s.IsUnlimited())
	})

	t.Run("concurrent access is race-free", func(t *testing.T) {
		s := memstore.New()
		d := datum(t, 1)

		var wg sync.WaitGroup
		for range 50 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = s.Put(ctx, "k", d)
				_, _, _ = s.Get(ctx, "k")
			}()
		}
		wg.Wait()
	})

	// interface conformance.
	var _ datastore.DataStore = memstore.New()
}
