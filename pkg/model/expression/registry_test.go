package expression_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/expression"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

var ctx = context.Background()

// stubEngine claims the given languages and records executions.
type stubEngine struct {
	kind      string
	languages []string
	calls     int
}

func (se *stubEngine) Type() string { return se.kind }

func (se *stubEngine) Languages() []string { return se.languages }

func (se *stubEngine) Evaluate(
	_ context.Context, _ data.FormalExpression, _ data.Source,
) (data.Value, error) {
	se.calls++

	return values.NewVariable(se.kind), nil
}

// stubExpr is a minimal FormalExpression with a controllable language.
type stubExpr struct {
	foundation.BaseElement
	lang string
}

func newStubExpr(t *testing.T, lang string) *stubExpr {
	t.Helper()

	be, err := foundation.NewBaseElement()
	require.NoError(t, err)

	return &stubExpr{BaseElement: *be, lang: lang}
}

func (se *stubExpr) Language() string { return se.lang }

func (se *stubExpr) Evaluate(
	context.Context, data.Source,
) (data.Value, error) {
	return nil, errs.New(errs.M("stub"))
}

func (se *stubExpr) Result() (data.Value, error) {
	return nil, errs.New(errs.M("stub"))
}

func (se *stubExpr) ResultType() string { return "" }

func (se *stubExpr) IsEvaluated() bool { return false }

// TestRegistryConstruction covers SRD-066 T-1's validation half.
func TestRegistryConstruction(t *testing.T) {
	ge := &stubEngine{kind: "##GoExpr", languages: []string{"gobpm:goexpr"}}

	t.Run("nil engine rejected",
		func(t *testing.T) {
			_, err := expression.NewRegistry(ge, nil)
			require.Error(t, err)
			require.Contains(t, err.Error(), "engine 1")
		})

	t.Run("claimless engine rejected",
		func(t *testing.T) {
			_, err := expression.NewRegistry(&stubEngine{kind: "##Empty"})
			require.Error(t, err)
			require.Contains(t, err.Error(), "##Empty")
		})

	t.Run("blank claim rejected",
		func(t *testing.T) {
			_, err := expression.NewRegistry(
				&stubEngine{kind: "##Blank", languages: []string{" "}})
			require.Error(t, err)
			require.Contains(t, err.Error(), "##Blank")
		})

	t.Run("duplicate claim names both kinds and the language",
		func(t *testing.T) {
			clash := &stubEngine{kind: "##Other",
				languages: []string{"GOBPM:GOEXPR"}} // case-insensitive

			_, err := expression.NewRegistry(ge, clash)
			require.Error(t, err)
			require.Contains(t, err.Error(), "gobpm:goexpr")
			require.Contains(t, err.Error(), "##GoExpr")
			require.Contains(t, err.Error(), "##Other")
		})

	t.Run("aggregation and claims",
		func(t *testing.T) {
			lite := &stubEngine{kind: "##Lite",
				languages: []string{"gobpm:lite"}}

			reg, err := expression.NewRegistry(ge, lite)
			require.NoError(t, err)

			require.Equal(t, "##GoExpr+##Lite", reg.Type())
			require.Equal(t,
				[]string{"gobpm:goexpr", "gobpm:lite"}, reg.Languages())
		})
}

// TestRegistryEvaluate covers SRD-066 T-1's routing half.
func TestRegistryEvaluate(t *testing.T) {
	t.Run("empty registry is ##None and fails with the wiring hint",
		func(t *testing.T) {
			reg, err := expression.NewRegistry()
			require.NoError(t, err)

			require.Equal(t, expression.NoneType, reg.Type())
			require.Empty(t, reg.Languages())

			_, err = reg.Evaluate(ctx, newStubExpr(t, "gobpm:goexpr"), nil)
			require.Error(t, err)
			require.Contains(t, err.Error(), "WithExpressionEngine")
		})

	t.Run("routes by normalized language",
		func(t *testing.T) {
			ge := &stubEngine{kind: "##GoExpr",
				languages: []string{"gobpm:goexpr"}}
			fe := &stubEngine{kind: "##FEEL",
				languages: []string{"urn:feel"}}

			reg, err := expression.NewRegistry(ge, fe)
			require.NoError(t, err)

			v, err := reg.Evaluate(ctx, newStubExpr(t, " URN:FEEL "), nil)
			require.NoError(t, err)
			require.Equal(t, "##FEEL", v.Get(ctx))
			require.Zero(t, ge.calls)
			require.Equal(t, 1, fe.calls)
		})

	t.Run("unclaimed language lists the registered claims",
		func(t *testing.T) {
			ge := &stubEngine{kind: "##GoExpr",
				languages: []string{"gobpm:goexpr"}}

			reg, err := expression.NewRegistry(ge)
			require.NoError(t, err)

			_, err = reg.Evaluate(ctx, newStubExpr(t, "urn:juel"), nil)
			require.Error(t, err)
			require.Contains(t, err.Error(), "urn:juel")
			require.Contains(t, err.Error(), "gobpm:goexpr")
		})

	t.Run("nil expression and empty language rejected",
		func(t *testing.T) {
			ge := &stubEngine{kind: "##GoExpr",
				languages: []string{"gobpm:goexpr"}}

			reg, err := expression.NewRegistry(ge)
			require.NoError(t, err)

			_, err = reg.Evaluate(ctx, nil, nil)
			require.Error(t, err)

			_, err = reg.Evaluate(ctx, newStubExpr(t, "  "), nil)
			require.Error(t, err)
			require.Contains(t, err.Error(), "no language")
		})
}
