package thresher_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/rules"
	"github.com/dr-dobermann/gobpm/pkg/rules/gorules"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// discountEngine registers the "discount" decision: read the order total from
// process data, yield the discount percent (the 1x1 fold commits it as the
// scalar variable "discount_pct").
func discountEngine(t *testing.T) *gorules.Registry {
	t.Helper()

	return gorules.New().MustRegister("discount",
		func(ctx context.Context, r service.DataReader) (rules.Row, error) {
			d, err := r.GetData("total")
			if err != nil {
				return nil, err
			}

			total, _ := d.Value().Get(ctx).(int)

			pct := 0
			if total > 100 {
				pct = 15
			}

			return rules.Row{"discount_pct": values.NewVariable(pct)}, nil
		})
}

// discountGt builds the condition "discount_pct > n" over the committed
// decision result.
func discountGt(t *testing.T, n int) data.FormalExpression {
	t.Helper()

	c, err := goexpr.New(nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			d, err := ds.Find(ctx, "discount_pct")
			if err != nil {
				return nil, err
			}

			v, _ := d.Value().Get(ctx).(int)

			return values.NewVariable(v > n), nil
		})
	require.NoError(t, err)

	return c
}

// brtRouteProcess builds: start → BRT("classify", ref "discount") →
// {big [discount_pct>10], small (default)} → ends. The BRT's own conditional
// outgoing flows route on the committed decision result.
func brtRouteProcess(
	t *testing.T, id string, total int, big, small *atomic.Bool,
) *process.Process {
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

	// the warm-up task sleeps so the test's observer attaches before the BRT
	// runs (the observer_test pattern).
	warm, err := activities.NewServiceTask("warm",
		nopOp(t, "warm-op", 300*time.Millisecond), activities.WithoutParams())
	require.NoError(t, err)

	brt, err := activities.NewBusinessRuleTask("classify", "discount")
	require.NoError(t, err)

	bigTask := laneTask(t, "big-discount", big)
	smallTask := laneTask(t, "small-discount", small)

	endB, err := events.NewEndEvent("end-big")
	require.NoError(t, err)
	endS, err := events.NewEndEvent("end-small")
	require.NoError(t, err)

	for _, e := range []flow.Element{
		start, warm, brt, bigTask, smallTask, endB, endS,
	} {
		require.NoError(t, proc.Add(e))
	}

	link(t, start, warm)
	link(t, warm, brt)

	_, err = flow.Link(brt, bigTask, flow.WithCondition(discountGt(t, 10)))
	require.NoError(t, err)

	sf, err := flow.Link(brt, smallTask)
	require.NoError(t, err)
	require.NoError(t, brt.SetDefaultFlow(sf.ID()))

	link(t, bigTask, endB)
	link(t, smallTask, endS)

	return proc
}

// runBRT starts an engine wired with eng, runs proc to completion and returns
// the collected facts.
func runBRT(
	t *testing.T, proc *process.Process, eng rules.Engine,
	wantState thresher.InstanceState,
) []observability.Fact {
	t.Helper()

	opts := []thresher.Option{thresher.WithoutBanner()}
	if eng != nil {
		opts = append(opts, thresher.WithRuleEngine(eng))
	}

	th, err := thresher.New("test-"+proc.ID(), opts...)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))

	_, err = th.RegisterProcess(proc)
	require.NoError(t, err)

	h, err := th.StartLatest(proc.ID())
	require.NoError(t, err)

	c := &collector{}
	sub := h.Observe(c)

	wctx, wcancel := context.WithTimeout(ctx, 5*time.Second)
	defer wcancel()

	state, err := h.WaitCompletion(wctx)
	if wantState == thresher.StateCompleted {
		require.NoError(t, err)
		require.Equal(t, wantState, state)
	} else {
		// a failed instance surfaces its classified error through the wait.
		require.Error(t, err)
	}

	sub.Cancel()

	c.mu.Lock()
	defer c.mu.Unlock()

	return append([]observability.Fact{}, c.events...)
}

// rulesFact finds the first KindRules fact with the given phase.
func rulesFact(
	facts []observability.Fact, phase observability.Phase,
) (observability.Fact, bool) {
	for _, f := range facts {
		if f.Kind == observability.KindRules && f.Phase == phase {
			return f, true
		}
	}

	return observability.Fact{}, false
}

// TestBusinessRuleTaskE2E covers SRD-060 T-5: the BRT evaluates the registered
// decision against live process data, the 1x1 fold commits the scalar, the
// task's conditional flows route on it, and the FR-6 Evaluated fact appears.
func TestBusinessRuleTaskE2E(t *testing.T) {
	t.Run("big discount routes the conditional flow",
		func(t *testing.T) {
			var big, small atomic.Bool

			proc := brtRouteProcess(t, "brt-big", 200, &big, &small)
			facts := runBRT(t, proc, discountEngine(t),
				thresher.StateCompleted)

			require.True(t, big.Load(), "the big-discount lane must run")
			require.False(t, small.Load(), "the default lane must not run")

			f, ok := rulesFact(facts, observability.PhaseEvaluated)
			require.True(t, ok, "the Rules/Evaluated fact must be observed")
			require.Equal(t, "discount",
				f.Details[observability.AttrDecisionRef])
			require.Equal(t, gorules.GoRulesType,
				f.Details[observability.AttrImplementation])
			require.Equal(t, "discount_pct",
				f.Details[observability.AttrResultVariable])
			require.Equal(t, "classify", f.NodeName)
		})

	t.Run("small discount takes the default flow",
		func(t *testing.T) {
			var big, small atomic.Bool

			proc := brtRouteProcess(t, "brt-small", 50, &big, &small)
			runBRT(t, proc, discountEngine(t), thresher.StateCompleted)

			require.False(t, big.Load())
			require.True(t, small.Load())
		})
}

// brtBoundaryProcess builds start → BRT (decision fails with a BpmnError) with
// an interrupting Error boundary routing to a recovery lane.
func brtBoundaryProcess(
	t *testing.T, id, code string, recovered *atomic.Bool,
) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	proc, err := process.New(id)
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	warm, err := activities.NewServiceTask("warm",
		nopOp(t, "warm-op", 300*time.Millisecond), activities.WithoutParams())
	require.NoError(t, err)

	brt, err := activities.NewBusinessRuleTask("classify", "no-rule")
	require.NoError(t, err)

	normalEnd, err := events.NewEndEvent("normal-end")
	require.NoError(t, err)

	bpErr, err := bpmncommon.NewError("brt-error", code, nil)
	require.NoError(t, err)

	eed, err := events.NewErrorEventDefinition(bpErr)
	require.NoError(t, err)

	be, err := events.NewBoundaryEvent("err-bnd", brt, eed, true)
	require.NoError(t, err)

	recovery := laneTask(t, "recovery", recovered)

	excEnd, err := events.NewEndEvent("exc-end")
	require.NoError(t, err)

	for _, e := range []flow.Element{
		start, warm, brt, normalEnd, be, recovery, excEnd,
	} {
		require.NoError(t, proc.Add(e))
	}

	link(t, start, warm)
	link(t, warm, brt)
	link(t, brt, normalEnd)
	link(t, be, recovery)
	link(t, recovery, excEnd)

	return proc
}

// TestBusinessRuleTaskFaultE2E covers SRD-060 T-6: a decision failing with a
// BpmnError travels the ordinary Error machinery — the boundary catches it and
// the exception flow completes the instance — and the FR-6 Failed fact appears.
func TestBusinessRuleTaskFaultE2E(t *testing.T) {
	const code = "NO_RULE"

	eng := gorules.New().MustRegister("no-rule",
		func(_ context.Context, _ service.DataReader) (rules.Row, error) {
			return nil, &events.BpmnError{Code: code}
		})

	var recovered atomic.Bool

	proc := brtBoundaryProcess(t, "brt-fault", code, &recovered)
	facts := runBRT(t, proc, eng, thresher.StateCompleted)

	require.True(t, recovered.Load(),
		"the boundary's exception flow must run")

	f, ok := rulesFact(facts, observability.PhaseFailed)
	require.True(t, ok, "the Rules/Failed fact must be observed")
	require.Equal(t, "no-rule", f.Details[observability.AttrDecisionRef])
	require.NotEmpty(t, f.Details[observability.AttrError])
}

// TestBusinessRuleTaskZeroConfig covers the batteries-included posture: with no
// WithRuleEngine option the bundled empty gorules registry is wired, and an
// unregistered decision reference fails the instance loud (never a silent
// no-op).
func TestBusinessRuleTaskZeroConfig(t *testing.T) {
	var big, small atomic.Bool

	proc := brtRouteProcess(t, "brt-zero", 200, &big, &small)
	facts := runBRT(t, proc, nil, thresher.StateTerminated)

	require.False(t, big.Load())
	require.False(t, small.Load())

	f, ok := rulesFact(facts, observability.PhaseFailed)
	require.True(t, ok, "the Rules/Failed fact must be observed")
	require.Equal(t, gorules.GoRulesType,
		f.Details[observability.AttrImplementation],
		"the bundled default registry answered the call")
}
