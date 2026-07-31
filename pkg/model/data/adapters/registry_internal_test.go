package adapters

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
)

// FIX-034 turned the registry's cache reads from panicking assertions into
// reported errors. Only adapterFor and Register write that cache and both store
// a *typeAdapter, so the guard fires solely on a corrupted registry — which an
// internal test can produce and an external one cannot.

type poisonSubject struct{ X int }

func TestAsAdapterRejectsForeignCacheEntry(t *testing.T) {
	rt := reflect.TypeFor[poisonSubject]()

	adapterCache.Store(rt, "not a *typeAdapter")
	t.Cleanup(func() { adapterCache.Delete(rt) })

	t.Run("adapterFor reports it", func(t *testing.T) {
		ta, err := adapterFor(rt)
		require.Nil(t, ta)
		require.Error(t, err)

		var ae *errs.ApplicationError

		require.ErrorAs(t, err, &ae)
		require.True(t, ae.HasClass(errs.BrokenInvariant),
			"a corrupted cache is a broken invariant, not a caller's type error")
		require.Contains(t, err.Error(), "not *typeAdapter",
			"the message names what the entry actually held")
	})

	t.Run("customFor declines rather than panicking", func(t *testing.T) {
		ta, ok := customFor(rt)
		require.Nil(t, ta)
		require.False(t, ok)
	})
}

func TestAsAdapterAcceptsARealEntry(t *testing.T) {
	rt := reflect.TypeFor[poisonSubject]()
	want := &typeAdapter{goType: rt, name: rt.String()}

	got, err := asAdapter(rt, want)
	require.NoError(t, err)
	require.Same(t, want, got)
}

// TestCustomFactoryRejectsAForeignPointer covers the other half of the registry
// invariant: Register keys its factory by reflect.TypeFor[T], so only a *T ever
// reaches it. Handing it something else means the resolution order paired the
// wrong entry with the wrong factory — a broken registry, which fails fast
// rather than building a value from a pointer it cannot interpret.
func TestCustomFactoryRejectsAForeignPointer(t *testing.T) {
	type other struct{ Y string }

	require.NoError(t, Register(func(v *poisonSubject) data.Value {
		return values.NewVariable(v.X)
	}))

	rt := reflect.TypeFor[poisonSubject]()
	t.Cleanup(func() { adapterCache.Delete(rt) })

	ta, ok := customFor(rt)
	require.True(t, ok, "the factory registered")

	require.Panics(t, func() { ta.custom(reflect.ValueOf(&other{})) },
		"a pointer of the wrong type is a corrupted registry, not a value")

	require.NotPanics(t, func() { ta.custom(reflect.ValueOf(&poisonSubject{X: 1})) })
}
