package data_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/stretchr/testify/require"
)

// TestSplitPath covers the path grammar (SRD-042 T-2): the plain-name case,
// mixed field/index steps, and every malformed shape.
func TestSplitPath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		head    string
		steps   []data.Step
		wantErr bool
	}{
		{"plain name", "order", "order", nil, false},
		{"one field", "order.total", "order",
			[]data.Step{{Field: "total"}}, false},
		{"index then field", "items[0].price", "items",
			[]data.Step{{Index: 0}, {Field: "price"}}, false},
		{"deep mix", "order.items[2].price", "order",
			[]data.Step{{Field: "items"}, {Index: 2}, {Field: "price"}}, false},
		{"leading index", "list[0]", "list",
			[]data.Step{{Index: 0}}, false},

		{"double dot", "a..b", "", nil, true},
		{"trailing dot", "a.", "", nil, true},
		{"unclosed index", "a[0", "", nil, true},
		{"non-numeric index", "a[x]", "", nil, true},
		{"negative index", "a[-1]", "", nil, true},
		{"empty index", "a[]", "", nil, true},
		{"stray close bracket in head", "a]b.c", "", nil, true},
		{"stray segment after index", "a[0]b", "", nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			head, steps, err := data.SplitPath(tt.path)
			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.head, head)
			require.Equal(t, tt.steps, steps)
		})
	}
}

// order builds "order = {id, total, items:[{price:50},{price:100}]}" — the
// worked structural value the walk/schema tests navigate.
func order(t *testing.T) *values.Record {
	t.Helper()

	item := func(p int) data.Value {
		return values.MustRecord(values.F("price", values.NewVariable(p)))
	}

	return values.MustRecord(
		values.F("id", values.NewVariable("A-1")),
		values.F("total", values.NewVariable(150)),
		values.F("items", values.NewArray[data.Value](item(50), item(100))),
		values.F("tags", values.NewArray[string]("urgent")),
	)
}

// TestWalkSteps covers navigation (SRD-042 T-3): record/list/leaf descent, a
// raw scalar-element leaf, and every classified mis-step.
func TestWalkSteps(t *testing.T) {
	ctx := context.Background()
	o := order(t)

	t.Run("navigate to a nested scalar", func(t *testing.T) {
		v, err := data.WalkSteps(ctx, o,
			[]data.Step{{Field: "items"}, {Index: 1}, {Field: "price"}})
		require.NoError(t, err)
		require.Equal(t, 100, v.Get(ctx))
	})

	t.Run("raw scalar element becomes a read-only leaf", func(t *testing.T) {
		v, err := data.WalkSteps(ctx, o,
			[]data.Step{{Field: "tags"}, {Index: 0}})
		require.NoError(t, err)
		require.Equal(t, "urgent", v.Get(ctx))
		require.Error(t, v.Update(ctx, "x")) // read-only
	})

	t.Run("field into a scalar fails", func(t *testing.T) {
		_, err := data.WalkSteps(ctx, o,
			[]data.Step{{Field: "total"}, {Field: "nope"}})
		require.Error(t, err)
	})

	t.Run("index into a record fails", func(t *testing.T) {
		_, err := data.WalkSteps(ctx, o, []data.Step{{Index: 0}})
		require.Error(t, err)
	})

	t.Run("unknown field fails", func(t *testing.T) {
		_, err := data.WalkSteps(ctx, o, []data.Step{{Field: "missing"}})
		require.Error(t, err)
	})

	t.Run("index out of range fails", func(t *testing.T) {
		_, err := data.WalkSteps(ctx, o,
			[]data.Step{{Field: "items"}, {Index: 9}})
		require.Error(t, err)
	})

	t.Run("step into a raw leaf fails", func(t *testing.T) {
		_, err := data.WalkSteps(ctx, o,
			[]data.Step{{Field: "tags"}, {Index: 0}, {Field: "x"}})
		require.Error(t, err)
	})
}

// TestNewPathData wraps a resolved leaf as a Ready, path-named Data whose name
// deliberately bypasses CheckName.
func TestNewPathData(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()
	leaf, err := data.WalkSteps(ctx, order(t),
		[]data.Step{{Field: "items"}, {Index: 0}, {Field: "price"}})
	require.NoError(t, err)

	d := data.NewPathData("order.items[0].price", leaf)
	require.Equal(t, "order.items[0].price", d.Name())
	require.Equal(t, "order.items[0].price", d.ID())
	require.Equal(t, 50, d.Value().Get(ctx))
	require.NotNil(t, d.ItemDefinition())
	require.Nil(t, d.Docs())
	require.Equal(t, data.StateReady, d.State().Name())
}

// TestScalarLeaf exercises the read-only leaf a raw collection element becomes:
// Lock/Unlock no-ops, Clone identity, Type of a typed and a nil element.
func TestScalarLeaf(t *testing.T) {
	ctx := context.Background()

	// a string element → leaf with a typed name.
	leaf, err := data.WalkSteps(ctx,
		values.MustRecord(values.F("tags",
			values.NewArray[string]("urgent"))),
		[]data.Step{{Field: "tags"}, {Index: 0}})
	require.NoError(t, err)
	require.Equal(t, "string", leaf.Type())

	leaf.Lock()
	leaf.Unlock()
	require.Equal(t, "urgent", leaf.Clone().Get(ctx))

	// a nil element → the "nil" type branch.
	nilLeaf, err := data.WalkSteps(ctx,
		values.MustRecord(values.F("vals",
			values.NewArray[any](nil))),
		[]data.Step{{Field: "vals"}, {Index: 0}})
	require.NoError(t, err)
	require.Equal(t, "nil", nilLeaf.Type())
}

// failRec is a Record whose Field always fails — it injects the error into the
// traversal helpers' defensive branches (its Keys report a field that Field
// then refuses). It embeds a real Record for the rest of the Value surface.
type failRec struct{ *values.Record }

func (failRec) Field(context.Context, string) (data.Value, error) {
	return nil, errTest
}

// failCol is a Collection whose GetAt always fails, for the same purpose.
type failCol struct{ *values.Array[data.Value] }

func (failCol) GetAt(context.Context, any) (any, error) {
	return nil, errTest
}

var errTest = errTestType("injected traversal failure")

type errTestType string

func (e errTestType) Error() string { return string(e) }

// TestTraversalErrorPropagation drives the traversal helpers' defensive error
// branches with fault-injecting values (unreachable via the real value types,
// where Keys/Count bound Field/GetAt).
func TestTraversalErrorPropagation(t *testing.T) {
	ctx := context.Background()

	badRec := failRec{values.MustRecord(values.F("x", values.NewVariable(1)))}
	badCol := failCol{values.NewArray[data.Value](values.NewVariable(1))}

	// SchemaAt over a record whose Field fails, and a list whose GetAt fails.
	_, err := data.SchemaAt(ctx, badRec, "")
	require.ErrorIs(t, err, errTest)
	_, err = data.SchemaAt(ctx, badCol, "")
	require.ErrorIs(t, err, errTest)

	// Walk directly over each failing value.
	require.ErrorIs(t, data.Walk(ctx, badRec, func(string, data.FieldInfo) {}),
		errTest)
	require.ErrorIs(t, data.Walk(ctx, badCol, func(string, data.FieldInfo) {}),
		errTest)

	// Walk over good containers holding a failing child — the recursion
	// propagates the error up (both the record and list recursion branches).
	recWithBad := values.MustRecord(values.F("bad", badRec))
	require.ErrorIs(t,
		data.Walk(ctx, recWithBad, func(string, data.FieldInfo) {}), errTest)

	colWithBad := values.NewArray[data.Value](badRec)
	require.ErrorIs(t,
		data.Walk(ctx, colWithBad, func(string, data.FieldInfo) {}), errTest)

	// resolveInto: head into a non-record, and a failing head field.
	_, err = data.SchemaAt(ctx, values.NewVariable(5), "foo")
	require.Error(t, err)
	_, err = data.SchemaAt(ctx, badRec, "x.deeper")
	require.ErrorIs(t, err, errTest)
}

// TestResolvePath covers the shared Source.Find resolver directly: plain
// pass-through, structural walk, and every error branch incl. a non-Ready head.
func TestResolvePath(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()
	o := order(t)

	readyHead := func(string) (data.Data, error) {
		return data.NewPathData("order", o), nil // Ready by construction
	}

	// plain name → the head unchanged.
	d, err := data.ResolvePath(ctx, "order", readyHead)
	require.NoError(t, err)
	require.Equal(t, "order", d.Name())

	// structural → the walked leaf.
	d, err = data.ResolvePath(ctx, "order.total", readyHead)
	require.NoError(t, err)
	require.Equal(t, 150, d.Value().Get(ctx))

	// resolveHead error propagates.
	_, err = data.ResolvePath(ctx, "x.y",
		func(string) (data.Data, error) { return nil, errTest })
	require.ErrorIs(t, err, errTest)

	// SplitPath error (malformed) and WalkSteps error (bad field).
	_, err = data.ResolvePath(ctx, "a..b", readyHead)
	require.Error(t, err)
	_, err = data.ResolvePath(ctx, "order.nope", readyHead)
	require.Error(t, err)

	// a non-Ready head cannot be navigated.
	notReady := func(string) (data.Data, error) {
		return data.MustParameter("order",
			data.MustItemAwareElement(
				data.MustItemDefinition(o), data.UnavailableDataState)), nil
	}
	_, err = data.ResolvePath(ctx, "order.total", notReady)
	require.Error(t, err)
}

// TestSchemaAtAndWalk covers shape-by-traversal (SRD-042 T-7).
func TestSchemaAtAndWalk(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()
	o := order(t)

	t.Run("SchemaAt root lists fields and kinds", func(t *testing.T) {
		fis, err := data.SchemaAt(ctx, o, "")
		require.NoError(t, err)
		require.Equal(t, []data.FieldInfo{
			{Name: "id", Kind: "scalar", Type: "string"},
			{Name: "total", Kind: "scalar", Type: "int"},
			{Name: "items", Kind: "list"},
			{Name: "tags", Kind: "list"},
		}, fis)
	})

	t.Run("SchemaAt into a list reports the element slot", func(t *testing.T) {
		fis, err := data.SchemaAt(ctx, o, "items")
		require.NoError(t, err)
		require.Len(t, fis, 1)
		require.Equal(t, "[]", fis[0].Name)
		require.Equal(t, "record", fis[0].Kind)
	})

	t.Run("SchemaAt into a nested record", func(t *testing.T) {
		fis, err := data.SchemaAt(ctx, o, "items[0]")
		require.NoError(t, err)
		require.Equal(t,
			[]data.FieldInfo{{Name: "price", Kind: "scalar", Type: "int"}}, fis)
	})

	t.Run("SchemaAt of a scalar is a valid answer", func(t *testing.T) {
		fis, err := data.SchemaAt(ctx, o, "total")
		require.NoError(t, err)
		require.Equal(t,
			[]data.FieldInfo{{Name: "", Kind: "scalar", Type: "int"}}, fis)
	})

	t.Run("SchemaAt of an empty list has unknown element", func(t *testing.T) {
		empty := values.MustRecord(
			values.F("items", values.NewArray[data.Value]()))
		fis, err := data.SchemaAt(ctx, empty, "items")
		require.NoError(t, err)
		require.Equal(t,
			[]data.FieldInfo{{Name: "[]", Kind: "unknown"}}, fis)
	})

	t.Run("SchemaAt with a bad/unresolvable path errors", func(t *testing.T) {
		_, err := data.SchemaAt(ctx, o, "a..b") // malformed
		require.Error(t, err)

		_, err = data.SchemaAt(ctx, o, "total.nope") // field into scalar
		require.Error(t, err)

		_, err = data.SchemaAt(ctx, o, "[0]") // index into a record
		require.Error(t, err)
	})

	t.Run("Walk visits every node", func(t *testing.T) {
		seen := map[string]string{} // path -> kind
		require.NoError(t, data.Walk(ctx, o,
			func(path string, fi data.FieldInfo) {
				seen[path] = fi.Kind
			}))

		require.Equal(t, "record", seen[""])
		require.Equal(t, "scalar", seen[".id"])
		require.Equal(t, "list", seen[".items"])
		require.Equal(t, "record", seen[".items[0]"])
		require.Equal(t, "scalar", seen[".items[1].price"])
		require.Equal(t, "scalar", seen[".tags[0]"])
	})
}

// TestParsePath covers the write-side path parser (SRD-043 T-1): the leading
// segment is itself a step — a field OR an index — and an empty path is nil.
func TestParsePath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		steps   []data.Step
		wantErr bool
	}{
		{"empty", "", nil, false},
		{"leading field", "order", []data.Step{{Field: "order"}}, false},
		{"field then field", "order.total",
			[]data.Step{{Field: "order"}, {Field: "total"}}, false},
		{"field index field", "items[0].price",
			[]data.Step{{Field: "items"}, {Index: 0}, {Field: "price"}}, false},
		{"leading index", "[0].total",
			[]data.Step{{Index: 0}, {Field: "total"}}, false},
		{"leading index only", "[2]", []data.Step{{Index: 2}}, false},

		{"double dot", "a..b", nil, true},
		{"unclosed leading index", "[0", nil, true},
		{"non-numeric leading index", "[x]", nil, true},
		{"stray segment after index", "a[0]b", nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			steps, err := data.ParsePath(tt.path)
			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.steps, steps)
		})
	}
}
