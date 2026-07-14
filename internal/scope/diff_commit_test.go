package scope

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/stretchr/testify/require"
)

// structData wraps a structural value as a committed datum (testData boxes its
// val into a scalar Variable, so records/lists need their own wrapper).
func structData(t *testing.T, name string, v data.Value) data.Data {
	t.Helper()

	_ = data.CreateDefaultStates()

	iae := data.MustItemAwareElement(
		data.MustItemDefinition(v), data.ReadyDataState)

	p, err := data.NewParameter(name, iae)
	require.NoError(t, err)

	return p
}

// orderData builds the "order" fixture {total, items:[{price}...]} at the
// given total/prices.
func orderData(t *testing.T, total int, prices ...int) data.Data {
	t.Helper()

	items := make([]data.Value, len(prices))
	for i, p := range prices {
		items[i] = values.MustRecord(values.F("price", values.NewVariable(p)))
	}

	return structData(t, "order", values.MustRecord(
		values.F("total", values.NewVariable(total)),
		values.F("items", values.NewArray(items...)),
	))
}

// TestCommitReturnsDiff covers the committed changed-path set (SRD-044 T-2):
// a first commit is one Value_Added at the name's root; a changed re-commit is
// per-path; an unchanged re-commit contributes nothing.
func TestCommitReturnsDiff(t *testing.T) {
	root := mustPath(t, "/proc")

	p, err := New(root, nil)
	require.NoError(t, err)

	t.Run("first commit — one Added at the root", func(t *testing.T) {
		changes, err := p.Commit(root, orderData(t, 100, 50, 100))
		require.NoError(t, err)
		require.Equal(t,
			[]data.Change{{Path: "order", Type: data.ValueAdded}}, changes)
	})

	t.Run("changed re-commit — per-path changes", func(t *testing.T) {
		changes, err := p.Commit(root, orderData(t, 150, 50, 100, 20))
		require.NoError(t, err)
		require.Equal(t, []data.Change{
			{Path: "order.total", Type: data.ValueUpdated},
			{Path: "order.items[2]", Type: data.ValueAdded},
		}, changes)
	})

	t.Run("unchanged re-commit — empty", func(t *testing.T) {
		changes, err := p.Commit(root, orderData(t, 150, 50, 100, 20))
		require.NoError(t, err)
		require.Empty(t, changes)
	})

	t.Run("batch aggregates per-name diffs", func(t *testing.T) {
		changes, err := p.Commit(root,
			orderData(t, 175, 50, 100, 20), testData(t, "note", "expedite"))
		require.NoError(t, err)
		require.Equal(t, []data.Change{
			{Path: "order.total", Type: data.ValueUpdated},
			{Path: "note", Type: data.ValueAdded},
		}, changes)
	})

	t.Run("empty batch — nothing", func(t *testing.T) {
		changes, err := p.Commit(root)
		require.NoError(t, err)
		require.Empty(t, changes)
	})
}

// TestFrameCommitPropagatesDiff (SRD-044 T-3): Frame.Commit surfaces the
// scope's changed-path set to its track caller.
func TestFrameCommitPropagatesDiff(t *testing.T) {
	root := mustPath(t, "/proc")

	p, err := New(root, nil)
	require.NoError(t, err)

	f, err := NewFrame("track-1", "node-1", p.Root(), p)
	require.NoError(t, err)

	require.NoError(t, f.Put(orderData(t, 100, 50)))

	changes, err := f.Commit()
	require.NoError(t, err)
	require.Equal(t,
		[]data.Change{{Path: "order", Type: data.ValueAdded}}, changes)

	// A second frame re-committing a changed order surfaces the leaf paths.
	f2, err := NewFrame("track-1", "node-2", p.Root(), p)
	require.NoError(t, err)

	require.NoError(t, f2.Put(orderData(t, 150, 50)))

	changes, err = f2.Commit()
	require.NoError(t, err)
	require.Equal(t, []data.Change{
		{Path: "order.total", Type: data.ValueUpdated}}, changes)
}
