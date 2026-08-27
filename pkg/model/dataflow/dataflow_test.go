package dataflow_test

import (
	"context"
	"errors"
	"testing"

	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/datastore"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/dataflow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/stretchr/testify/require"
)

const owner = `task "t"[t1]`

// memStore is a one-map Data Store double.
type memStore struct {
	m      map[string]data.Data
	getErr error
	putErr error
}

func (s *memStore) Get(_ context.Context, key string) (data.Data, bool, error) {
	if s.getErr != nil {
		return nil, false, s.getErr
	}

	d, ok := s.m[key]

	return d, ok, nil
}

func (s *memStore) Put(_ context.Context, key string, d data.Data) error {
	if s.putErr != nil {
		return s.putErr
	}

	s.m[key] = d

	return nil
}

func (*memStore) Capacity() int     { return 0 }
func (*memStore) IsUnlimited() bool { return true }
func newMemStore() *memStore        { return &memStore{m: map[string]data.Data{}} }

// oneStoreReg resolves every ref to one store; a nil store is "unregistered".
type oneStoreReg struct{ store datastore.DataStore }

func (r oneStoreReg) Store(ref string) (datastore.DataStore, error) {
	if r.store == nil {
		return nil, errors.New("no store " + ref)
	}

	return r.store, nil
}

// iae builds an item-aware element over item id carrying v in state st.
func iae(id string, v any, st *data.SrcState) *data.ItemAwareElement {
	return data.MustItemAwareElement(
		data.MustItemDefinition(values.NewVariable(v), foundation.WithID(id)), st)
}

// param builds a parameter named name over item id carrying v in state st.
func param(t *testing.T, name, id string, v any, st *data.SrcState) *data.Parameter {
	t.Helper()

	p, err := data.NewParameter(name, iae(id, v, st))
	require.NoError(t, err)

	return p
}

// datum builds a Ready scope datum named name carrying v.
func datum(t *testing.T, name string, v any, st *data.SrcState) data.Data {
	t.Helper()

	p, err := data.NewParameter(name, iae(name, v, st))
	require.NoError(t, err)

	return p
}

// frame builds a plane seeded with dd and a frame for node "n" over it,
// with the store registry reg (nil = none wired).
func frame(t *testing.T, reg datastore.Registry, dd ...data.Data) *scope.Frame {
	t.Helper()

	pl, err := scope.New(scope.RootDataPath, nil)
	require.NoError(t, err)

	if len(dd) > 0 {
		_, err = pl.Commit(pl.Root(), dd...)
		require.NoError(t, err)
	}

	f, err := scope.NewFrame("track", "n", pl.Root(), pl)
	require.NoError(t, err)
	f.SetDataStores(reg)

	return f
}

// inputAssoc builds an input association: source named src → target item.
func inputAssoc(t *testing.T, src, targetItem string, opts ...options.Option) *data.Association {
	t.Helper()

	all := append([]options.Option{data.WithSource(iae(src, "", nil))}, opts...)

	a, err := data.NewAssociation(iae(targetItem, "", nil), all...)
	require.NoError(t, err)

	return a
}

// outputAssoc builds an output association: source item → target named trg.
func outputAssoc(t *testing.T, srcItem, trg string, opts ...options.Option) *data.Association {
	t.Helper()

	all := append([]options.Option{data.WithSource(iae(srcItem, "", nil))}, opts...)

	a, err := data.NewAssociation(iae(trg, "", nil), all...)
	require.NoError(t, err)

	return a
}

// instantiated returns the frame's instance of the input/output def.
func instantiated(t *testing.T, f *scope.Frame, def *data.Parameter, input bool) *data.Parameter {
	t.Helper()

	if input {
		require.NoError(t, f.InstantiateInputs([]*data.Parameter{def}))

		return f.Inputs()[0]
	}

	require.NoError(t, f.InstantiateOutputs([]*data.Parameter{def}))

	return f.Outputs()[0]
}

// TestFillInputFromScope — SRD-094 T-7's halves at the copy path: the
// scope source fills the frame input and flips it Ready; an absent or
// not-Ready source fails a required input and leaves an optional one.
func TestFillInputFromScope(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()
	required := map[string]bool{"in": true}

	t.Run("a Ready source fills the input", func(t *testing.T) {
		f := frame(t, nil, datum(t, "src", "hello", data.ReadyDataState))
		in := instantiated(t, f,
			param(t, "in", "in", "", data.UnavailableDataState), true)

		require.NoError(t, dataflow.FillInput(ctx, f,
			inputAssoc(t, "src", "in"), in, required, owner))
		require.Equal(t, "hello", in.Value().Get(ctx))
		require.Equal(t, data.ReadyDataState.Name(), in.State().Name())
	})

	t.Run("an absent source fails a required input naming the owner",
		func(t *testing.T) {
			f := frame(t, nil)
			in := instantiated(t, f,
				param(t, "in", "in", "", data.UnavailableDataState), true)

			err := dataflow.FillInput(ctx, f,
				inputAssoc(t, "src", "in"), in, required, owner)
			require.ErrorContains(t, err, "does not wait for data")
			require.ErrorContains(t, err, owner)
		})

	t.Run("a not-Ready source leaves an optional input", func(t *testing.T) {
		f := frame(t, nil, datum(t, "src", "", data.UnavailableDataState))
		in := instantiated(t, f,
			param(t, "in", "in", "", data.UnavailableDataState), true)

		require.NoError(t, dataflow.FillInput(ctx, f,
			inputAssoc(t, "src", "in"), in, map[string]bool{}, owner))
		require.Equal(t, data.UnavailableDataState.Name(), in.State().Name())
	})

}

// TestFillInputFromStore covers the Data Store half of FillInput (SRD-068
// FR-4): hit, miss on required and optional, read error, unregistered
// store, no registry.
func TestFillInputFromStore(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()
	required := map[string]bool{"in": true}
	ref := data.WithDataStoreRef("audit")

	t.Run("a stored Ready value fills the input", func(t *testing.T) {
		st := newMemStore()
		st.m["key"] = datum(t, "key", "stored", data.ReadyDataState)
		f := frame(t, oneStoreReg{st})
		in := instantiated(t, f,
			param(t, "in", "in", "", data.UnavailableDataState), true)

		require.NoError(t, dataflow.FillInput(ctx, f,
			inputAssoc(t, "key", "in", ref), in, required, owner))
		require.Equal(t, "stored", in.Value().Get(ctx))
	})

	t.Run("a missing key fails a required input, leaves an optional one",
		func(t *testing.T) {
			f := frame(t, oneStoreReg{newMemStore()})
			in := instantiated(t, f,
				param(t, "in", "in", "", data.UnavailableDataState), true)

			require.ErrorContains(t, dataflow.FillInput(ctx, f,
				inputAssoc(t, "key", "in", ref), in, required, owner),
				`DataStore "audit"`)
			require.NoError(t, dataflow.FillInput(ctx, f,
				inputAssoc(t, "key", "in", ref), in, map[string]bool{}, owner))
		})

	t.Run("a read error is always hard", func(t *testing.T) {
		st := newMemStore()
		st.getErr = errors.New("boom")
		f := frame(t, oneStoreReg{st})
		in := instantiated(t, f,
			param(t, "in", "in", "", data.UnavailableDataState), true)

		require.ErrorContains(t, dataflow.FillInput(ctx, f,
			inputAssoc(t, "key", "in", ref), in, map[string]bool{}, owner),
			"couldn't read DataStore")
	})

	t.Run("an unregistered store and no registry are hard errors",
		func(t *testing.T) {
			in := param(t, "in", "in", "", data.UnavailableDataState)

			require.ErrorContains(t, dataflow.FillInput(ctx,
				frame(t, oneStoreReg{nil}), inputAssoc(t, "key", "in", ref),
				in, nil, owner), "couldn't resolve DataStore")
			require.ErrorContains(t, dataflow.FillInput(ctx,
				frame(t, nil), inputAssoc(t, "key", "in", ref),
				in, nil, owner), "no Data Store registry")
		})

}

// TestPushOutput — SRD-094 T-6's halves at the copy path: the output lands
// in THIS frame's datum by name, a not-Ready output pushes nothing, a
// missing target and a value the target cannot hold are errors, and the
// store half writes a clone under the target name.
func TestPushOutput(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()

	t.Run("a Ready output updates the named datum in place and marks it Ready",
		func(t *testing.T) {
			// a Data Object fed by an association starts Unavailable
			f := frame(t, nil, datum(t, "sink", "", data.UnavailableDataState))
			out := instantiated(t, f,
				param(t, "out", "out", "produced", data.ReadyDataState), false)

			require.NoError(t, dataflow.PushOutput(ctx, f,
				outputAssoc(t, "out", "sink"), out, owner))

			d, err := f.GetData("sink")
			require.NoError(t, err)
			require.Equal(t, "produced", d.Value().Get(ctx))
			require.Equal(t, data.ReadyDataState.Name(), d.State().Name(),
				"produced now: readable through an input association")
		})

	t.Run("a not-Ready output pushes nothing", func(t *testing.T) {
		f := frame(t, nil, datum(t, "sink", "before", data.ReadyDataState))
		out := instantiated(t, f,
			param(t, "out", "out", "", data.UnavailableDataState), false)

		require.NoError(t, dataflow.PushOutput(ctx, f,
			outputAssoc(t, "out", "sink"), out, owner))

		d, err := f.GetData("sink")
		require.NoError(t, err)
		require.Equal(t, "before", d.Value().Get(ctx))
	})

	t.Run("a missing target is an error", func(t *testing.T) {
		out := instantiated(t, frame(t, nil),
			param(t, "out", "out", "produced", data.ReadyDataState), false)

		require.ErrorContains(t, dataflow.PushOutput(ctx, frame(t, nil),
			outputAssoc(t, "out", "sink"), out, owner), "couldn't resolve")
	})

	t.Run("the store half writes a clone under the target name", func(t *testing.T) {
		st := newMemStore()
		f := frame(t, oneStoreReg{st})
		out := instantiated(t, f,
			param(t, "out", "out", "produced", data.ReadyDataState), false)

		require.NoError(t, dataflow.PushOutput(ctx, f,
			outputAssoc(t, "out", "key", data.WithDataStoreRef("audit")),
			out, owner))

		stored := st.m["key"]
		require.NotNil(t, stored)
		require.Equal(t, "produced", stored.Value().Get(ctx))
		require.NotSame(t, out, stored)

		st.putErr = errors.New("full")
		require.ErrorContains(t, dataflow.PushOutput(ctx, f,
			outputAssoc(t, "out", "key", data.WithDataStoreRef("audit")),
			out, owner), "couldn't write")

		require.ErrorContains(t, dataflow.PushOutput(ctx, frame(t, nil),
			outputAssoc(t, "out", "key", data.WithDataStoreRef("audit")),
			out, owner), "no Data Store registry")
	})
}
