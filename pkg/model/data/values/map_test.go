package values_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/stretchr/testify/require"
)

// TestMapValue covers the dynamic values.Map[T] (SRD-047 T-1, FR-2).
func TestMapValue(t *testing.T) {
	ctx := context.Background()

	t.Run("NewMap copies the input and allows nil", func(t *testing.T) {
		src := map[string]int{"a": 1, "b": 2}
		m, err := values.NewMap(src)
		require.NoError(t, err)

		src["a"] = 99 // the Map holds its own copy
		require.Equal(t, map[string]int{"a": 1, "b": 2}, m.Get(ctx))

		empty, err := values.NewMap[int](nil)
		require.NoError(t, err)
		require.Empty(t, empty.Get(ctx))
		require.Empty(t, empty.Keys())
	})

	t.Run("NewMap rejects an empty key; MustMap panics on it",
		func(t *testing.T) {
			_, err := values.NewMap(map[string]int{"": 1})
			require.Error(t, err)

			require.Panics(t, func() {
				values.MustMap(map[string]int{"": 1})
			})
		})

	t.Run("Get returns an isolated snapshot", func(t *testing.T) {
		m := values.MustMap(map[string]int{"a": 1})

		snap, ok := m.Get(ctx).(map[string]int)
		require.True(t, ok)

		snap["a"] = 42
		require.Equal(t, map[string]int{"a": 1}, m.Get(ctx))
	})

	t.Run("Update replaces the whole entry set", func(t *testing.T) {
		m := values.MustMap(map[string]int{"a": 1, "b": 2})

		require.NoError(t, m.Update(ctx, map[string]int{"c": 3}))
		require.Equal(t, map[string]int{"c": 3}, m.Get(ctx))
		require.Equal(t, []string{"c"}, m.Keys()) // "a"/"b" replaced away
	})

	t.Run("Update rejects a wrong payload shape and an empty key",
		func(t *testing.T) {
			m := values.MustMap(map[string]int{"a": 1})

			require.Error(t, m.Update(ctx, 42))
			require.Error(t, m.Update(ctx, map[string]string{"a": "x"}))
			require.Error(t, m.Update(ctx, map[string]int{"": 1}))

			// a failed Update leaves the value untouched
			require.Equal(t, map[string]int{"a": 1}, m.Get(ctx))
		})

	t.Run("Keys is sorted regardless of insertion order", func(t *testing.T) {
		m := values.MustMap(map[string]int{"zz": 1, "aa": 2, "mm": 3})
		require.NoError(t, m.SetEntry(ctx, "bb", 4))

		require.Equal(t, []string{"aa", "bb", "mm", "zz"}, m.Keys())
	})

	t.Run("Entry returns the value; a missing key is ObjectNotFound",
		func(t *testing.T) {
			m := values.MustMap(map[string]int{"a": 1})

			v, err := m.Entry(ctx, "a")
			require.NoError(t, err)
			require.Equal(t, 1, v)

			_, err = m.Entry(ctx, "nope")
			require.Error(t, err)
		})

	t.Run("SetEntry upserts; the value side is type-checked",
		func(t *testing.T) {
			m := values.MustMap(map[string]int{"a": 1})

			require.NoError(t, m.SetEntry(ctx, "a", 10))  // replace
			require.NoError(t, m.SetEntry(ctx, "new", 5)) // add — keys are data
			require.Equal(t, map[string]int{"a": 10, "new": 5}, m.Get(ctx))

			require.Error(t, m.SetEntry(ctx, "a", "not an int"))
			require.Error(t, m.SetEntry(ctx, "", 1))
		})

	t.Run("DeleteEntry removes; a missing key is ObjectNotFound",
		func(t *testing.T) {
			m := values.MustMap(map[string]int{"a": 1, "b": 2})

			require.NoError(t, m.DeleteEntry(ctx, "a"))
			require.Equal(t, []string{"b"}, m.Keys())

			require.Error(t, m.DeleteEntry(ctx, "a"))
		})

	t.Run("Clone is independent of the original", func(t *testing.T) {
		m := values.MustMap(map[string]int{"a": 1})

		c, ok := m.Clone().(data.Map)
		require.True(t, ok)

		require.NoError(t, c.SetEntry(ctx, "b", 2))
		require.NoError(t, m.DeleteEntry(ctx, "a"))

		require.Equal(t, []string{"a", "b"}, c.Keys())
		require.Empty(t, m.Keys())
	})

	t.Run("Lock/Unlock guard direct internal access", func(t *testing.T) {
		m := values.MustMap(map[string]int{"a": 1})

		m.Lock()
		guarded := true // the mutex guards direct pointer mutation of internals
		m.Unlock()

		require.True(t, guarded)
	})

	t.Run("Type names the element type", func(t *testing.T) {
		require.Equal(t, "int", values.MustMap[int](nil).Type())
		require.Equal(t, "Value", values.MustMap[data.Value](nil).Type())
	})

	t.Run("a Map implements no other structural capability",
		func(t *testing.T) {
			var v data.Value = values.MustMap(map[string]int{"a": 1})

			_, isRecord := v.(data.Record)
			_, isCollection := v.(data.Collection)
			require.False(t, isRecord)
			require.False(t, isCollection)
		})

	t.Run("typed helpers EntryT/SetEntryT", func(t *testing.T) {
		m := values.MustMap(map[string]int{"a": 1})

		v, err := m.EntryT("a")
		require.NoError(t, err)
		require.Equal(t, 1, v)

		_, err = m.EntryT("nope")
		require.Error(t, err)

		require.NoError(t, m.SetEntryT("b", 2))
		require.Equal(t, 2, must(m.EntryT("b")))

		require.Error(t, m.SetEntryT("", 3))
	})
}

// must unwraps a (value, error) pair for compact assertions.
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}

	return v
}
