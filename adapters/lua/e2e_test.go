package lua_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/adapters/lua"
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
	"github.com/dr-dobermann/gobpm/pkg/script"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// discountLua is the worked SRD script: lazy reads, has() probing, a
// named-outputs return.
const discountLua = `
local total = data.total
local tier  = has("tier") and data.tier or "retail"

return {
  discount_pct = (tier == "vip" and total > 100) and 25
                 or (total > 100 and 15 or 5),
  audited      = true,
}`

// fakeEngine is the stub second engine proving multi-engine routing with a
// real interpreter beside it.
type fakeEngine struct {
	mu  sync.Mutex
	ran int
}

func (fe *fakeEngine) Type() string { return "##Fake" }

func (fe *fakeEngine) Formats() []string { return []string{"text/x-fake"} }

func (fe *fakeEngine) Execute(
	_ context.Context, _, _ string, _ service.DataReader,
) (script.Outputs, error) {
	fe.mu.Lock()
	fe.ran++
	fe.mu.Unlock()

	return script.Outputs{"fake_out": values.NewVariable("ok")}, nil
}

func (fe *fakeEngine) runs() int {
	fe.mu.Lock()
	defer fe.mu.Unlock()

	return fe.ran
}

// laneTask flips hit when executed.
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

// discountGt builds "discount_pct > n" over the committed float64 output.
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

// link connects two elements with an unconditional flow.
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

func (c *collector) implementations() map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()

	out := map[string]string{}

	for _, f := range c.facts {
		if f.Kind == observability.KindScript &&
			f.Phase == observability.PhaseExecuted {
			out[f.Details[observability.AttrScriptFormat]] =
				f.Details[observability.AttrImplementation]
		}
	}

	return out
}

// luaProcess builds start → warm(sleep) → lua(script) → fake(script) →
// {big [discount_pct>10] | small (default)} → ends.
func luaProcess(
	t *testing.T, id string, total int, tier string, big, small *atomic.Bool,
) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	props := []*data.Property{
		data.MustProperty("total",
			data.MustItemDefinition(values.NewVariable(total),
				foundation.WithID("total")),
			data.ReadyDataState),
	}
	if tier != "" {
		props = append(props, data.MustProperty("tier",
			data.MustItemDefinition(values.NewVariable(tier),
				foundation.WithID("tier")),
			data.ReadyDataState))
	}

	proc, err := process.New(id, data.WithProperties(props...))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	warmOp, err := gooper.New("warm-op",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			time.Sleep(300 * time.Millisecond)

			return nil, nil
		})
	require.NoError(t, err)

	warm, err := activities.NewServiceTask("warm", warmOp,
		activities.WithoutParams())
	require.NoError(t, err)

	luaTask, err := activities.NewScriptTask("classify", "text/x-lua",
		discountLua)
	require.NoError(t, err)

	fakeTask, err := activities.NewScriptTask("side", "text/x-fake", "any")
	require.NoError(t, err)

	bigTask := laneTask(t, "big-discount", big)
	smallTask := laneTask(t, "small-discount", small)

	endB, err := events.NewEndEvent("end-big")
	require.NoError(t, err)
	endS, err := events.NewEndEvent("end-small")
	require.NoError(t, err)

	for _, e := range []flow.Element{
		start, warm, luaTask, fakeTask, bigTask, smallTask, endB, endS,
	} {
		require.NoError(t, proc.Add(e))
	}

	link(t, start, warm)
	link(t, warm, luaTask)
	link(t, luaTask, fakeTask)

	_, err = flow.Link(fakeTask, bigTask,
		flow.WithCondition(discountGt(t, 10)))
	require.NoError(t, err)

	sf, err := flow.Link(fakeTask, smallTask)
	require.NoError(t, err)
	require.NoError(t, fakeTask.SetDefaultFlow(sf.ID()))

	link(t, bigTask, endB)
	link(t, smallTask, endS)

	return proc
}

// runLua wires the engines, runs proc and returns the collector.
func runLua(
	t *testing.T, proc *process.Process, fe *fakeEngine,
) *collector {
	t.Helper()

	th, err := thresher.New("test-"+proc.ID(),
		thresher.WithoutBanner(),
		thresher.WithScriptEngine(lua.New()),
		thresher.WithScriptEngine(fe))
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
	require.NoError(t, werr)

	sub.Cancel()

	return c
}

// TestLuaE2E covers SRD-065 T-5: a real Lua script beside a stub engine —
// live multi-engine routing, the Lua outputs driving a conditional flow,
// and the fact attribution per format.
func TestLuaE2E(t *testing.T) {
	t.Run("vip big order routes the big-discount lane",
		func(t *testing.T) {
			var big, small atomic.Bool

			fe := &fakeEngine{}
			c := runLua(t,
				luaProcess(t, "lua-vip", 500, "vip", &big, &small), fe)

			require.True(t, big.Load(), "25% must route the big lane")
			require.False(t, small.Load())
			require.Equal(t, 1, fe.runs(), "the fake engine ran its task")

			impls := c.implementations()
			require.Equal(t, "##Lua", impls["text/x-lua"])
			require.Equal(t, "##Fake", impls["text/x-fake"])
		})

	t.Run("a small order with no tier takes the default lane",
		func(t *testing.T) {
			var big, small atomic.Bool

			fe := &fakeEngine{}
			runLua(t, luaProcess(t, "lua-small", 40, "", &big, &small), fe)

			require.False(t, big.Load())
			require.True(t, small.Load(),
				"5% (has(tier)=false fallthrough) must route the default")
		})
}
