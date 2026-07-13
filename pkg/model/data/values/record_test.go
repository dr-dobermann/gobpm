package values_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/stretchr/testify/require"
)

// TestRecordValue covers the dynamic values.Record (SRD-042 FR-2, T-1): the
// data.Record capability (Keys order, Field, SetField), whole-structure
// Get/Update, deep Clone independence, and field-name reservation.
func TestRecordValue(t *testing.T) {
	ctx := context.Background()

	t.Run("construction and Keys order", func(t *testing.T) {
		r := values.MustRecord(
			values.F("id", values.NewVariable("A-1")),
			values.F("total", values.NewVariable(150)),
		)
		require.Equal(t, []string{"id", "total"}, r.Keys())
		require.Equal(t, "record", r.Type())
	})

	t.Run("nil / duplicate / bad-name rejected", func(t *testing.T) {
		_, err := values.NewRecord(values.F("id", nil))
		require.Error(t, err)

		_, err = values.NewRecord(
			values.F("id", values.NewVariable(1)),
			values.F("id", values.NewVariable(2)),
		)
		require.Error(t, err)

		_, err = values.NewRecord(values.F("a.b", values.NewVariable(1)))
		require.Error(t, err)
	})

	t.Run("Field and its miss", func(t *testing.T) {
		r := values.MustRecord(values.F("total", values.NewVariable(150)))

		v, err := r.Field(ctx, "total")
		require.NoError(t, err)
		require.Equal(t, 150, v.Get(ctx))

		_, err = r.Field(ctx, "missing")
		require.Error(t, err)

		var ae *errs.ApplicationError
		require.ErrorAs(t, err, &ae)
		require.True(t, ae.HasClass(errs.ObjectNotFound))
	})

	t.Run("SetField adds and replaces; reserves the name", func(t *testing.T) {
		r := values.MustRecord(values.F("id", values.NewVariable("A-1")))

		require.NoError(t, r.SetField(ctx, "total", values.NewVariable(10)))
		require.Equal(t, []string{"id", "total"}, r.Keys())

		require.NoError(t, r.SetField(ctx, "total", values.NewVariable(20)))
		require.Equal(t, []string{"id", "total"}, r.Keys()) // no dup key
		v, _ := r.Field(ctx, "total")
		require.Equal(t, 20, v.Get(ctx))

		require.Error(t, r.SetField(ctx, "a[0]", values.NewVariable(1)))
		require.Error(t, r.SetField(ctx, "x", nil))
	})

	t.Run("whole-structure Get / Update round-trip", func(t *testing.T) {
		r := values.MustRecord(
			values.F("id", values.NewVariable("A-1")),
			values.F("total", values.NewVariable(150)),
		)

		snap := r.Get(ctx).(map[string]any)
		require.Equal(t, "A-1", snap["id"])
		require.Equal(t, 150, snap["total"])

		require.NoError(t, r.Update(ctx, map[string]any{"total": 200}))
		v, _ := r.Field(ctx, "total")
		require.Equal(t, 200, v.Get(ctx))

		require.Error(t, r.Update(ctx, map[string]any{"nope": 1})) // unknown field
		require.Error(t, r.Update(ctx, "not a map"))               // wrong shape
	})

	t.Run("MustRecord panics on a bad field", func(t *testing.T) {
		require.Panics(t, func() {
			values.MustRecord(values.F("a.b", values.NewVariable(1)))
		})
	})

	t.Run("Update propagates a field's own error", func(t *testing.T) {
		r := values.MustRecord(values.F("total", values.NewVariable(150)))
		// the field is Variable[int]; a string value fails its checkValue.
		require.Error(t, r.Update(ctx, map[string]any{"total": "not an int"}))
	})

	t.Run("Lock/Unlock guard direct access", func(t *testing.T) {
		r := values.MustRecord(values.F("id", values.NewVariable("A-1")))
		r.Lock()
		r.Unlock()
	})

	t.Run("Clone is deep and independent", func(t *testing.T) {
		r := values.MustRecord(values.F("total", values.NewVariable(150)))

		clone, ok := r.Clone().(data.Record)
		require.True(t, ok)

		require.NoError(t, clone.SetField(ctx, "total", values.NewVariable(999)))

		orig, _ := r.Field(ctx, "total")
		require.Equal(t, 150, orig.Get(ctx)) // original untouched
		cv, _ := clone.Field(ctx, "total")
		require.Equal(t, 999, cv.Get(ctx))
	})
}
