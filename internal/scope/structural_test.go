package scope

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
)

// orderParam builds an "order" input parameter holding a structural record:
// {id, total, items:[{price:50},{price:100}]} (SRD-042 M4 fixtures).
func orderParam(t *testing.T) *data.Parameter {
	t.Helper()

	_ = data.CreateDefaultStates()

	item := func(p int) data.Value {
		return values.MustRecord(values.F("price", values.NewVariable(p)))
	}

	rec := values.MustRecord(
		values.F("id", values.NewVariable("A-1")),
		values.F("total", values.NewVariable(150)),
		values.F("items", values.NewArray[data.Value](item(50), item(100))),
	)

	p, err := data.NewParameter("order",
		data.MustItemAwareElement(
			data.MustItemDefinition(rec), data.ReadyDataState))
	require.NoError(t, err)

	return p
}

// TestFrameStructuralRead (SRD-042 T-4): frame.GetData resolves a structural
// path into a record-valued input; a mis-step errors.
func TestFrameStructuralRead(t *testing.T) {
	_, f := newTestFrame(t)
	require.NoError(t, f.InstantiateInputs([]*data.Parameter{orderParam(t)}))

	ctx := context.Background()

	d, err := f.GetData("order.items[1].price")
	require.NoError(t, err)
	require.Equal(t, 100, d.Value().Get(ctx))
	require.Equal(t, "order.items[1].price", d.Name())

	d, err = f.GetData("order.total")
	require.NoError(t, err)
	require.Equal(t, 150, d.Value().Get(ctx))

	_, err = f.GetData("order.total.nope") // field into a scalar
	require.Error(t, err)

	_, err = f.GetData("order.items[9]") // out of range
	require.Error(t, err)

	_, err = f.GetData("ghost.x") // unknown head
	require.Error(t, err)
}

// TestFindPlainNameUnchanged (SRD-042 T-9): plain and provider names resolve
// exactly as before — the structural split is transparent.
func TestFindPlainNameUnchanged(t *testing.T) {
	_, f := newTestFrame(t)
	require.NoError(t, f.InstantiateInputs(
		[]*data.Parameter{orderParam(t), testParam(t, "count", 5)}))

	ctx := context.Background()

	// a plain name returns the whole value (the record snapshot).
	d, err := f.GetData("order")
	require.NoError(t, err)
	require.IsType(t, map[string]any{}, d.Value().Get(ctx))

	d, err = f.GetData("count")
	require.NoError(t, err)
	require.Equal(t, 5, d.Value().Get(ctx))

	// an unknown plain name still errors as before.
	_, err = f.GetData("ghost")
	require.Error(t, err)
}

// TestFrameStructuralNonReadyHead: a structural read fails when its head is not
// Ready — structural navigation runs only over usable data (§3.5).
func TestFrameStructuralNonReadyHead(t *testing.T) {
	_, f := newTestFrame(t)

	rec := values.MustRecord(values.F("total", values.NewVariable(1)))
	draft, err := data.NewParameter("draft",
		data.MustItemAwareElement(
			data.MustItemDefinition(rec), data.UnavailableDataState))
	require.NoError(t, err)
	require.NoError(t, f.InstantiateInputs([]*data.Parameter{draft}))

	// the plain head still resolves (returns the unavailable datum)...
	_, err = f.GetData("draft")
	require.NoError(t, err)

	// ...but a structural step into a non-Ready head errors.
	_, err = f.GetData("draft.total")
	require.Error(t, err)
}

// frameSource adapts a Frame to a data.Source for expression evaluation.
type frameSource struct{ *Frame }

func (fs frameSource) Find(_ context.Context, name string) (data.Data, error) {
	return fs.GetData(name)
}

// TestGoexprReadsStructuralPath (SRD-042 T-6): a goexpr expression's ds.Find
// resolves a structural path through the seam — the expression layer, and so a
// gateway/flow condition built on it (T-5), needs no change.
func TestGoexprReadsStructuralPath(t *testing.T) {
	_, f := newTestFrame(t)
	require.NoError(t, f.InstantiateInputs([]*data.Parameter{orderParam(t)}))

	ctx := context.Background()
	src := frameSource{f}

	// the exclusive-gateway-style condition "order.total > 100".
	cond := func(ctx context.Context, ds data.Source) (data.Value, error) {
		d, err := ds.Find(ctx, "order.total")
		if err != nil {
			return nil, err
		}

		return values.NewVariable(d.Value().Get(ctx).(int) > 100), nil
	}

	ge := goexpr.Must(src,
		data.MustItemDefinition(values.NewVariable(false)),
		cond, foundation.WithID("order-gate"))

	res, err := ge.Evaluate(ctx, src)
	require.NoError(t, err)
	require.True(t, res.Get(ctx).(bool))
}
