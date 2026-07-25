package lua_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/adapters/lua"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/script"
)

var ctx = context.Background()

// countingReader hands named values and counts reads per name.
type countingReader struct {
	values map[string]any
	reads  atomic.Int32
}

func (cr *countingReader) GetData(name string) (data.Data, error) {
	cr.reads.Add(1)

	v, ok := cr.values[name]
	if !ok {
		return nil, errs.New(errs.M("no datum %q", name))
	}

	return data.MustParameter(name,
		data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable(v)),
			data.ReadyDataState)), nil
}

func (cr *countingReader) GetDataByID(id string) (data.Data, error) {
	return cr.GetData(id)
}

func (cr *countingReader) GetSources() []string { return nil }

func (cr *countingReader) List(string) ([]string, error) { return nil, nil }

func TestMain(m *testing.M) {
	if err := data.CreateDefaultStates(); err != nil {
		panic(err)
	}

	m.Run()
}

func rdr(kv map[string]any) *countingReader {
	return &countingReader{values: kv}
}

// run executes body on a fresh engine.
func run(
	t *testing.T, body string, r *countingReader,
) (script.Outputs, error) {
	t.Helper()

	return lua.New().Execute(ctx, "text/x-lua", body, r)
}

// TestEngineIdentity covers T-1.
func TestEngineIdentity(t *testing.T) {
	e := lua.New()

	require.Equal(t, "##Lua", e.Type())
	require.Equal(t, lua.LuaType, e.Type())
	require.Equal(t,
		[]string{"text/x-lua", "application/x-lua", "lua"}, e.Formats())

	t.Run("a fresh state per execution",
		func(t *testing.T) {
			r := rdr(nil)

			_, err := e.Execute(ctx, "lua", "leak = 42", r)
			require.NoError(t, err)

			outs, err := e.Execute(ctx, "lua",
				"return {seen = leak ~= nil}", r)
			require.NoError(t, err)
			require.Equal(t, false, outs["seen"].Get(ctx),
				"a global from run 1 must be invisible in run 2")
		})

	t.Run("empty script and nil reader rejected",
		func(t *testing.T) {
			_, err := e.Execute(ctx, "lua", "", rdr(nil))
			require.Error(t, err)

			_, err = e.Execute(ctx, "lua", "return {}", nil)
			require.Error(t, err)
		})
}

// TestDataBridge covers T-2.
func TestDataBridge(t *testing.T) {
	t.Run("lazy reads — only accessed data is read",
		func(t *testing.T) {
			r := rdr(map[string]any{"a": 1, "b": 2, "c": 3})

			outs, err := run(t, "return {x = data.a}", r)
			require.NoError(t, err)
			require.Equal(t, float64(1), outs["x"].Get(ctx))
			require.Equal(t, int32(1), r.reads.Load(),
				"only the accessed datum must be read")
		})

	t.Run("an absent datum raises naming it",
		func(t *testing.T) {
			_, err := run(t, "return {x = data.missing}", rdr(nil))
			require.Error(t, err)
			require.Contains(t, err.Error(), "missing")
			require.Contains(t, err.Error(), "has()")
		})

	t.Run("has() probes optional data",
		func(t *testing.T) {
			outs, err := run(t,
				`return {a = has("total"), b = has("nope")}`,
				rdr(map[string]any{"total": 1}))
			require.NoError(t, err)
			require.Equal(t, true, outs["a"].Get(ctx))
			require.Equal(t, false, outs["b"].Get(ctx))
		})

	t.Run("the data table is read-only",
		func(t *testing.T) {
			_, err := run(t, "data.x = 1", rdr(nil))
			require.Error(t, err)
			require.Contains(t, err.Error(), "read-only")
		})

	t.Run("Go values bridge in, including nested structures",
		func(t *testing.T) {
			r := rdr(map[string]any{
				"flag": true,
				"n64":  int64(7),
				"rate": 0.5,
				"name": "gold",
				"tags": []any{"a", "b"},
				"dims": map[string]any{"w": 2, "h": 3},
			})

			outs, err := run(t, `
				return {
					f = data.flag,
					n = data.n64 + 1,
					r = data.rate * 2,
					s = data.name .. "!",
					t1 = data.tags[1],
					area = data.dims.w * data.dims.h,
				}`, r)
			require.NoError(t, err)
			require.Equal(t, true, outs["f"].Get(ctx))
			require.Equal(t, float64(8), outs["n"].Get(ctx))
			require.Equal(t, float64(1), outs["r"].Get(ctx))
			require.Equal(t, "gold!", outs["s"].Get(ctx))
			require.Equal(t, "a", outs["t1"].Get(ctx))
			require.Equal(t, float64(6), outs["area"].Get(ctx))
		})

	t.Run("an unmappable Go value fails loud",
		func(t *testing.T) {
			_, err := run(t, "return {x = data.ch}",
				rdr(map[string]any{"ch": make(chan int)}))
			require.Error(t, err)
			require.Contains(t, err.Error(), "ch")
		})
}

// TestResults covers T-3.
func TestResults(t *testing.T) {
	t.Run("the worked script",
		func(t *testing.T) {
			r := rdr(map[string]any{"total": 150, "tier": "vip"})

			outs, err := run(t, `
				local total = data.total
				local tier  = has("tier") and data.tier or "retail"

				return {
					discount_pct = (tier == "vip" and total > 100) and 25
					               or (total > 100 and 15 or 5),
					audited      = true,
				}`, r)
			require.NoError(t, err)
			require.Equal(t, float64(25), outs["discount_pct"].Get(ctx))
			require.Equal(t, true, outs["audited"].Get(ctx))
		})

	t.Run("no return commits nothing",
		func(t *testing.T) {
			outs, err := run(t, "local x = 1", rdr(nil))
			require.NoError(t, err)
			require.Empty(t, outs)
		})

	t.Run("nested tables map to Go structures",
		func(t *testing.T) {
			outs, err := run(t, `
				return {
					seq = {1, 2, 3},
					rec = {a = 1, b = "x"},
				}`, rdr(nil))
			require.NoError(t, err)
			require.Equal(t, []any{
				float64(1), float64(2), float64(3),
			}, outs["seq"].Get(ctx))
			require.Equal(t, map[string]any{
				"a": float64(1), "b": "x",
			}, outs["rec"].Get(ctx))
		})

	t.Run("a non-table return errors",
		func(t *testing.T) {
			_, err := run(t, "return 42", rdr(nil))
			require.Error(t, err)
			require.Contains(t, err.Error(), "table")
		})

	t.Run("non-string output keys error",
		func(t *testing.T) {
			_, err := run(t, "return {[true] = 1, x = 2}", rdr(nil))
			require.Error(t, err)
			require.Contains(t, err.Error(), "string keys")
		})

	t.Run("a function-valued output errors",
		func(t *testing.T) {
			_, err := run(t, "return {f = function() end}", rdr(nil))
			require.Error(t, err)
			require.Contains(t, err.Error(), "function")
		})
}

// TestSandboxAndContext covers T-4.
func TestSandboxAndContext(t *testing.T) {
	t.Run("io and os never load; the load family is removed",
		func(t *testing.T) {
			outs, err := run(t, `
				return {
					io_gone       = io == nil,
					os_gone       = os == nil,
					dofile_gone   = dofile == nil,
					loadfile_gone = loadfile == nil,
					load_gone     = load == nil,
					loadstr_gone  = loadstring == nil,
					math_ok       = math.floor(1.5) == 1,
					str_ok        = string.upper("a") == "A",
				}`, rdr(nil))
			require.NoError(t, err)

			for _, k := range []string{
				"io_gone", "os_gone", "dofile_gone", "loadfile_gone",
				"load_gone", "loadstr_gone", "math_ok", "str_ok",
			} {
				require.Equal(t, true, outs[k].Get(ctx), k)
			}
		})

	t.Run("an infinite loop aborts on the context deadline",
		func(t *testing.T) {
			dctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
			defer cancel()

			start := time.Now()

			_, err := lua.New().Execute(dctx, "lua",
				"while true do end", rdr(nil))
			require.Error(t, err)
			require.Less(t, time.Since(start), 3*time.Second,
				"the deadline must abort the script")
		})

	t.Run("compile and runtime errors surface with the Lua message",
		func(t *testing.T) {
			_, err := run(t, "this is not lua", rdr(nil))
			require.Error(t, err)

			_, err = run(t, `error("business says no")`, rdr(nil))
			require.Error(t, err)
			require.Contains(t, err.Error(), "business says no")
		})
}
