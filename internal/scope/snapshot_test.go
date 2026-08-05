package scope

import (
	"context"
	"errors"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	dataobjects "github.com/dr-dobermann/gobpm/pkg/model/data_objects"
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

	t.Run("property and data object clone by their own shapes", func(t *testing.T) {
		// Property and DataObject declare concrete Clone methods that shadow
		// the promoted ItemAwareElement.Clone (the SRD-079 incident-snapshot
		// gap): cloneDatum must copy both, preserving value-copy semantics.
		prop, err := data.NewProperty("prop",
			data.MustItemDefinition(values.NewVariable(7),
				foundation.WithID("prop")),
			data.ReadyDataState)
		require.NoError(t, err)

		do, err := dataobjects.New("dobj",
			data.MustItemDefinition(values.NewVariable("d1"),
				foundation.WithID("dobj")),
			data.ReadyDataState)
		require.NoError(t, err)

		_, err = p.Commit(root, prop, do)
		require.NoError(t, err)

		snap, err := p.SnapshotAt(root)
		require.NoError(t, err)

		byName := map[string]data.Data{}
		for _, d := range snap {
			byName[d.Name()] = d
		}

		require.Contains(t, byName, "prop")
		require.Contains(t, byName, "dobj")
		require.Equal(t, 7, byName["prop"].Value().Get(ctx))
		require.Equal(t, "d1", byName["dobj"].Value().Get(ctx))

		// mutate the live scope: the snapshots must not see it.
		_, err = p.Commit(root,
			structData(t, "prop", values.NewVariable(100)))
		require.NoError(t, err)
		require.Equal(t, 7, byName["prop"].Value().Get(ctx),
			"the property snapshot is a value copy")
	})

	t.Run("a failing clone reports its datum and location", func(t *testing.T) {
		child := mustPath(t, "/proc/cl")
		require.NoError(t, p.OpenScope(child))
		t.Cleanup(func() { _ = p.CloseScope(child) })

		for name, d := range map[string]data.Data{
			"bad":  &failingCloneDatum{unclonableDatum{name: "bad"}},
			"badp": &failingPropCloneDatum{unclonableDatum{name: "badp"}},
			"badd": &failingDOCloneDatum{unclonableDatum{name: "badd"}},
		} {
			p.scopes[child] = map[string]data.Data{name: d}

			_, err = p.SnapshotAt(child)
			require.Error(t, err)
			require.Contains(t, err.Error(), `couldn't clone "`+name+`"`)
		}
	})

	t.Run("an unwrappable clone reports the wrap failure", func(t *testing.T) {
		child := mustPath(t, "/proc/wr")
		require.NoError(t, p.OpenScope(child))
		t.Cleanup(func() { _ = p.CloseScope(child) })

		// the clone succeeds but the datum's reserved-character name breaks
		// the Parameter wrap.
		p.scopes[child]["wr.x"] = &emptyNameCloneDatum{
			unclonableDatum{name: "wr.x"}}

		_, err = p.SnapshotAt(child)
		require.Error(t, err)
		require.Contains(t, err.Error(), "couldn't wrap")
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

func (d *unclonableDatum) Name() string                         { return d.name }
func (d *unclonableDatum) Value() data.Value                    { return nil }
func (d *unclonableDatum) State() data.SrcState                 { return data.SrcState{} }
func (d *unclonableDatum) ItemDefinition() *data.ItemDefinition { return nil }

// failingCloneDatum adds a Clone that always fails — SnapshotAt's clone-error
// branch.
type failingCloneDatum struct {
	unclonableDatum
}

func (d *failingCloneDatum) Clone() (*data.ItemAwareElement, error) {
	return nil, errors.New("forged clone failure")
}

// failingPropCloneDatum errors through the Property clone shape.
type failingPropCloneDatum struct {
	unclonableDatum
}

func (d *failingPropCloneDatum) Clone() (*data.Property, error) {
	return nil, errors.New("forged property clone failure")
}

// failingDOCloneDatum errors through the DataObject clone shape.
type failingDOCloneDatum struct {
	unclonableDatum
}

func (d *failingDOCloneDatum) Clone() (*dataobjects.DataObject, error) {
	return nil, errors.New("forged data object clone failure")
}

// emptyNameCloneDatum clones fine but its reserved-character name breaks the
// Parameter wrap — cloneDatum's wrap-error branch.
type emptyNameCloneDatum struct {
	unclonableDatum
}

func (d *emptyNameCloneDatum) Clone() (*data.ItemAwareElement, error) {
	return data.MustItemAwareElement(
		data.MustItemDefinition(values.NewVariable(1)),
		data.ReadyDataState), nil
}

// TestOwnDataAndOpenPaths (SRD-070 FR-4): the checkpoint capture's
// enumeration surface — per-scope OWN data, no walk-up duplication.
func TestOwnDataAndOpenPaths(t *testing.T) {
	ctx := context.Background()
	root := mustPath(t, "/proc")

	p, err := New(root, nil)
	require.NoError(t, err)

	_, err = p.Commit(root, structData(t, "x", values.NewVariable(1)))
	require.NoError(t, err)

	child := mustPath(t, "/proc/sub")
	require.NoError(t, p.OpenScope(child))

	_, err = p.Commit(child, structData(t, "y", values.NewVariable(2)))
	require.NoError(t, err)

	t.Run("OpenPaths lists every open scope, sorted",
		func(t *testing.T) {
			paths := p.OpenPaths()
			require.Equal(t, []DataPath{root, child}, paths)
		})

	t.Run("OwnData carries only the scope's own book",
		func(t *testing.T) {
			own, err := p.OwnData(child)
			require.NoError(t, err)
			require.Len(t, own, 1, "y only — x is the parent's")
			require.Equal(t, "y", own[0].Name())

			// value copy: later mutation is invisible.
			_, err = p.Commit(child, structData(t, "y", values.NewVariable(9)))
			require.NoError(t, err)
			require.Equal(t, 2, own[0].Value().Get(ctx))
		})

	t.Run("an unopened path is loud",
		func(t *testing.T) {
			_, err := p.OwnData(mustPath(t, "/proc/ghost"))
			require.Error(t, err)
		})
}

// brokenDatum lacks the Clone capability — the cloneDatum guard.
type brokenDatum struct{ data.Data }

func (brokenDatum) Name() string { return "broken" }


func TestOwnDataUnclonable(t *testing.T) {
	root := mustPath(t, "/proc")

	p, err := New(root, nil)
	require.NoError(t, err)

	p.scopes[root]["broken"] = brokenDatum{}

	_, err = p.OwnData(root)
	require.Error(t, err)
	require.Contains(t, err.Error(), "isn't clonable")
}
