package lite_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/expression/lite"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// bodylessExpr is a FormalExpression WITHOUT the BodyHolder capability —
// the engine must refuse it loud.
type bodylessExpr struct {
	foundation.BaseElement
}

func newBodylessExpr(t *testing.T) *bodylessExpr {
	t.Helper()

	be, err := foundation.NewBaseElement()
	require.NoError(t, err)

	return &bodylessExpr{BaseElement: *be}
}

func (be *bodylessExpr) Language() string { return lite.Language }

func (be *bodylessExpr) Evaluate(
	context.Context, data.Source,
) (data.Value, error) {
	return nil, errs.New(errs.M("stub"))
}

func (be *bodylessExpr) Result() (data.Value, error) {
	return nil, errs.New(errs.M("stub"))
}

func (be *bodylessExpr) ResultType() string { return "" }

func (be *bodylessExpr) IsEvaluated() bool { return false }

// TestEngineSurface covers SRD-067 T-5: the claims, the parameter
// validation, the BodyHolder requirement and the declared-result-type
// guard.
func TestEngineSurface(t *testing.T) {
	e := lite.New()
	src := newSource(t, nil)

	t.Run("claims",
		func(t *testing.T) {
			require.Equal(t, "##Lite", e.Type())
			require.Equal(t, []string{"gobpm:lite"}, e.Languages())
		})

	t.Run("params validated",
		func(t *testing.T) {
			_, err := e.Evaluate(ctx, nil, src)
			require.Error(t, err)
			require.Contains(t, err.Error(), "nil FormalExpression")

			ex, err := lite.Expr("1 + 1")
			require.NoError(t, err)

			_, err = e.Evaluate(ctx, ex, nil)
			require.Error(t, err)
			require.Contains(t, err.Error(), "nil data Source")
		})

	t.Run("a body-less expression is refused",
		func(t *testing.T) {
			_, err := e.Evaluate(ctx, newBodylessExpr(t), src)
			require.Error(t, err)
			require.Contains(t, err.Error(), "BodyHolder")
		})

	t.Run("the declared result type is enforced",
		func(t *testing.T) {
			cond, err := lite.Cond("1 + 1") // declares bool, produces number
			require.NoError(t, err)

			_, err = e.Evaluate(ctx, cond, src)
			require.Error(t, err)
			require.Contains(t, err.Error(), "declared")
			require.Contains(t, err.Error(), "float64")
		})

	t.Run("every produced kind packs and passes when undeclared",
		func(t *testing.T) {
			for body, wantType := range map[string]string{
				"1 + 1":                        "float64",
				"'a'":                          "string",
				"1 < 2":                        "bool",
				`time("2026-08-01T00:00:00Z")`: "Time",
			} {
				ex, err := lite.Expr(body)
				require.NoError(t, err)

				v, err := e.Evaluate(ctx, ex, src)
				require.NoError(t, err, "body: %s", body)
				require.Equal(t, wantType, v.Type(), "body: %s", body)
			}
		})
}

// TestConveniences covers the Expr/Cond constructors (SRD-067 FR-5).
func TestConveniences(t *testing.T) {
	t.Run("Expr pre-tags gobpm:lite",
		func(t *testing.T) {
			ex, err := lite.Expr("total > 100")
			require.NoError(t, err)
			require.Equal(t, "gobpm:lite", ex.Language())
			require.Equal(t, "total > 100", ex.Body())
			require.Empty(t, ex.ResultType())
		})

	t.Run("Cond declares the bool result",
		func(t *testing.T) {
			c, err := lite.Cond("total > 100")
			require.NoError(t, err)
			require.Equal(t, "gobpm:lite", c.Language())
			require.Equal(t, "bool", c.ResultType())
		})

	t.Run("a later WithResultType overrides deliberately",
		func(t *testing.T) {
			c, err := lite.Cond("tier", data.WithResultType("string"))
			require.NoError(t, err)
			require.Equal(t, "string", c.ResultType())
		})

	t.Run("an empty body is rejected",
		func(t *testing.T) {
			_, err := lite.Expr("  ")
			require.Error(t, err)
		})
}
