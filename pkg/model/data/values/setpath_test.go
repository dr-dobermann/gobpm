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

// TestMapSetPath covers the key step on the write walk (SRD-047 M3, T-4/FR-6):
// upsert an entry, vivify a missing map through a following key step, and the
// classified mis-step errors.
func TestMapSetPath(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()

	t.Run("upsert an existing and a new entry", func(t *testing.T) {
		root := values.MustRecord(values.F("fx",
			values.MustMap(map[string]data.Value{
				"EUR": values.NewVariable(1.08),
			})))

		// replace an existing entry
		require.NoError(t, values.SetPath(ctx, root, `fx["EUR"]`,
			values.NewVariable(1.09)))
		// add a new one — keys are data
		require.NoError(t, values.SetPath(ctx, root, `fx["GBP"]`,
			values.NewVariable(1.27)))

		fx, err := root.Field(ctx, "fx")
		require.NoError(t, err)
		require.Equal(t, []string{"EUR", "GBP"}, fx.(data.Map).Keys())
	})

	t.Run("vivify a map through a following key step", func(t *testing.T) {
		root := values.MustRecord()

		// "quotes" is missing; the following ["EUR"] step vivifies a map,
		// then ".bid" vivifies a record inside it, then the leaf is set.
		require.NoError(t, values.SetPath(ctx, root, `quotes["EUR"].bid`,
			values.NewVariable(107)))

		require.Equal(t, 107, readInt(t, root,
			data.Step{Field: "quotes"}, data.Step{Key: "EUR"},
			data.Step{Field: "bid"}))

		// a second write descends the now-existing ["EUR"] entry rather than
		// re-vivifying it, so both fields coexist in the same record.
		require.NoError(t, values.SetPath(ctx, root, `quotes["EUR"].ask`,
			values.NewVariable(108)))
		require.Equal(t, 108, readInt(t, root,
			data.Step{Field: "quotes"}, data.Step{Key: "EUR"},
			data.Step{Field: "ask"}))
		require.Equal(t, 107, readInt(t, root,
			data.Step{Field: "quotes"}, data.Step{Key: "EUR"},
			data.Step{Field: "bid"}))
	})

	t.Run("set at a top-level dynamic map root", func(t *testing.T) {
		root := values.MustMap[data.Value](nil)

		require.NoError(t, values.SetPath(ctx, root, `["k"]`,
			values.NewVariable(5)))
		require.Equal(t, 5, readInt(t, root, data.Step{Key: "k"}))
	})

	t.Run("a raw (non-Value) entry is not navigable for a deeper write",
		func(t *testing.T) {
			root := values.MustRecord(values.F("m",
				values.MustMap(map[string]int{"k": 1})))

			err := values.SetPath(ctx, root, `m["k"].deeper`,
				values.NewVariable(2))
			require.Error(t, err)
			require.Contains(t, err.Error(), "navigable entry")
		})

	t.Run("a key step into a non-map is notWritable", func(t *testing.T) {
		root := values.MustRecord(values.F("total", values.NewVariable(1)))

		// last-step key into a scalar → setLast's map guard
		err := values.SetPath(ctx, root, `total["k"]`, values.NewVariable(2))
		require.Error(t, err)
		require.Contains(t, err.Error(), "a map")

		// intermediate key into a scalar → descendOrVivify's map guard
		err = values.SetPath(ctx, root, `total["k"].deep`,
			values.NewVariable(3))
		require.Error(t, err)
		require.Contains(t, err.Error(), "a map")
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
