package expressiontest_test

import (
	"context"
	"os"
	"os/exec"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	dataexpr "github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/expression/expressiontest"
	"github.com/dr-dobermann/gobpm/pkg/model/expression/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/expression/lite"
	"github.com/stretchr/testify/require"
)

// mapSource is a map-backed data.Source — the minimum a Subject needs.
type mapSource struct{ dd map[string]data.Data }

func (ms *mapSource) Find(_ context.Context, name string) (data.Data, error) {
	d, ok := ms.dd[name]
	if !ok {
		return nil, errs.New(
			errs.M("no datum %q", name),
			errs.C("EXPRTEST", errs.ObjectNotFound))
	}

	return d, nil
}

// source wraps one named value as a Ready parameter.
func source(t *testing.T, name string, v any) data.Source {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	p, err := data.ReadyValueParameter(name, values.NewVariable(v))
	require.NoError(t, err)

	return &mapSource{dd: map[string]data.Data{name: p}}
}

// TestLiteConformance runs the published suite against the lite engine, whose
// expressions are a text body.
func TestLiteConformance(t *testing.T) {
	expressiontest.Conformance(t,
		func(t *testing.T) expressiontest.Subject {
			ex, err := lite.Expr("a + 1")
			require.NoError(t, err)

			// float64, not int: lite evaluates arithmetic in floats. That
			// is engine-specific and legitimately so, which is exactly why
			// Want is supplied by the caller rather than inferred.
			return expressiontest.Subject{
				Engine:         lite.New(),
				Expr:           ex,
				Source:         source(t, "a", 1),
				Want:           float64(2),
				SourceRequired: true,
			}
		})
}

// TestGoExprConformance runs the SAME suite against goexpr, whose expressions
// are Go functions rather than text.
//
// Both in-core engines are exercised deliberately: they are the two extremes
// of what an ExpressionEngine can be, so a suite that fits both is one an
// outside FEEL or JUEL adapter can also satisfy. A suite proved against a
// single engine would silently encode that engine's shape.
func TestGoExprConformance(t *testing.T) {
	expressiontest.Conformance(t,
		func(t *testing.T) expressiontest.Subject {
			require.NoError(t, data.CreateDefaultStates())

			src := source(t, "a", 1)

			ex := dataexpr.Must(src,
				data.MustItemDefinition(values.NewVariable(0)),
				func(ctx context.Context, ds data.Source) (data.Value, error) {
					d, err := ds.Find(ctx, "a")
					if err != nil {
						return nil, err
					}

					n, ok := d.Value().Get(ctx).(int)
					if !ok {
						return nil, errs.New(errs.M("a is not an int"))
					}

					return values.NewVariable(n + 1), nil
				})

			return expressiontest.Subject{
				Engine: goexpr.New(),
				Expr:   ex,
				Source: src,
				Want:   2,
			}
		})
}

// brokenEngine claims no kind and no language: it satisfies the interface and
// is unroutable, which is precisely what testTypeIsNamed and
// testLanguagesAreClaimed exist to catch.
type brokenEngine struct{}

func (brokenEngine) Type() string        { return "" }
func (brokenEngine) Languages() []string { return nil }

func (brokenEngine) Evaluate(
	context.Context, data.FormalExpression, data.Source,
) (data.Value, error) {
	return nil, nil
}

// TestSuiteRejectsABrokenEngine is the suite's own negative control (SRD-088
// T-9). It runs in a child process for the reason given in the messagingtest
// twin: a failed subtest marks its parent failed, so "assert this fails"
// cannot be written in-process.
func TestSuiteRejectsABrokenEngine(t *testing.T) {
	if os.Getenv("GOBPM_CONFORMANCE_NEGATIVE") == "1" {
		expressiontest.Conformance(t,
			func(t *testing.T) expressiontest.Subject {
				ex, err := lite.Expr("a + 1")
				require.NoError(t, err)

				return expressiontest.Subject{
					Engine: brokenEngine{},
					Expr:   ex,
					Source: source(t, "a", 1),
				}
			})

		return
	}

	cmd := exec.Command(os.Args[0],
		"-test.run=^TestSuiteRejectsABrokenEngine$/^TypeIsNamed$",
		"-test.timeout=5m")
	cmd.Env = append(os.Environ(), "GOBPM_CONFORMANCE_NEGATIVE=1")

	if err := cmd.Run(); err == nil {
		t.Fatal("the conformance suite PASSED an engine that names no kind " +
			"and claims no language — such an engine can never be routed to")
	}
}
