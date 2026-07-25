package activities

import (
	"context"
	"errors"
	"testing"

	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/datastore"
	"github.com/dr-dobermann/gobpm/pkg/datastore/memstore"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	datastores "github.com/dr-dobermann/gobpm/pkg/model/data_stores"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
)

// errStore is a DataStore whose Get/Put fail with a configured error — to drive
// the reroute's read/write error branches.
type errStore struct {
	getErr error
	putErr error
}

func (e errStore) Get(context.Context, string) (data.Data, bool, error) {
	return nil, false, e.getErr
}
func (e errStore) Put(context.Context, string, data.Data) error { return e.putErr }
func (errStore) Capacity() int                                  { return 0 }
func (errStore) IsUnlimited() bool                              { return true }

// oneStoreReg is a Registry resolving every ref to one store.
type oneStoreReg struct{ store datastore.DataStore }

func (r oneStoreReg) Store(string) (datastore.DataStore, error) {
	return r.store, nil
}

// readyIAE builds a Ready item-aware datum over item id carrying v.
func readyIAE(id string, v any) *data.ItemAwareElement {
	return data.MustItemAwareElement(
		data.MustItemDefinition(values.NewVariable(v), foundation.WithID(id)),
		data.ReadyDataState)
}

// storeRef builds a DataStoreReference named name over item id, targeting ref.
func storeRef(
	t *testing.T, name, ref, id string,
) *datastores.DataStoreReference {
	t.Helper()

	r, err := datastores.New(name, ref,
		data.MustItemDefinition(values.NewVariable(0), foundation.WithID(id)),
		data.ReadyDataState)
	require.NoError(t, err)

	return r
}

// TestTaskDataStoreRouting covers the DataStoreReference reroute (SRD-064 FR-4):
// a task output association writes the engine-global store, an input reads it,
// and an unresolvable store fails loud.
func TestTaskDataStoreRouting(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()

	newFrameWith := func(t *testing.T, tsk *task, reg *memstore.Registry) *scope.Frame {
		t.Helper()

		pl, err := scope.New(scope.RootDataPath, nil)
		require.NoError(t, err)
		f, err := scope.NewFrame("track-t", tsk.ID(), pl.Root(), pl)
		require.NoError(t, err)

		if reg != nil {
			f.SetDataStores(reg)
		}

		return f
	}

	t.Run("output association writes the engine store", func(t *testing.T) {
		tsk := newIOTask(t, "in-x", "out-x", 1, 0)
		reg := memstore.NewRegistry()
		require.NoError(t, reg.Register("orders", memstore.New()))
		f := newFrameWith(t, tsk, reg)

		ref := storeRef(t, "total", "orders", "out-x")
		require.NoError(t, ref.AssociateSource(tsk, []string{"out-x"}, nil))

		require.NoError(t, tsk.LoadData(ctx, f))
		require.NoError(t, f.Put(data.MustParameter("res", readyIAE("out-x", 99))))
		require.NoError(t, tsk.UploadData(ctx, f))

		store, err := reg.Store("orders")
		require.NoError(t, err)
		d, ok, err := store.Get(ctx, "total")
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, 99, d.Value().Get(ctx))
	})

	t.Run("input association reads the engine store", func(t *testing.T) {
		tsk := newIOTask(t, "in-y", "out-y", 0, 0)
		store := memstore.New()
		require.NoError(t, store.Put(ctx, "seed", readyIAE("in-y", 77)))
		reg := memstore.NewRegistry()
		require.NoError(t, reg.Register("audit", store))
		f := newFrameWith(t, tsk, reg)

		ref := storeRef(t, "seed", "audit", "in-y")
		require.NoError(t, ref.AssociateTarget(tsk, nil))

		require.NoError(t, tsk.LoadData(ctx, f))
		in, err := f.GetDataByID("in-y")
		require.NoError(t, err)
		require.Equal(t, 77, in.Value().Get(ctx))
	})

	t.Run("unregistered store fails loud on output", func(t *testing.T) {
		tsk := newIOTask(t, "in-x", "out-x", 1, 0)
		f := newFrameWith(t, tsk, memstore.NewRegistry()) // empty registry

		ref := storeRef(t, "total", "missing", "out-x")
		require.NoError(t, ref.AssociateSource(tsk, []string{"out-x"}, nil))

		require.NoError(t, tsk.LoadData(ctx, f))
		require.NoError(t, f.Put(data.MustParameter("res", readyIAE("out-x", 99))))
		require.Error(t, tsk.UploadData(ctx, f))
	})

	t.Run("nil registry fails loud on output", func(t *testing.T) {
		tsk := newIOTask(t, "in-x", "out-x", 1, 0)
		f := newFrameWith(t, tsk, nil) // no registry wired

		ref := storeRef(t, "total", "orders", "out-x")
		require.NoError(t, ref.AssociateSource(tsk, []string{"out-x"}, nil))

		require.NoError(t, tsk.LoadData(ctx, f))
		require.NoError(t, f.Put(data.MustParameter("res", readyIAE("out-x", 99))))
		require.Error(t, tsk.UploadData(ctx, f))
	})

	t.Run("unregistered store fails loud on input", func(t *testing.T) {
		tsk := newIOTask(t, "in-y", "out-y", 0, 0)
		f := newFrameWith(t, tsk, memstore.NewRegistry()) // empty registry

		ref := storeRef(t, "seed", "missing", "in-y")
		require.NoError(t, ref.AssociateTarget(tsk, nil))

		require.Error(t, tsk.LoadData(ctx, f))
	})

	t.Run("absent store value fails a required input", func(t *testing.T) {
		tsk := newIOTask(t, "in-y", "out-y", 0, 0)
		reg := memstore.NewRegistry()
		require.NoError(t, reg.Register("audit", memstore.New())) // empty store
		f := newFrameWith(t, tsk, reg)

		ref := storeRef(t, "seed", "audit", "in-y")
		require.NoError(t, ref.AssociateTarget(tsk, nil))

		require.Error(t, tsk.LoadData(ctx, f))
	})
}

// TestTaskOpErr covers the single-line operation-error builder.
func TestTaskOpErr(t *testing.T) {
	tsk, err := newTask("op-err", WithoutParams())
	require.NoError(t, err)
	require.Error(t, tsk.opErr("boom", errors.New("cause")))
}

// TestTaskDataStoreRoutingErrorBranches drives the reroute's store read/write
// error branches with a failing store, and the input-fill type check.
func TestTaskDataStoreRoutingErrorBranches(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()

	frame := func(t *testing.T, tsk *task, reg datastore.Registry) *scope.Frame {
		t.Helper()

		pl, err := scope.New(scope.RootDataPath, nil)
		require.NoError(t, err)
		f, err := scope.NewFrame("track-t", tsk.ID(), pl.Root(), pl)
		require.NoError(t, err)
		f.SetDataStores(reg)

		return f
	}

	t.Run("store read error surfaces on input", func(t *testing.T) {
		tsk := newIOTask(t, "in-y", "out-y", 0, 0)
		f := frame(t, tsk, oneStoreReg{errStore{getErr: errors.New("boom")}})

		ref := storeRef(t, "seed", "audit", "in-y")
		require.NoError(t, ref.AssociateTarget(tsk, nil))

		require.Error(t, tsk.LoadData(ctx, f))
	})

	t.Run("store write error surfaces on output", func(t *testing.T) {
		tsk := newIOTask(t, "in-x", "out-x", 1, 0)
		f := frame(t, tsk, oneStoreReg{errStore{putErr: errors.New("boom")}})

		ref := storeRef(t, "total", "orders", "out-x")
		require.NoError(t, ref.AssociateSource(tsk, []string{"out-x"}, nil))

		require.NoError(t, tsk.LoadData(ctx, f))
		require.NoError(t, f.Put(data.MustParameter("res", readyIAE("out-x", 99))))
		require.Error(t, tsk.UploadData(ctx, f))
	})
}
