package scope

import (
	"context"
	"errors"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
)

// TestSnapshotAt (SRD-059 FR-4): the compensation ledger's value-copy — the
// snapshot sees the walk-up surface and stays immune to later scope mutation.
func TestSnapshotAt(t *testing.T) {
	ctx := context.Background()
	root := mustPath(t, "/proc")

	p, err := New(root, nil)
	require.NoError(t, err)

	_, err = p.Commit(root, structData(t, "x", values.NewVariable(1)))
	require.NoError(t, err)

	t.Run("walk-up visibility from a child scope", func(t *testing.T) {
		child := mustPath(t, "/proc/sub")
		require.NoError(t, p.OpenScope(child))
		t.Cleanup(func() { _ = p.CloseScope(child) })

		_, err = p.Commit(child, structData(t, "y", values.NewVariable(2)))
		require.NoError(t, err)

		snap, err := p.SnapshotAt(child)
		require.NoError(t, err)
		require.Len(t, snap, 2, "x by walk-up, y local")
	})

	t.Run("a snapshot is a value copy — later mutation invisible", func(t *testing.T) {
		snap, err := p.SnapshotAt(root)
		require.NoError(t, err)
		require.Len(t, snap, 1)
		require.Equal(t, "x", snap[0].Name())
		require.Equal(t, 1, snap[0].Value().Get(ctx))

		// mutate the live scope after the snapshot.
		_, err = p.Commit(root, structData(t, "x", values.NewVariable(42)))
		require.NoError(t, err)

		live, err := p.GetData(root, "x")
		require.NoError(t, err)
		require.Equal(t, 42, live.Value().Get(ctx))
		require.Equal(t, 1, snap[0].Value().Get(ctx),
			"the snapshot still sees the world as it was")
	})

	t.Run("an uncontained path errors", func(t *testing.T) {
		_, err := p.SnapshotAt(mustPath(t, "/elsewhere"))
		require.Error(t, err)
	})

	t.Run("an unreadable datum errors (white-box)", func(t *testing.T) {
		// forge: a name registered in the scope whose datum doesn't answer to
		// it (a zero-value Parameter has no name) — GetData misses and
		// SnapshotAt surfaces the read failure.
		p.scopes[root]["ghost"] = &data.Parameter{}
		t.Cleanup(func() { delete(p.scopes[root], "ghost") })

		_, err := p.SnapshotAt(root)
		require.Error(t, err)
	})

	t.Run("an unclonable datum errors (white-box)", func(t *testing.T) {
		p.scopes[root]["bad"] = &unclonableDatum{name: "bad"}
		t.Cleanup(func() { delete(p.scopes[root], "bad") })

		_, err := p.SnapshotAt(root)
		require.Error(t, err)
	})

	t.Run("a failing clone errors (white-box)", func(t *testing.T) {
		p.scopes[root]["bad2"] = &failingCloneDatum{
			unclonableDatum: unclonableDatum{name: "bad2"},
		}
		t.Cleanup(func() { delete(p.scopes[root], "bad2") })

		_, err := p.SnapshotAt(root)
		require.Error(t, err)
	})
}

// unclonableDatum is a data.Data with no Clone method — SnapshotAt's
// isn't-clonable defensive branch (every real scope datum is a *data.Parameter
// and clones; the branch guards the interface seam).
type unclonableDatum struct {
	foundation.BaseElement
	name string
}

func (d *unclonableDatum) Name() string                        { return d.name }
func (d *unclonableDatum) Value() data.Value                   { return nil }
func (d *unclonableDatum) State() data.SrcState                { return data.SrcState{} }
func (d *unclonableDatum) ItemDefinition() *data.ItemDefinition { return nil }

// failingCloneDatum adds a Clone that always fails — SnapshotAt's clone-error
// branch.
type failingCloneDatum struct {
	unclonableDatum
}

func (d *failingCloneDatum) Clone() (*data.ItemAwareElement, error) {
	return nil, errors.New("forged clone failure")
}
