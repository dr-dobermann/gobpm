package values_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/stretchr/testify/require"
)

// orderVal builds {total:150, items:[{price:50}]} — the write fixture.
func orderVal() *values.Record {
	return values.MustRecord(
		values.F("total", values.NewVariable(150)),
		values.F("items", values.NewArray[data.Value](
			values.MustRecord(values.F("price", values.NewVariable(50))))),
	)
}

// readInt walks the steps and returns the leaf as an int (via the S1 read walk).
func readInt(t *testing.T, root data.Value, steps ...data.Step) int {
	t.Helper()

	v, err := data.WalkSteps(context.Background(), root, steps)
	require.NoError(t, err)

	n, ok := v.Get(context.Background()).(int)
	require.True(t, ok)

	return n
}

// TestSetPath covers the write-walk (SRD-043 T-1): set an existing nested field,
// append a list element, auto-vivify missing records/lists, and every error.
func TestSetPath(t *testing.T) {
	ctx := context.Background()

	t.Run("set an existing nested field", func(t *testing.T) {
		o := orderVal()
		require.NoError(t,
			values.SetPath(ctx, o, "items[0].price", values.NewVariable(60)))
		require.Equal(t, 60,
			readInt(t, o, data.Step{Field: "items"}, data.Step{Index: 0},
				data.Step{Field: "price"}))
	})

	t.Run("append a list element at index == len", func(t *testing.T) {
		o := orderVal()
		newItem := values.MustRecord(values.F("price", values.NewVariable(99)))
		require.NoError(t, values.SetPath(ctx, o, "items[1]", newItem))
		require.Equal(t, 99,
			readInt(t, o, data.Step{Field: "items"}, data.Step{Index: 1},
				data.Step{Field: "price"}))
	})

	t.Run("auto-vivify a missing record chain", func(t *testing.T) {
		root := values.MustRecord()
		require.NoError(t,
			values.SetPath(ctx, root, "order.total", values.NewVariable(150)))
		require.Equal(t, 150,
			readInt(t, root, data.Step{Field: "order"},
				data.Step{Field: "total"}))
	})

	t.Run("auto-vivify a missing list + record", func(t *testing.T) {
		root := values.MustRecord()
		require.NoError(t,
			values.SetPath(ctx, root, "items[0].price", values.NewVariable(7)))
		require.Equal(t, 7,
			readInt(t, root, data.Step{Field: "items"}, data.Step{Index: 0},
				data.Step{Field: "price"}))
	})

	t.Run("errors", func(t *testing.T) {
		o := orderVal()

		// index past len (a hole) — no auto-grow.
		require.Error(t,
			values.SetPath(ctx, o, "items[5].price", values.NewVariable(1)))
		// field into a scalar (last step).
		require.Error(t,
			values.SetPath(ctx, o, "total.nope", values.NewVariable(1)))
		// field into a scalar (mid-walk).
		require.Error(t,
			values.SetPath(ctx, o, "total.a.b", values.NewVariable(1)))
		// index into a scalar (mid-walk).
		require.Error(t,
			values.SetPath(ctx, o, "total[0].x", values.NewVariable(1)))
		// index into a record (last step).
		require.Error(t,
			values.SetPath(ctx, o, "[0]", values.NewVariable(1)))
		// step into a raw scalar element.
		tags := values.MustRecord(values.F("tags",
			values.NewArray("urgent")))
		require.Error(t,
			values.SetPath(ctx, tags, "tags[0].x", values.NewVariable(1)))
		// malformed path.
		require.Error(t,
			values.SetPath(ctx, o, "a..b", values.NewVariable(1)))
		// empty path — a whole-value write is Value.Update.
		require.Error(t, values.SetPath(ctx, o, "", values.NewVariable(1)))
	})
}

// TestArraySetAt covers the atomic indexed set (SRD-043 T-2): set, append,
// out-of-range, type-mismatch, and the cursor-free guarantee.
func TestArraySetAt(t *testing.T) {
	ctx := context.Background()

	t.Run("set an existing index, cursor unchanged", func(t *testing.T) {
		a := values.NewArray(1, 2, 3)
		require.NoError(t, a.GoTo(2))
		require.NoError(t, a.SetAt(ctx, 0, 10))

		v, err := a.GetAt(ctx, 0)
		require.NoError(t, err)
		require.Equal(t, 10, v)
		require.Equal(t, 2, a.Index()) // SetAt did not move the cursor
	})

	t.Run("append at index == len; empty seats the cursor", func(t *testing.T) {
		a := values.NewArray[int]()
		require.Equal(t, -1, a.Index())
		require.NoError(t, a.SetAt(ctx, 0, 5)) // append on empty
		require.Equal(t, 1, a.Count())
		require.Equal(t, 0, a.Index()) // seated like Add

		require.NoError(t, a.SetAt(ctx, 1, 6)) // append at len
		require.Equal(t, 2, a.Count())
		require.Equal(t, 0, a.Index()) // still, not moved
	})

	t.Run("out of range and type mismatch", func(t *testing.T) {
		a := values.NewArray(1, 2)
		require.Error(t, a.SetAt(ctx, 9, 1))         // hole
		require.Error(t, a.SetAt(ctx, -1, 1))        // negative
		require.Error(t, a.SetAt(ctx, 0, "not int")) // value type mismatch
		require.Error(t, a.SetAt(ctx, "x", 1))       // index not an int
	})

	t.Run("SetAtT typed", func(t *testing.T) {
		a := values.NewArray(1, 2)
		require.NoError(t, a.SetAtT(1, 20))
		require.NoError(t, a.SetAtT(2, 30)) // append
		require.Error(t, a.SetAtT(9, 1))    // hole
		v, _ := a.GetAtT(2)
		require.Equal(t, 30, v)
	})
}
