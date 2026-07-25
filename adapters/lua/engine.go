// Package lua is the batteries Script Engine (ADR-031 §2.4): Lua via the
// pure-Go gopher-lua VM — no cgo, static builds intact. Each execution
// runs on a fresh, context-bound, sandboxed LState (base/table/string/math
// only; the load family removed; io/os never loaded; print is the one
// recorded stdout side-channel).
//
// Scripts read process data lazily through the read-only `data` global —
// an absent datum RAISES naming it (the fail-loud house rule over Lua's
// nil idiom; probe optional data with `has(name)`) — and produce outputs
// by returning a table of named values. Lua numbers land as float64 (the
// language's single number type).
package lua

import (
	"context"
	"sort"

	glua "github.com/yuin/gopher-lua"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/script"
)

const (
	errorClass = "LUA"

	// LuaType is the engine kind (the "##"-hint convention).
	LuaType = "##Lua"
)

// formats are the scriptFormat MIME hints this engine claims.
var formats = []string{"text/x-lua", "application/x-lua", "lua"}

// Engine is the Lua script.Engine. It is stateless — every Execute builds
// its own sandboxed LState — so one Engine serves concurrent tracks.
type Engine struct{}

// interface check
var _ script.Engine = (*Engine)(nil)

// New creates the Lua engine.
func New() *Engine {
	return &Engine{}
}

// Type returns the engine kind.
func (e *Engine) Type() string {
	return LuaType
}

// Formats returns the claimed scriptFormat MIME hints.
func (e *Engine) Formats() []string {
	return append([]string{}, formats...)
}

// unsafeBaseFns are the base-library escape hatches the sandbox removes.
var unsafeBaseFns = []string{"dofile", "loadfile", "load", "loadstring"}

// Execute runs body on a fresh sandboxed LState bound to ctx and returns
// the script's named outputs (the returned table). A missing datum, an
// unmappable value, a non-table return and any Lua error fail loud.
func (e *Engine) Execute(
	ctx context.Context,
	format, body string,
	r service.DataReader,
) (script.Outputs, error) {
	if body == "" {
		return nil, errs.New(
			errs.M("Execute: an empty script isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if r == nil {
		return nil, errs.New(
			errs.M("Execute: a nil DataReader isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	l := glua.NewState(glua.Options{SkipOpenLibs: true})
	defer l.Close()

	l.SetContext(ctx)

	if err := openSandbox(l); err != nil {
		return nil, err
	}

	installDataAPI(ctx, l, r)

	if err := l.DoString(body); err != nil {
		return nil, errs.New(
			errs.M("script failed"),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err),
			errs.D("script_format", format))
	}

	return chunkOutputs(l, format)
}

// openSandbox loads the safe library set and removes the base library's
// escape hatches (FR-3).
func openSandbox(l *glua.LState) error {
	for _, lib := range []struct {
		fn   glua.LGFunction
		name string
	}{
		{glua.OpenBase, glua.BaseLibName},
		{glua.OpenTable, glua.TabLibName},
		{glua.OpenString, glua.StringLibName},
		{glua.OpenMath, glua.MathLibName},
	} {
		if err := l.CallByParam(glua.P{
			Fn:      l.NewFunction(lib.fn),
			NRet:    0,
			Protect: true,
		}, glua.LString(lib.name)); err != nil {
			return errs.New(
				errs.M("couldn't open the %q library", lib.name),
				errs.C(errorClass, errs.OperationFailed),
				errs.E(err))
		}
	}

	for _, fn := range unsafeBaseFns {
		l.SetGlobal(fn, glua.LNil)
	}

	return nil
}

// installDataAPI installs the read-only lazy `data` table and the
// `has(name)` probe (FR-4).
func installDataAPI(
	ctx context.Context, l *glua.LState, r service.DataReader,
) {
	dataTbl := l.NewTable()
	meta := l.NewTable()

	meta.RawSetString("__index", l.NewFunction(func(ls *glua.LState) int {
		name := ls.CheckString(2)

		d, err := r.GetData(name)
		if err != nil {
			ls.RaiseError("no process datum %q (probe optional data "+
				"with has())", name)

			return 0
		}

		lv, err := toLua(ls, d.Value().Get(ctx))
		if err != nil {
			ls.RaiseError("datum %q: %s", name, err.Error())

			return 0
		}

		ls.Push(lv)

		return 1
	}))

	meta.RawSetString("__newindex", l.NewFunction(func(ls *glua.LState) int {
		ls.RaiseError("the data table is read-only — return outputs " +
			"from the script instead")

		return 0
	}))

	l.SetMetatable(dataTbl, meta)
	l.SetGlobal("data", dataTbl)

	l.SetGlobal("has", l.NewFunction(func(ls *glua.LState) int {
		_, err := r.GetData(ls.CheckString(1))
		ls.Push(glua.LBool(err == nil))

		return 1
	}))
}

// chunkOutputs converts the chunk's returned table into script.Outputs
// (FR-5): nothing/nil = no outputs; a non-table return or a non-string
// key is a classified error.
func chunkOutputs(l *glua.LState, format string) (script.Outputs, error) {
	ret := l.Get(-1)

	if ret == glua.LNil || l.GetTop() == 0 {
		return nil, nil
	}

	tbl, ok := ret.(*glua.LTable)
	if !ok {
		return nil, errs.New(
			errs.M("a script must return a table of named outputs "+
				"(got %s)", ret.Type().String()),
			errs.C(errorClass, errs.InvalidObject),
			errs.D("script_format", format))
	}

	raw, err := tableToGo(tbl)
	if err != nil {
		return nil, errs.New(
			errs.M("couldn't map the script's outputs"),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err),
			errs.D("script_format", format))
	}

	named, ok := raw.(map[string]any)
	if !ok {
		return nil, errs.New(
			errs.M("a script's outputs must be named "+
				"(a sequence table isn't)"),
			errs.C(errorClass, errs.InvalidObject),
			errs.D("script_format", format))
	}

	outs := script.Outputs{}

	// sorted for deterministic error order on a bad entry.
	names := make([]string, 0, len(named))
	for n := range named {
		names = append(names, n)
	}

	sort.Strings(names)

	for _, n := range names {
		outs[n] = values.NewVariable(named[n])
	}

	return outs, nil
}
