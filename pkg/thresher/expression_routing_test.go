package thresher_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	dgexpr "github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

// xTestEngine interprets the "x-test:expr" language: the body "gt <n>"
// compares the "total" datum against n — a real (if tiny) text
// interpreter proving BodyHolder-based evaluation.
type xTestEngine struct {
	evals atomic.Int32
}

func (e *xTestEngine) Type() string { return "##XTest" }

func (e *xTestEngine) Languages() []string { return []string{"x-test:expr"} }

func (e *xTestEngine) Evaluate(
	ctx context.Context, expr data.FormalExpression, src data.Source,
) (data.Value, error) {
	e.evals.Add(1)

	body := expr.(data.BodyHolder).Body()
	n := 0
	_, err := fmtSscanf(body, &n)
	if err != nil {
		return nil, err
	}

	d, err := src.Find(ctx, "total")
	if err != nil {
		return nil, err
	}

	total, _ := d.Value().Get(ctx).(int)

	return values.NewVariable(total > n), nil
}

// fmtSscanf parses "gt <n>".
func fmtSscanf(body string, n *int) (int, error) {
	body = strings.TrimPrefix(body, "gt ")

	v := 0
	for _, r := range body {
		v = v*10 + int(r-'0')
	}

	*n = v

	return 1, nil
}

// functorGt builds a gobpm:goexpr condition "total > n".
func functorGt(t *testing.T, n int) data.FormalExpression {
	t.Helper()

	c, err := dgexpr.New(nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			d, err := ds.Find(ctx, "total")
			if err != nil {
				return nil, err
			}

			v, _ := d.Value().Get(ctx).(int)

			return values.NewVariable(v > n), nil
		})
	require.NoError(t, err)

	return c
}

// mixedProcess builds start → a → {b [functor cond] } → { c [text cond] }
// → end: one process carrying BOTH expression kinds on its flows.
func mixedProcess(t *testing.T, id string, total int) (
	*process.Process, *atomic.Bool, *atomic.Bool,
) {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	proc, err := process.New(id,
		data.WithProperties(
			data.MustProperty("total",
				data.MustItemDefinition(values.NewVariable(total),
					foundation.WithID("total")),
				data.ReadyDataState)))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	route := laneTask(t, "route", &atomic.Bool{})

	var hitFunctor, hitText atomic.Bool

	fLane := laneTask(t, "functor-lane", &hitFunctor)
	tLane := laneTask(t, "text-lane", &hitText)

	endF, err := events.NewEndEvent("end-f")
	require.NoError(t, err)
	endT, err := events.NewEndEvent("end-t")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, route, fLane, tLane, endF, endT} {
		require.NoError(t, proc.Add(e))
	}

	link(t, start, route)

	_, err = flow.Link(route, fLane, flow.WithCondition(functorGt(t, 100)))
	require.NoError(t, err)

	textCond, err := data.NewTextExpression("x-test:expr", "gt 100",
		data.WithResultType("bool"))
	require.NoError(t, err)

	_, err = flow.Link(route, tLane, flow.WithCondition(textCond))
	require.NoError(t, err)

	link(t, fLane, endF)
	link(t, tLane, endT)

	return proc, &hitFunctor, &hitText
}

// TestMixedLanguageExpressions covers SRD-066 T-5: one process mixes a
// functor condition and a text-expression condition; each routes to its
// own engine and both lanes follow the evaluated results.
func TestMixedLanguageExpressions(t *testing.T) {
	xt := &xTestEngine{}

	proc, hitFunctor, hitText := mixedProcess(t, "expr-mixed", 150)

	th, err := thresher.New("test-expr-mixed",
		thresher.WithoutBanner(), thresher.WithExpressionEngine(xt))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, th.Run(ctx))

	_, err = th.RegisterProcess(proc)
	require.NoError(t, err)

	h, err := th.StartLatest(proc.ID())
	require.NoError(t, err)

	wctx, wcancel := context.WithTimeout(ctx, 5*time.Second)
	defer wcancel()

	_, werr := h.WaitCompletion(wctx)
	require.NoError(t, werr)

	require.True(t, hitFunctor.Load(),
		"the functor condition (gobpm:goexpr via the battery) must pass")
	require.True(t, hitText.Load(),
		"the text condition (x-test:expr via the registered engine) must pass")
	require.Positive(t, xt.evals.Load(),
		"the text engine must have evaluated")
}

// TestOptedOutRegistryFaultsFunctorConditions: with the batteries
// suppressed and nothing registered, a functor condition faults the
// instance loud with the (empty) claims context.
func TestOptedOutRegistryFaultsFunctorConditions(t *testing.T) {
	proc, _, _ := mixedProcess(t, "expr-optout", 150)

	th, err := thresher.New("test-expr-optout",
		thresher.WithoutBanner(),
		thresher.WithoutDefaultExpressionEngines())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, th.Run(ctx))

	_, err = th.RegisterProcess(proc)
	require.NoError(t, err)

	h, err := th.StartLatest(proc.ID())
	require.NoError(t, err)

	wctx, wcancel := context.WithTimeout(ctx, 5*time.Second)
	defer wcancel()

	_, werr := h.WaitCompletion(wctx)
	require.Error(t, werr)
	require.Contains(t, werr.Error(), "WithExpressionEngine")
}
