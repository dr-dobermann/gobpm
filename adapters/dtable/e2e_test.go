package dtable_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/adapters/dtable"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// deployedDiscountEngine builds the full component: a vocabulary of Go
// behavior + the SRD's JSON artifact deployed through the seam.
func deployedDiscountEngine(t *testing.T) *dtable.Engine {
	t.Helper()

	dec, err := dtable.NewJSONDecoder(dtable.NewVocabulary().
		MustAddCondition("big-order", dtable.GT("total", 100)))
	require.NoError(t, err)

	e, err := dtable.New(dtable.WithDecoder(dec))
	require.NoError(t, err)

	require.NoError(t, e.Deploy(context.Background(), []byte(`{
		"name": "discount",
		"hitPolicy": "FIRST",
		"rules": [
			{"when": ["big-order"], "then": {"discount_pct": 15}},
			{"when": [], "then": {"discount_pct": 5}}
		]
	}`)))

	return e
}

// laneTask flips hit when executed — the probe for which lane fired.
func laneTask(
	t *testing.T, name string, hit *atomic.Bool,
) *activities.ServiceTask {
	t.Helper()

	op, err := gooper.New(name,
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			hit.Store(true)

			return nil, nil
		})
	require.NoError(t, err)

	st, err := activities.NewServiceTask(name, op, activities.WithoutParams())
	require.NoError(t, err)

	return st
}

// sleepTask gives the test's observer time to attach before the BRT runs.
func sleepTask(t *testing.T) *activities.ServiceTask {
	t.Helper()

	op, err := gooper.New("warm-op",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			time.Sleep(300 * time.Millisecond)

			return nil, nil
		})
	require.NoError(t, err)

	st, err := activities.NewServiceTask("warm", op, activities.WithoutParams())
	require.NoError(t, err)

	return st
}

// discountGt builds the condition "discount_pct > n" over the committed
// decision result (a float64 — the deployed-JSON-literal typing).
func discountGt(t *testing.T, n float64) data.FormalExpression {
	t.Helper()

	c, err := goexpr.New(nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			d, err := ds.Find(ctx, "discount_pct")
			if err != nil {
				return nil, err
			}

			v, _ := d.Value().Get(ctx).(float64)

			return values.NewVariable(v > n), nil
		})
	require.NoError(t, err)

	return c
}

// brtProcess builds start → warm → classify(BRT "discount") →
// {big [discount_pct>10] | small (default)} → ends.
func brtProcess(
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

	warm := sleepTask(t)

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

// link connects two elements with an unconditional sequence flow.
func link(t *testing.T, from, to flow.Element) {
	t.Helper()

	_, err := flow.Link(from.(flow.SequenceSource), to.(flow.SequenceTarget))
	require.NoError(t, err)
}

// collector records observed facts.
type collector struct {
	mu    sync.Mutex
	facts []observability.Fact
}

func (c *collector) OnFact(f observability.Fact) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.facts = append(c.facts, f)
}

func (c *collector) rulesFact(
	phase observability.Phase,
) (observability.Fact, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, f := range c.facts {
		if f.Kind == observability.KindRules && f.Phase == phase {
			return f, true
		}
	}

	return observability.Fact{}, false
}

// runBRT wires eng into a thresher, runs proc and returns the collector
// (and the completion error, if any).
func runBRT(
	t *testing.T, proc *process.Process, eng *dtable.Engine,
) (*collector, error) {
	t.Helper()

	th, err := thresher.New("test-"+proc.ID(),
		thresher.WithoutBanner(), thresher.WithRuleEngine(eng))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, th.Run(ctx))

	_, err = th.RegisterProcess(proc)
	require.NoError(t, err)

	h, err := th.StartLatest(proc.ID())
	require.NoError(t, err)

	c := &collector{}
	sub := h.Observe(c)

	wctx, wcancel := context.WithTimeout(ctx, 5*time.Second)
	defer wcancel()

	_, werr := h.WaitCompletion(wctx)

	sub.Cancel()

	return c, werr
}

// TestDTableE2E covers SRD-062 T-5: the full deploy+evaluate component
// driving the Business Rule Task through the seam.
func TestDTableE2E(t *testing.T) {
	t.Run("a deployed table routes the big-discount lane",
		func(t *testing.T) {
			var big, small atomic.Bool

			c, err := runBRT(t,
				brtProcess(t, "dt-big", 150, &big, &small),
				deployedDiscountEngine(t))
			require.NoError(t, err)

			require.True(t, big.Load(), "the big-discount lane must run")
			require.False(t, small.Load())

			f, ok := c.rulesFact(observability.PhaseEvaluated)
			require.True(t, ok, "the Rules/Evaluated fact must be observed")
			require.Equal(t, dtable.DTableType,
				f.Details[observability.AttrImplementation])
			require.Equal(t, "discount",
				f.Details[observability.AttrDecisionRef])
		})

	t.Run("the fallthrough rule takes the default lane",
		func(t *testing.T) {
			var big, small atomic.Bool

			_, err := runBRT(t,
				brtProcess(t, "dt-small", 50, &big, &small),
				deployedDiscountEngine(t))
			require.NoError(t, err)

			require.False(t, big.Load())
			require.True(t, small.Load())
		})

	t.Run("a missing datum fails the instance loud",
		func(t *testing.T) {
			// the table reads "total", the process declares "amount" only.
			proc, err := process.New("dt-loud",
				data.WithProperties(
					data.MustProperty("amount",
						data.MustItemDefinition(values.NewVariable(1),
							foundation.WithID("amount")),
						data.ReadyDataState)))
			require.NoError(t, err)

			start, err := events.NewStartEvent("start")
			require.NoError(t, err)

			brt, err := activities.NewBusinessRuleTask("classify", "discount")
			require.NoError(t, err)

			end, err := events.NewEndEvent("end")
			require.NoError(t, err)

			for _, e := range []flow.Element{start, brt, end} {
				require.NoError(t, proc.Add(e))
			}

			link(t, start, brt)
			link(t, brt, end)

			_, werr := runBRT(t, proc, deployedDiscountEngine(t))
			require.Error(t, werr)
			require.Contains(t, werr.Error(), "total")
		})
}
