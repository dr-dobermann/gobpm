package dataflow_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/generated/mockdata"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/dataflow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// fakeEngine evaluates through the test's own closure, so a test can assert
// WHAT the expression was handed — the frame-backed data.Source is the
// contract under test (ADR-011 §2.4), not any particular language.
type fakeEngine struct {
	eval func(context.Context, data.Source) (data.Value, error)
}

func (fakeEngine) Type() string        { return "##Fake" }
func (fakeEngine) Languages() []string { return []string{"fake"} }
func (e fakeEngine) Evaluate(
	ctx context.Context, _ data.FormalExpression, src data.Source,
) (data.Value, error) {
	return e.eval(ctx, src)
}

// anExpr is a stand-in FormalExpression: the fake engine never reads it.
func anExpr(t *testing.T) data.FormalExpression {
	t.Helper()

	return mockdata.NewMockFormalExpression(t)
}

// record builds a Ready scope datum named name carrying the given fields.
func record(t *testing.T, name string, fields ...values.RecordField) data.Data {
	t.Helper()

	r, err := values.NewRecord(fields...)
	require.NoError(t, err)

	p, err := data.NewParameter(name,
		data.MustItemAwareElement(
			data.MustItemDefinition(r, foundation.WithID(name)),
			data.ReadyDataState))
	require.NoError(t, err)

	return p
}

// assign builds one from→to mapping.
func assign(t *testing.T, to string) *data.Assignment {
	t.Helper()

	a, err := data.NewAssignment(anExpr(t), to)
	require.NoError(t, err)

	return a
}

// TestFillInputTransformation is SRD-097 T-4: an input association's
// transformation is evaluated against the frame and REPLACES the target,
// and the expression reads its sources by structural path.
func TestFillInputTransformation(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()

	f := frame(t, nil,
		record(t, "order", values.F("total", values.NewVariable(10))),
		datum(t, "rate", 3, data.ReadyDataState))

	var sawTotal, sawRate int

	f.SetExpressionEngine(fakeEngine{
		eval: func(ctx context.Context, src data.Source) (data.Value, error) {
			// the path form the ADR promises, resolved through the frame
			d, err := src.Find(ctx, "order.total")
			require.NoError(t, err)
			sawTotal = d.Value().Get(ctx).(int)

			d, err = src.Find(ctx, "rate")
			require.NoError(t, err)
			sawRate = d.Value().Get(ctx).(int)

			return values.NewVariable(sawTotal * sawRate), nil
		}})

	ia := inputAssoc(t, "order", "amount",
		data.WithSource(iae("rate", "", nil)),
		data.WithTransformation(anExpr(t)))

	dst := instantiated(t, f, param(t, "amount", "amount", 0, nil), true)

	require.NoError(t, dataflow.FillInput(ctx, f, ia, dst, nil, owner))

	require.Equal(t, 10, sawTotal)
	require.Equal(t, 3, sawRate)
	require.Equal(t, 30, dst.Value().Get(ctx),
		"the transformation's result replaces the target")
	require.Equal(t, data.ReadyDataState.Name(), dst.State().Name())
	require.Len(t, f.DataMovements(), 2, "both sources are recorded")
}

// TestPushOutputAssignments is SRD-097 T-5: each assignment writes its own
// result at its own path inside the target, in declaration order; a
// step-less `to` replaces the whole value.
func TestPushOutputAssignments(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()

	t.Run("nested writes, in order", func(t *testing.T) {
		f := frame(t, nil, record(t, "order",
			values.F("status", values.NewVariable("new")),
			values.F("code", values.NewVariable(0))))

		var calls int

		f.SetExpressionEngine(fakeEngine{
			eval: func(context.Context, data.Source) (data.Value, error) {
				calls++
				if calls == 1 {
					return values.NewVariable("done"), nil
				}

				return values.NewVariable(7), nil
			}})

		oa := outputAssoc(t, "result", "order",
			data.WithAssignments(
				assign(t, "order.status"),
				assign(t, "order.code")))

		src := instantiated(t, f,
			param(t, "result", "result", 1, data.ReadyDataState), false)

		require.NoError(t, dataflow.PushOutput(ctx, f, oa, src, owner))

		d, err := f.GetData("order.status")
		require.NoError(t, err)
		require.Equal(t, "done", d.Value().Get(ctx))

		d, err = f.GetData("order.code")
		require.NoError(t, err)
		require.Equal(t, 7, d.Value().Get(ctx))
		require.Equal(t, 2, calls, "one evaluation per assignment")
	})

	t.Run("a step-less to replaces the whole value", func(t *testing.T) {
		f := frame(t, nil, datum(t, "note", "old", data.ReadyDataState))
		f.SetExpressionEngine(fakeEngine{
			eval: func(context.Context, data.Source) (data.Value, error) {
				return values.NewVariable("new"), nil
			}})

		oa := outputAssoc(t, "result", "note",
			data.WithAssignments(assign(t, "note")))

		src := instantiated(t, f,
			param(t, "result", "result", 1, data.ReadyDataState), false)

		require.NoError(t, dataflow.PushOutput(ctx, f, oa, src, owner))

		d, err := f.GetData("note")
		require.NoError(t, err)
		require.Equal(t, "new", d.Value().Get(ctx))
	})
}

// TestExpressionShapeOverDataStore is SRD-097 T-6: a transformation whose
// target is a Data Store key writes the store, exactly as a plain copy does.
func TestExpressionShapeOverDataStore(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()
	store := newMemStore()
	f := frame(t, oneStoreReg{store: store},
		datum(t, "result", 5, data.ReadyDataState))

	f.SetExpressionEngine(fakeEngine{
		eval: func(context.Context, data.Source) (data.Value, error) {
			return values.NewVariable(42), nil
		}})

	oa := outputAssoc(t, "result", "archive",
		data.WithDataStoreRef("store-1"),
		data.WithTransformation(anExpr(t)))

	src := instantiated(t, f,
		param(t, "result", "result", 5, data.ReadyDataState), false)

	require.NoError(t, dataflow.PushOutput(ctx, f, oa, src, owner))

	d, ok := store.m["archive"]
	require.True(t, ok, "the store holds the association's target key")
	require.Equal(t, 42, d.Value().Get(ctx))
}

// TestExpressionAssociationFailsFast is SRD-097 T-7: the three ways an
// expression-bearing association refuses, each naming what a reader needs.
func TestExpressionAssociationFailsFast(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()

	t.Run("an unavailable source gates a required input", func(t *testing.T) {
		f := frame(t, nil, datum(t, "order", 1, data.UndefinedSrcState))
		f.SetExpressionEngine(fakeEngine{
			eval: func(context.Context, data.Source) (data.Value, error) {
				t.Fatal("the expression must not run on an unavailable source")

				return nil, nil
			}})

		ia := inputAssoc(t, "order", "amount",
			data.WithTransformation(anExpr(t)))
		dst := instantiated(t, f, param(t, "amount", "amount", 0, nil), true)

		err := dataflow.FillInput(ctx, f, ia, dst,
			map[string]bool{"amount": true}, owner)
		require.ErrorContains(t, err, "is unavailable")
		require.ErrorContains(t, err, "does not wait")
	})

	t.Run("an optional input with an unavailable source is skipped",
		func(t *testing.T) {
			f := frame(t, nil, datum(t, "order", 1, data.UndefinedSrcState))

			ia := inputAssoc(t, "order", "amount",
				data.WithTransformation(anExpr(t)))
			dst := instantiated(t, f, param(t, "amount", "amount", 0, nil), true)

			require.NoError(t, dataflow.FillInput(ctx, f, ia, dst, nil, owner))
		})

	t.Run("no engine wired names the association", func(t *testing.T) {
		f := frame(t, nil, datum(t, "order", 1, data.ReadyDataState))

		ia := inputAssoc(t, "order", "amount",
			data.WithTransformation(anExpr(t)),
			foundation.WithID("a-77"))
		dst := instantiated(t, f, param(t, "amount", "amount", 0, nil), true)

		err := dataflow.FillInput(ctx, f, ia, dst, nil, owner)
		require.ErrorContains(t, err, "no expression engine")
		require.ErrorContains(t, err, "a-77")
	})

	t.Run("an assignment writing outside the target is refused",
		func(t *testing.T) {
			f := frame(t, nil, datum(t, "order", 1, data.ReadyDataState))
			f.SetExpressionEngine(fakeEngine{
				eval: func(context.Context, data.Source) (data.Value, error) {
					return values.NewVariable(1), nil
				}})

			oa := outputAssoc(t, "result", "order",
				data.WithAssignments(assign(t, "elsewhere.field")))
			src := instantiated(t, f,
				param(t, "result", "result", 1, data.ReadyDataState), false)

			err := dataflow.PushOutput(ctx, f, oa, src, owner)
			require.ErrorContains(t, err, "elsewhere.field")
			require.ErrorContains(t, err, "target is")
		})

	t.Run("an evaluation failure carries its cause", func(t *testing.T) {
		f := frame(t, nil, datum(t, "order", 1, data.ReadyDataState))
		f.SetExpressionEngine(fakeEngine{
			eval: func(context.Context, data.Source) (data.Value, error) {
				return nil, errors.New("boom")
			}})

		ia := inputAssoc(t, "order", "amount",
			data.WithTransformation(anExpr(t)))
		dst := instantiated(t, f, param(t, "amount", "amount", 0, nil), true)

		require.ErrorContains(t,
			dataflow.FillInput(ctx, f, ia, dst, nil, owner), "boom")
	})

	t.Run("an assignment's own evaluation failure carries its cause",
		func(t *testing.T) {
			f := frame(t, nil, datum(t, "note", "old", data.ReadyDataState))
			f.SetExpressionEngine(fakeEngine{
				eval: func(context.Context, data.Source) (data.Value, error) {
					return nil, errors.New("assign-boom")
				}})

			oa := outputAssoc(t, "result", "note",
				data.WithAssignments(assign(t, "note")))
			src := instantiated(t, f,
				param(t, "result", "result", 1, data.ReadyDataState), false)

			require.ErrorContains(t,
				dataflow.PushOutput(ctx, f, oa, src, owner), "assign-boom")
		})

	t.Run("a structural write into a scalar is refused", func(t *testing.T) {
		f := frame(t, nil, datum(t, "note", "flat", data.ReadyDataState))
		f.SetExpressionEngine(fakeEngine{
			eval: func(context.Context, data.Source) (data.Value, error) {
				return values.NewVariable("x"), nil
			}})

		oa := outputAssoc(t, "result", "note",
			data.WithAssignments(assign(t, "note.field")))
		src := instantiated(t, f,
			param(t, "result", "result", 1, data.ReadyDataState), false)

		require.ErrorContains(t,
			dataflow.PushOutput(ctx, f, oa, src, owner), "note.field")
	})

	t.Run("an unresolvable output target is refused", func(t *testing.T) {
		f := frame(t, nil)
		f.SetExpressionEngine(fakeEngine{
			eval: func(context.Context, data.Source) (data.Value, error) {
				return values.NewVariable(1), nil
			}})

		oa := outputAssoc(t, "result", "missing",
			data.WithTransformation(anExpr(t)))
		src := instantiated(t, f,
			param(t, "result", "result", 1, data.ReadyDataState), false)

		require.ErrorContains(t,
			dataflow.PushOutput(ctx, f, oa, src, owner), "missing")
	})

	t.Run("a store target with no registry is refused", func(t *testing.T) {
		f := frame(t, nil, datum(t, "result", 1, data.ReadyDataState))
		f.SetExpressionEngine(fakeEngine{
			eval: func(context.Context, data.Source) (data.Value, error) {
				return values.NewVariable(1), nil
			}})

		oa := outputAssoc(t, "result", "archive",
			data.WithDataStoreRef("store-1"),
			data.WithTransformation(anExpr(t)))
		src := instantiated(t, f,
			param(t, "result", "result", 1, data.ReadyDataState), false)

		require.ErrorContains(t,
			dataflow.PushOutput(ctx, f, oa, src, owner), "Data Store registry")
	})

	t.Run("a store target with no engine is refused", func(t *testing.T) {
		store := newMemStore()
		f := frame(t, oneStoreReg{store: store},
			datum(t, "result", 1, data.ReadyDataState))

		oa := outputAssoc(t, "result", "archive",
			data.WithDataStoreRef("store-1"),
			data.WithTransformation(anExpr(t)),
			foundation.WithID("a-88"))
		src := instantiated(t, f,
			param(t, "result", "result", 1, data.ReadyDataState), false)

		err := dataflow.PushOutput(ctx, f, oa, src, owner)
		require.ErrorContains(t, err, "no expression engine")
		require.ErrorContains(t, err, "a-88")
		require.Empty(t, store.m, "nothing is stored when nothing evaluated")
	})

	t.Run("an expression producing no value is refused", func(t *testing.T) {
		f := frame(t, nil, datum(t, "order", 1, data.ReadyDataState))
		f.SetExpressionEngine(fakeEngine{
			eval: func(context.Context, data.Source) (data.Value, error) {
				return nil, nil
			}})

		ia := inputAssoc(t, "order", "amount",
			data.WithTransformation(anExpr(t)))
		dst := instantiated(t, f, param(t, "amount", "amount", 0, nil), true)

		require.ErrorContains(t,
			dataflow.FillInput(ctx, f, ia, dst, nil, owner), "no value")
	})
}

// TestPlainAssociationUnchanged is SRD-097 T-8 / NFR-2: an association with
// neither shape takes the copy path it always took — the value copied, one
// movement recorded, no engine consulted.
func TestPlainAssociationUnchanged(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()
	f := frame(t, nil, datum(t, "order", 11, data.ReadyDataState))
	// No engine wired at all: a plain association must not reach for one.

	ia := inputAssoc(t, "order", "amount")
	dst := instantiated(t, f, param(t, "amount", "amount", 0, nil), true)

	require.NoError(t, dataflow.FillInput(ctx, f, ia, dst, nil, owner))
	require.Equal(t, 11, dst.Value().Get(ctx))
	require.Len(t, f.DataMovements(), 1)
}
