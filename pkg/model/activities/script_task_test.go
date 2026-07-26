package activities_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/generated/mockrenv"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/script"
)

// scriptStub is a controllable script.Engine for Exec tests.
type scriptStub struct {
	kind    string
	formats []string
	outs    script.Outputs
	err     error
	gotFmt  string
	gotBody string
}

func (ss *scriptStub) Type() string { return ss.kind }

func (ss *scriptStub) Formats() []string { return ss.formats }

func (ss *scriptStub) Execute(
	_ context.Context, format, body string, _ service.DataReader,
) (script.Outputs, error) {
	ss.gotFmt, ss.gotBody = format, body

	return ss.outs, ss.err
}

func TestScriptTaskDefinition(t *testing.T) {
	t.Run("empty format rejected",
		func(t *testing.T) {
			_, err := activities.NewScriptTask("calc", " ", "x = 1")
			require.Error(t, err)
			require.Contains(t, err.Error(), "format")
		})

	t.Run("empty script rejected",
		func(t *testing.T) {
			_, err := activities.NewScriptTask("calc", "text/x-lua", "  ")
			require.Error(t, err)
			require.Contains(t, err.Error(), "script")
		})

	t.Run("empty name rejected",
		func(t *testing.T) {
			_, err := activities.NewScriptTask("", "text/x-lua", "x = 1")
			require.Error(t, err)
		})

	t.Run("invalid option rejected",
		func(t *testing.T) {
			_, err := activities.NewScriptTask("calc", "text/x-lua", "x = 1",
				events.WithParallel())
			require.Error(t, err)
		})

	t.Run("definition and getters",
		func(t *testing.T) {
			st, err := activities.NewScriptTask("calc", " text/x-lua ",
				"return {x = 1}")
			require.NoError(t, err)

			require.Equal(t, "calc", st.Name())
			require.Equal(t, "text/x-lua", st.ScriptFormat())
			require.Equal(t, "return {x = 1}", st.Script())
			require.Equal(t, flow.ScriptTask, st.TaskType())
			require.Equal(t, st, st.Node())
		})

	t.Run("clone carries format and script",
		func(t *testing.T) {
			st, err := activities.NewScriptTask("calc", "text/x-lua",
				"return {x = 1}")
			require.NoError(t, err)

			n, err := st.Clone()
			require.NoError(t, err)

			cloned, ok := n.(*activities.ScriptTask)
			require.True(t, ok)
			require.NotSame(t, st, cloned)
			require.Equal(t, "text/x-lua", cloned.ScriptFormat())
			require.Equal(t, "return {x = 1}", cloned.Script())
		})
}

func TestScriptTaskExec(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()

	newST := func(t *testing.T) *activities.ScriptTask {
		t.Helper()

		st, err := activities.NewScriptTask("calc", "text/x-fake",
			"out = total * 2")
		require.NoError(t, err)

		return st
	}

	t.Run("nil runtime environment rejected",
		func(t *testing.T) {
			_, err := newST(t).Exec(ctx, nil)
			require.Error(t, err)
			require.Contains(t, err.Error(), "RuntimeEnvironment")
		})

	t.Run("outputs committed per-name in sorted order",
		func(t *testing.T) {
			eng := &scriptStub{kind: "##Fake",
				formats: []string{"text/x-fake"},
				outs: script.Outputs{
					"beta":  values.NewVariable(2),
					"alpha": values.NewVariable(1),
				}}

			var order []string

			sink := &factSink{}
			re := mockrenv.NewMockRuntimeEnvironment(t)
			re.EXPECT().ScriptEngine().Return(eng)
			re.EXPECT().Reporter().Return(sink)
			re.EXPECT().Put(mock.MatchedBy(func(dd []data.Data) bool {
				if len(dd) != 1 {
					return false
				}

				order = append(order, dd[0].Name())

				return true
			})).Return(nil).Twice()

			flows, err := newST(t).Exec(ctx, re)
			require.NoError(t, err)
			require.Empty(t, flows)

			require.Equal(t, []string{"alpha", "beta"}, order,
				"outputs must commit in sorted name order")
			require.Equal(t, "text/x-fake", eng.gotFmt)
			require.Equal(t, "out = total * 2", eng.gotBody)

			// the SRD-069 pair: Invoked opens, Executed closes.
			require.Len(t, sink.facts, 2)

			inv := sink.facts[0]
			require.Equal(t, observability.KindScript, inv.Kind)
			require.Equal(t, observability.PhaseInvoked, inv.Phase)
			require.Equal(t, "text/x-fake",
				inv.Details[observability.AttrScriptFormat])
			require.Equal(t, "##Fake",
				inv.Details[observability.AttrImplementation])
			require.Empty(t, inv.Details[observability.AttrStage])

			f := sink.facts[1]
			require.Equal(t, observability.KindScript, f.Kind)
			require.Equal(t, observability.PhaseExecuted, f.Phase)
			require.Equal(t, "text/x-fake",
				f.Details[observability.AttrScriptFormat])
			require.Equal(t, "##Fake",
				f.Details[observability.AttrImplementation])
			require.Equal(t, "2", f.Details[observability.AttrOutputCount])
		})

	t.Run("registry-routed engine kind lands in the fact",
		func(t *testing.T) {
			eng := &scriptStub{kind: "##Fake",
				formats: []string{"text/x-fake"},
				outs:    script.Outputs{"x": values.NewVariable(1)}}

			reg, err := script.NewRegistry(eng)
			require.NoError(t, err)

			sink := &factSink{}
			re := mockrenv.NewMockRuntimeEnvironment(t)
			re.EXPECT().ScriptEngine().Return(reg)
			re.EXPECT().Reporter().Return(sink)
			re.EXPECT().Put(mock.Anything).Return(nil)

			_, err = newST(t).Exec(ctx, re)
			require.NoError(t, err)

			require.Len(t, sink.facts, 2)
			for _, f := range sink.facts {
				require.Equal(t, "##Fake",
					f.Details[observability.AttrImplementation],
					"the facts must name the ROUTED engine, not the aggregate")
			}
		})

	t.Run("empty outputs commit nothing",
		func(t *testing.T) {
			eng := &scriptStub{kind: "##Fake",
				formats: []string{"text/x-fake"}}

			sink := &factSink{}
			re := mockrenv.NewMockRuntimeEnvironment(t)
			re.EXPECT().ScriptEngine().Return(eng)
			re.EXPECT().Reporter().Return(sink)
			// no Put expectation: the strict mock fails on any call.

			_, err := newST(t).Exec(ctx, re)
			require.NoError(t, err)
			require.Len(t, sink.facts, 2)
			require.Equal(t, "0",
				sink.facts[1].Details[observability.AttrOutputCount])
		})

	t.Run("engine error fails the task and reports the Failed fact",
		func(t *testing.T) {
			eng := &scriptStub{kind: "##Fake",
				formats: []string{"text/x-fake"},
				err:     errs.New(errs.M("lua: syntax error"))}

			sink := &factSink{}
			re := mockrenv.NewMockRuntimeEnvironment(t)
			re.EXPECT().ScriptEngine().Return(eng)
			re.EXPECT().Reporter().Return(sink)

			_, err := newST(t).Exec(ctx, re)
			require.Error(t, err)
			require.Contains(t, err.Error(), "syntax error")

			require.Len(t, sink.facts, 2)
			require.Equal(t, observability.PhaseInvoked, sink.facts[0].Phase)

			f := sink.facts[1]
			require.Equal(t, observability.PhaseFailed, f.Phase)
			require.Equal(t, "engine",
				f.Details[observability.AttrStage])
			require.NotEmpty(t, f.Details[observability.AttrError])
		})

	t.Run("commit failure is wrapped with the task identity",
		func(t *testing.T) {
			eng := &scriptStub{kind: "##Fake",
				formats: []string{"text/x-fake"},
				outs:    script.Outputs{"x": values.NewVariable(1)}}

			sink := &factSink{}
			re := mockrenv.NewMockRuntimeEnvironment(t)
			re.EXPECT().ScriptEngine().Return(eng)
			re.EXPECT().Reporter().Return(sink)
			re.EXPECT().Put(mock.Anything).
				Return(errs.New(errs.M("scope rejected the parameter")))

			_, err := newST(t).Exec(ctx, re)
			require.Error(t, err)
			require.Contains(t, err.Error(), "couldn't commit script output")
			require.Contains(t, err.Error(), "calc")

			// the pair closes on the commit stage (SRD-069 FR-2).
			require.Len(t, sink.facts, 2)
			require.Equal(t, observability.PhaseFailed, sink.facts[1].Phase)
			require.Equal(t, "commit",
				sink.facts[1].Details[observability.AttrStage])
		})

	t.Run("a bad output name fails the commit loud",
		func(t *testing.T) {
			eng := &scriptStub{kind: "##Fake",
				formats: []string{"text/x-fake"},
				outs:    script.Outputs{"": values.NewVariable(1)}}

			sink := &factSink{}
			re := mockrenv.NewMockRuntimeEnvironment(t)
			re.EXPECT().ScriptEngine().Return(eng)
			re.EXPECT().Reporter().Return(sink)

			_, err := newST(t).Exec(ctx, re)
			require.Error(t, err)
			require.Contains(t, err.Error(), "couldn't commit script output")
			require.Equal(t, "commit",
				sink.facts[1].Details[observability.AttrStage])
		})
}
