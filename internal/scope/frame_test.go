package scope

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/stretchr/testify/require"
)

// testParam builds an input/output definition carrying val.
func testParam(t *testing.T, name string, val any) *data.Parameter {
	t.Helper()

	_ = data.CreateDefaultStates()

	p, err := data.NewParameter(
		name,
		data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable(val)),
			data.ReadyDataState))
	require.NoError(t, err)

	return p
}

// testProp builds a property definition carrying val.
func testProp(t *testing.T, name string, val any) *data.Property {
	t.Helper()

	_ = data.CreateDefaultStates()

	pr, err := data.NewProperty(
		name,
		data.MustItemDefinition(values.NewVariable(val)),
		data.ReadyDataState)
	require.NoError(t, err)

	return pr
}

// newTestFrame builds a plane rooted at /proc and an open frame on it.
func newTestFrame(t *testing.T) (*Scope, *Frame) {
	t.Helper()

	p, err := New(mustPath(t, "/proc"), nil)
	require.NoError(t, err)

	f, err := NewFrame("track-1", "node-1", p.Root(), p)
	require.NoError(t, err)

	return p, f
}

func TestNewFrame(t *testing.T) {
	p, err := New(mustPath(t, "/proc"), nil)
	require.NoError(t, err)

	t.Run("empty ids rejected", func(t *testing.T) {
		_, err := NewFrame("", "node", p.Root(), p)
		require.Error(t, err)

		_, err = NewFrame("track", "   ", p.Root(), p)
		require.Error(t, err)
	})

	t.Run("nil plane rejected", func(t *testing.T) {
		_, err := NewFrame("track", "node", p.Root(), nil)
		require.Error(t, err)
	})

	t.Run("invalid and unopened scopes rejected", func(t *testing.T) {
		_, err := NewFrame("track", "node", DataPath("bad"), p)
		require.Error(t, err)

		_, err = NewFrame("track", "node", mustPath(t, "/proc/ghost"), p)
		require.Error(t, err)
	})

	t.Run("identity is exposed", func(t *testing.T) {
		f, err := NewFrame("track-9", "node-7", p.Root(), p)
		require.NoError(t, err)
		require.Equal(t, "track-9", f.TrackID())
		require.Equal(t, "node-7", f.NodeID())
	})
}

func TestFrameInstantiation(t *testing.T) {
	ctx := context.Background()

	t.Run("instances share identity, not value", func(t *testing.T) {
		_, f := newTestFrame(t)
		def := testParam(t, "x", 1)

		require.NoError(t, f.InstantiateInputs([]*data.Parameter{def}))

		inst, err := f.GetData("x")
		require.NoError(t, err)
		require.Equal(t, def.ItemDefinition().ID(),
			inst.ItemDefinition().ID())

		// mutating the instance leaves the definition untouched.
		require.NoError(t,
			inst.Value().Update(ctx, 99))
		require.Equal(t, 1, def.Value().Get(ctx))
		require.Equal(t, 99, inst.Value().Get(ctx))
	})

	t.Run("two frames get independent instances", func(t *testing.T) {
		p, err := New(mustPath(t, "/proc"), nil)
		require.NoError(t, err)

		def := testParam(t, "x", 1)

		fA, err := NewFrame("track-A", "node-1", p.Root(), p)
		require.NoError(t, err)
		fB, err := NewFrame("track-B", "node-1", p.Root(), p)
		require.NoError(t, err)

		require.NoError(t, fA.InstantiateInputs([]*data.Parameter{def}))
		require.NoError(t, fB.InstantiateInputs([]*data.Parameter{def}))

		instA, err := fA.GetData("x")
		require.NoError(t, err)
		require.NoError(t, instA.Value().Update(ctx, 42))

		instB, err := fB.GetData("x")
		require.NoError(t, err)
		require.Equal(t, 1, instB.Value().Get(ctx), "no cross-frame clobber")
	})

	t.Run("nil and duplicate definitions rejected", func(t *testing.T) {
		_, f := newTestFrame(t)

		require.Error(t, f.InstantiateInputs([]*data.Parameter{nil}))

		def := testParam(t, "x", 1)
		require.NoError(t, f.InstantiateOutputs([]*data.Parameter{def}))
		require.Error(t, f.InstantiateOutputs([]*data.Parameter{def}))
	})

	t.Run("under-specified definition fails to instantiate",
		func(t *testing.T) {
			_, f := newTestFrame(t)

			// an ItemDefinition without a value is legal BPMN
			// (under-specified) but can't produce a frame instance.
			bare, err := data.NewItemAwareElement(
				data.MustItemDefinition(nil), data.ReadyDataState)
			require.NoError(t, err)

			def, err := data.NewParameter("bare", bare)
			require.NoError(t, err)

			require.Error(t,
				f.InstantiateInputs([]*data.Parameter{def}))

			// a value-less property can't be constructed (FIX-018); a bare
			// zero-value struct is the only remaining value-less source, and
			// LoadProperties still rejects it (the clone precondition).
			require.Error(t,
				f.LoadProperties([]*data.Property{{}}))
		})

	t.Run("properties are frame-local", func(t *testing.T) {
		pl, f := newTestFrame(t)

		require.NoError(t,
			f.LoadProperties([]*data.Property{testProp(t, "cnt", 7)}))
		require.Error(t,
			f.LoadProperties([]*data.Property{nil}))

		d, err := f.GetData("cnt")
		require.NoError(t, err)
		require.Equal(t, "cnt", d.Name())

		// not in the container scope...
		_, err = pl.GetData(pl.Root(), "cnt")
		require.Error(t, err)

		// ...and not committed either.
		require.NoError(t, f.Commit())
		_, err = pl.GetData(pl.Root(), "cnt")
		require.Error(t, err)
	})
}

func TestFrameResolution(t *testing.T) {
	pl, f := newTestFrame(t)

	require.NoError(t, pl.Commit(pl.Root(), testData(t, "x", "container")))
	require.NoError(t, pl.Commit(pl.Root(), testData(t, "only-up", 5)))

	t.Run("frame input shadows container data", func(t *testing.T) {
		require.NoError(t,
			f.InstantiateInputs([]*data.Parameter{testParam(t, "x", "frame")}))

		d, err := f.GetData("x")
		require.NoError(t, err)
		require.Equal(t, "frame",
			d.Value().Get(context.Background()))
	})

	t.Run("fallthrough to the container walk", func(t *testing.T) {
		d, err := f.GetData("only-up")
		require.NoError(t, err)
		require.Equal(t, "only-up", d.Name())
	})

	t.Run("id lookup: frame first, then container", func(t *testing.T) {
		up := testData(t, "by-id", 3)
		require.NoError(t, pl.Commit(pl.Root(), up))

		d, err := f.GetDataByID(up.ItemDefinition().ID())
		require.NoError(t, err)
		require.Equal(t, "by-id", d.Name())

		_, err = f.GetDataByID("ghost-id")
		require.Error(t, err)
	})

	t.Run("empty lookup args rejected", func(t *testing.T) {
		_, err := f.GetData(" ")
		require.Error(t, err)

		_, err = f.GetDataByID("")
		require.Error(t, err)
	})

	t.Run("readable frame groups resolve, outputs don't", func(t *testing.T) {
		_, f := newTestFrame(t)

		in := testParam(t, "in", 1)
		out := testParam(t, "out", 2)
		require.NoError(t, f.InstantiateInputs([]*data.Parameter{in}))
		require.NoError(t, f.InstantiateOutputs([]*data.Parameter{out}))
		require.NoError(t,
			f.LoadProperties([]*data.Property{testProp(t, "prop", 3)}))
		require.NoError(t, f.Put(testData(t, "put", 4)))

		for _, n := range []string{"in", "prop", "put"} {
			d, err := f.GetData(n)
			require.NoError(t, err)
			require.Equal(t, n, d.Name())
		}

		// outputs are write targets, not sources: they never resolve —
		// otherwise a not-yet-filled output would shadow the data meant to
		// fill it at the producer stage.
		_, err := f.GetData("out")
		require.Error(t, err)

		_, err = f.GetDataByID(out.ItemDefinition().ID())
		require.Error(t, err)

		// id lookup hits the frame before the container.
		d, err := f.GetDataByID(in.ItemDefinition().ID())
		require.NoError(t, err)
		require.Equal(t, "in", d.Name())

		// the instance accessors expose what was instantiated.
		require.Len(t, f.Inputs(), 1)
		require.Len(t, f.Outputs(), 1)
		require.Equal(t, "in", f.Inputs()[0].Name())
		require.Equal(t, "out", f.Outputs()[0].Name())
	})
}

func TestFrameSourceResolution(t *testing.T) {
	pl, err := New(mustPath(t, "/proc"), &stubSupplier{t: t})
	require.NoError(t, err)

	f, err := NewFrame("track-1", "node-1", pl.Root(), pl)
	require.NoError(t, err)

	require.NoError(t, pl.Commit(pl.Root(), testData(t, "x", "container")))

	t.Run("path-qualified name resolves via the source", func(t *testing.T) {
		d, err := f.GetData(RuntimeVarsSegment + PathSeparator + "alive")
		require.NoError(t, err)
		require.Equal(t, "alive", d.Name())
	})

	t.Run("unknown source is an error", func(t *testing.T) {
		_, err := f.GetData("BUSINESS" + PathSeparator + "order")
		require.Error(t, err)
	})

	t.Run("plain name still walks the default scope", func(t *testing.T) {
		d, err := f.GetData("x")
		require.NoError(t, err)
		require.Equal(t, "x", d.Name())
	})

	t.Run("a source never intersects a same-named user variable", func(t *testing.T) {
		// the supplier serves "alive"; a user property of the same name is
		// independent — the plain name reads the default scope, the qualified
		// name reads the source (NFR-2).
		require.NoError(t,
			pl.Commit(pl.Root(), testData(t, "alive", "user-owned")))

		user, err := f.GetData("alive")
		require.NoError(t, err)
		require.Equal(t, "user-owned",
			user.Value().Get(context.Background()))

		runtime, err := f.GetData(RuntimeVarsSegment + PathSeparator + "alive")
		require.NoError(t, err)
		require.Equal(t, true,
			runtime.Value().Get(context.Background()))
	})
}

func TestFrameDiscovery(t *testing.T) {
	pl, err := New(mustPath(t, "/proc"), &stubSupplier{t: t})
	require.NoError(t, err)

	// a child container scope under the root
	sub := mustPath(t, "/proc/sub")
	require.NoError(t, pl.OpenScope(sub))

	require.NoError(t, pl.Commit(pl.Root(), testData(t, "root-var", 1)))
	require.NoError(t, pl.Commit(sub, testData(t, "sub-var", 2)))

	f, err := NewFrame("track-1", "node-1", sub, pl)
	require.NoError(t, err)

	t.Run("GetSources delegates to the plane", func(t *testing.T) {
		require.Equal(t, []string{RuntimeVarsSegment}, f.GetSources())
	})

	t.Run("List of a source returns its names", func(t *testing.T) {
		names, err := f.List(RuntimeVarsSegment)
		require.NoError(t, err)
		require.Equal(t, []string{"alive"}, names)
	})

	t.Run("List of the default scope walks parent-ward", func(t *testing.T) {
		names, err := f.List("")
		require.NoError(t, err)
		require.Equal(t, []string{"root-var", "sub-var"}, names)
	})
}

func TestFrameCommitAndDiscard(t *testing.T) {
	t.Run("commit flushes outputs and puts", func(t *testing.T) {
		pl, f := newTestFrame(t)

		require.NoError(t,
			f.InstantiateOutputs([]*data.Parameter{testParam(t, "out", 1)}))
		require.NoError(t, f.Put(testData(t, "result", "done")))

		require.NoError(t, f.Commit())

		for _, n := range []string{"out", "result"} {
			_, err := pl.GetData(pl.Root(), n)
			require.NoError(t, err, "committed %q must be in the container", n)
		}
	})

	t.Run("put validation", func(t *testing.T) {
		_, f := newTestFrame(t)

		require.Error(t, f.Put(nil))
		require.Error(t, f.Put(unnamedData{Data: testData(t, "n", 1)}))
		require.NoError(t, f.Put())
	})

	t.Run("sealed after commit", func(t *testing.T) {
		_, f := newTestFrame(t)

		require.NoError(t, f.Commit())
		require.Error(t, f.Commit())
		require.Error(t, f.Put(testData(t, "late", 1)))
		require.Error(t,
			f.InstantiateInputs([]*data.Parameter{testParam(t, "x", 1)}))
		require.Error(t,
			f.LoadProperties([]*data.Property{testProp(t, "p", 1)}))
	})

	t.Run("discard leaves no trace and seals", func(t *testing.T) {
		pl, f := newTestFrame(t)

		require.NoError(t, f.Put(testData(t, "ghost", 1)))

		f.Discard()
		f.Discard() // idempotent

		require.Error(t, f.Commit(), "no commit after discard")

		_, err := pl.GetData(pl.Root(), "ghost")
		require.Error(t, err, "discarded data must not reach the container")
	})

	t.Run("commit propagates plane failures", func(t *testing.T) {
		pl, err := New(mustPath(t, "/proc"), nil)
		require.NoError(t, err)

		child := mustPath(t, "/proc/sub")
		require.NoError(t, pl.OpenScope(child))

		f, err := NewFrame("track", "node", child, pl)
		require.NoError(t, err)

		require.NoError(t, f.Put(testData(t, "x", 1)))

		// the target scope disappears before the commit.
		require.NoError(t, pl.CloseScope(child))
		require.Error(t, f.Commit())
	})
}
