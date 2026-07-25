package script_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/script"
)

var ctx = context.Background()

// stubEngine answers the given formats and records its executions.
type stubEngine struct {
	kind    string
	formats []string
	err     error
	calls   []string // "format|script"
}

func (se *stubEngine) Type() string { return se.kind }

func (se *stubEngine) Formats() []string { return se.formats }

func (se *stubEngine) Execute(
	_ context.Context, format, body string, _ service.DataReader,
) (script.Outputs, error) {
	se.calls = append(se.calls, format+"|"+body)

	if se.err != nil {
		return nil, se.err
	}

	return script.Outputs{"ran": values.NewVariable(se.kind)}, nil
}

// nilReader is a placeholder DataReader for routing tests.
type nilReader struct{}

func (nilReader) GetData(string) (data.Data, error) {
	return nil, errs.New(errs.M("no data"))
}

func (nilReader) GetDataByID(string) (data.Data, error) {
	return nil, errs.New(errs.M("no data"))
}

func (nilReader) GetSources() []string { return nil }

func (nilReader) List(string) ([]string, error) { return nil, nil }

// TestRegistryConstruction covers SRD-064 T-1's validation half.
func TestRegistryConstruction(t *testing.T) {
	lua := &stubEngine{kind: "##Lua", formats: []string{"text/x-lua", "lua"}}

	t.Run("nil engine rejected",
		func(t *testing.T) {
			_, err := script.NewRegistry(lua, nil)
			require.Error(t, err)
			require.Contains(t, err.Error(), "engine 1")
		})

	t.Run("formatless engine rejected",
		func(t *testing.T) {
			_, err := script.NewRegistry(&stubEngine{kind: "##Empty"})
			require.Error(t, err)
			require.Contains(t, err.Error(), "##Empty")
		})

	t.Run("blank format claim rejected",
		func(t *testing.T) {
			_, err := script.NewRegistry(
				&stubEngine{kind: "##Blank", formats: []string{"  "}})
			require.Error(t, err)
			require.Contains(t, err.Error(), "##Blank")
		})

	t.Run("duplicate claim names both kinds and the format",
		func(t *testing.T) {
			other := &stubEngine{kind: "##Other",
				formats: []string{"TEXT/X-LUA"}} // case-insensitive clash

			_, err := script.NewRegistry(lua, other)
			require.Error(t, err)
			require.Contains(t, err.Error(), "text/x-lua")
			require.Contains(t, err.Error(), "##Lua")
			require.Contains(t, err.Error(), "##Other")
		})

	t.Run("aggregation and claims",
		func(t *testing.T) {
			st := &stubEngine{kind: "##Starlark",
				formats: []string{"text/x-python"}}

			reg, err := script.NewRegistry(lua, st)
			require.NoError(t, err)

			require.Equal(t, "##Lua+##Starlark", reg.Type())
			require.Equal(t,
				[]string{"lua", "text/x-lua", "text/x-python"},
				reg.Formats())

			e, ok := reg.EngineFor("Text/X-Python")
			require.True(t, ok)
			require.Equal(t, "##Starlark", e.Type())

			_, ok = reg.EngineFor("text/x-ruby")
			require.False(t, ok)
		})
}

// TestRegistryExecute covers SRD-064 T-1's routing half.
func TestRegistryExecute(t *testing.T) {
	t.Run("empty registry is ##None and fails with the wiring hint",
		func(t *testing.T) {
			reg, err := script.NewRegistry()
			require.NoError(t, err)

			require.Equal(t, script.NoneType, reg.Type())
			require.Empty(t, reg.Formats())

			_, err = reg.Execute(ctx, "text/x-lua", "return {}", nilReader{})
			require.Error(t, err)
			require.Contains(t, err.Error(), "WithScriptEngine")
		})

	t.Run("routes by normalized format",
		func(t *testing.T) {
			lua := &stubEngine{kind: "##Lua", formats: []string{"text/x-lua"}}
			st := &stubEngine{kind: "##Starlark",
				formats: []string{"text/x-python"}}

			reg, err := script.NewRegistry(lua, st)
			require.NoError(t, err)

			outs, err := reg.Execute(ctx, " TEXT/X-PYTHON ", "x = 1",
				nilReader{})
			require.NoError(t, err)
			require.Equal(t, "##Starlark", outs["ran"].Get(ctx))
			require.Empty(t, lua.calls)
			require.Len(t, st.calls, 1)
		})

	t.Run("unclaimed format lists the registered claims",
		func(t *testing.T) {
			lua := &stubEngine{kind: "##Lua", formats: []string{"text/x-lua"}}

			reg, err := script.NewRegistry(lua)
			require.NoError(t, err)

			_, err = reg.Execute(ctx, "text/x-ruby", "puts 1", nilReader{})
			require.Error(t, err)
			require.Contains(t, err.Error(), "text/x-ruby")
			require.Contains(t, err.Error(), "text/x-lua")
		})

	t.Run("parameter validation",
		func(t *testing.T) {
			reg, err := script.NewRegistry(
				&stubEngine{kind: "##Lua", formats: []string{"lua"}})
			require.NoError(t, err)

			_, err = reg.Execute(ctx, "  ", "x", nilReader{})
			require.Error(t, err)

			_, err = reg.Execute(ctx, "lua", "x", nil)
			require.Error(t, err)
		})

	t.Run("a routed engine's own error passes through unwrapped",
		func(t *testing.T) {
			boom := &stubEngine{kind: "##Boom", formats: []string{"boom"},
				err: errs.New(errs.M("syntax error at line 3"))}

			reg, err := script.NewRegistry(boom)
			require.NoError(t, err)

			_, err = reg.Execute(ctx, "boom", "x", nilReader{})
			require.Error(t, err)
			require.Contains(t, err.Error(), "syntax error at line 3")
		})
}
