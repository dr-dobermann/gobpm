package lite_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/expression/lite"
)

var ctx = context.Background()

// testSource is a map-backed data.Source mirroring the runtime frame: a
// structural name resolves its head against the map and walks the steps
// through data.ResolvePath (internal/scope/frame.go's GetData shape).
type testSource struct {
	dd map[string]data.Data
}

func (ts *testSource) Find(
	ctx context.Context, name string,
) (data.Data, error) {
	return data.ResolvePath(ctx, name,
		func(head string) (data.Data, error) {
			d, ok := ts.dd[head]
			if !ok {
				return nil, errs.New(
					errs.M("no datum %q", head),
					errs.C("LITE_TEST", errs.ObjectNotFound))
			}

			return d, nil
		})
}

// newSource wraps named values as Ready parameters.
func newSource(t *testing.T, vv map[string]data.Value) *testSource {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	dd := map[string]data.Data{}

	for n, v := range vv {
		p, err := data.ReadyValueParameter(n, v)
		require.NoError(t, err)

		dd[n] = p
	}

	return &testSource{dd: dd}
}

// adrSource builds the ADR-032 §2.3 worked-examples fixture set: single
// values of every kind, an array, a nested record and a map.
func adrSource(t *testing.T) *testSource {
	t.Helper()

	customer, err := values.NewRecord(
		values.F("tier", values.NewVariable("vip")))
	require.NoError(t, err)

	order, err := values.NewRecord(
		values.F("total", values.NewVariable(150)),
		values.F("customer", customer))
	require.NoError(t, err)

	rates, err := values.NewMap(map[string]float64{
		"EUR": 1.1,
		"USD": 0.9,
	})
	require.NoError(t, err)

	return newSource(t, map[string]data.Value{
		"total":    values.NewVariable(150),
		"rate":     values.NewVariable(0.19),
		"tier":     values.NewVariable("gold"),
		"name":     values.NewVariable("Ann"),
		"approved": values.NewVariable(true),
		"blocked":  values.NewVariable(false),
		"deadline": values.NewVariable(
			time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)),
		"created_at": values.NewVariable(
			time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)),
		"updated_at": values.NewVariable(
			time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC)),
		"items": values.NewArray("sku-1", "sku-2"),
		"order": order,
		"rates": rates,
	})
}

// evalBody runs one body through the engine over src and returns the
// unwrapped result.
func evalBody(
	t *testing.T, src data.Source, body string,
) (any, error) {
	t.Helper()

	ex, err := lite.Expr(body)
	require.NoError(t, err)

	v, err := lite.New().Evaluate(ctx, ex, src)
	if err != nil {
		return nil, err
	}

	return v.Get(ctx), nil
}

// wantValue asserts a successful evaluation result.
func wantValue(t *testing.T, src data.Source, body string, want any) {
	t.Helper()

	got, err := evalBody(t, src, body)
	require.NoError(t, err, "body: %s", body)

	if f, ok := want.(float64); ok {
		require.InEpsilon(t, f, got, 1e-9, "body: %s", body)

		return
	}

	require.Equal(t, want, got, "body: %s", body)
}

// wantError asserts a loud failure whose message carries every marker.
func wantError(
	t *testing.T, src data.Source, body string, markers ...string,
) {
	t.Helper()

	_, err := evalBody(t, src, body)
	require.Error(t, err, "body: %s", body)

	for _, m := range markers {
		require.Contains(t, err.Error(), m, "body: %s", body)
	}
}
