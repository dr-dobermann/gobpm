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
	"github.com/dr-dobermann/gobpm/pkg/rules"
	"github.com/dr-dobermann/gobpm/pkg/rules/gorules"
)

func TestBusinessRuleTaskDefinition(t *testing.T) {
	t.Run("empty decision reference rejected",
		func(t *testing.T) {
			_, err := activities.NewBusinessRuleTask("check", "  ")
			require.Error(t, err)
			require.Contains(t, err.Error(), "decision reference")
		})

	t.Run("empty name rejected",
		func(t *testing.T) {
			_, err := activities.NewBusinessRuleTask("", "discount")
			require.Error(t, err)
		})

	t.Run("invalid option rejected",
		func(t *testing.T) {
			_, err := activities.NewBusinessRuleTask("check", "discount",
				events.WithParallel())
			require.Error(t, err)
		})

	t.Run("definition and getters",
		func(t *testing.T) {
			bt, err := activities.NewBusinessRuleTask("check", " discount ")
			require.NoError(t, err)

			require.Equal(t, "check", bt.Name())
			require.Equal(t, "discount", bt.DecisionRef())
			require.Equal(t, flow.BusinessRuleTask, bt.TaskType())
			require.Equal(t, bt, bt.Node())
		})

	t.Run("clone carries the decision reference",
		func(t *testing.T) {
			bt, err := activities.NewBusinessRuleTask("check", "discount")
			require.NoError(t, err)

			n, err := bt.Clone()
			require.NoError(t, err)

			cloned, ok := n.(*activities.BusinessRuleTask)
			require.True(t, ok)
			require.NotSame(t, bt, cloned)
			require.Equal(t, "discount", cloned.DecisionRef())
			require.Equal(t, "check", cloned.Name())
		})
}

// factSink collects reported facts for assertions.
type factSink struct {
	facts []observability.Fact
}

func (fs *factSink) Report(f observability.Fact) {
	fs.facts = append(fs.facts, f)
}

func TestBusinessRuleTaskExec(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()

	newBRT := func(t *testing.T, ref string) *activities.BusinessRuleTask {
		t.Helper()

		bt, err := activities.NewBusinessRuleTask("check", ref)
		require.NoError(t, err)

		return bt
	}

	t.Run("nil runtime environment rejected",
		func(t *testing.T) {
			_, err := newBRT(t, "discount").Exec(ctx, nil)
			require.Error(t, err)
			require.Contains(t, err.Error(), "RuntimeEnvironment")
		})

	t.Run("a 1x1 result folds to a scalar under its output name",
		func(t *testing.T) {
			eng := gorules.New().MustRegister("discount",
				func(
					_ context.Context,
					_ service.DataReader,
				) (rules.Row, error) {
					return rules.Row{
						"discount_pct": values.NewVariable(15),
					}, nil
				})

			sink := &factSink{}
			re := mockrenv.NewMockRuntimeEnvironment(t)
			re.EXPECT().RuleEngine().Return(eng)
			re.EXPECT().Reporter().Return(sink)
			re.EXPECT().Put(mock.MatchedBy(func(dd []data.Data) bool {
				return len(dd) == 1 &&
					dd[0].Name() == "discount_pct" &&
					dd[0].Value().Get(ctx) == 15
			})).Return(nil)

			flows, err := newBRT(t, "discount").Exec(ctx, re)
			require.NoError(t, err)
			require.Empty(t, flows)

			// the FR-6 Evaluated fact carries the decision-level details.
			require.Len(t, sink.facts, 1)
			f := sink.facts[0]
			require.Equal(t, observability.KindRules, f.Kind)
			require.Equal(t, observability.PhaseEvaluated, f.Phase)
			require.Equal(t, "check", f.NodeName)
			require.Equal(t, "discount",
				f.Details[observability.AttrDecisionRef])
			require.Equal(t, gorules.GoRulesType,
				f.Details[observability.AttrImplementation])
			require.Equal(t, "1", f.Details[observability.AttrRowCount])
			require.Equal(t, "discount_pct",
				f.Details[observability.AttrResultVariable])
		})

	t.Run("a multi-output row commits as a row list under the decision ref",
		func(t *testing.T) {
			eng := gorules.New().MustRegister("route",
				func(
					_ context.Context,
					_ service.DataReader,
				) (rules.Row, error) {
					return rules.Row{
						"lane":     values.NewVariable("fast"),
						"priority": values.NewVariable(3),
					}, nil
				})

			sink := &factSink{}
			re := mockrenv.NewMockRuntimeEnvironment(t)
			re.EXPECT().RuleEngine().Return(eng)
			re.EXPECT().Reporter().Return(sink)
			re.EXPECT().Put(mock.MatchedBy(func(dd []data.Data) bool {
				if len(dd) != 1 || dd[0].Name() != "route" {
					return false
				}

				rows, ok := dd[0].Value().(*values.Array[data.Value])

				return ok && rows.Count() == 1
			})).Return(nil)

			_, err := newBRT(t, "route").Exec(ctx, re)
			require.NoError(t, err)
			require.Len(t, sink.facts, 1)
			require.Equal(t, "route",
				sink.facts[0].Details[observability.AttrResultVariable])
		})

	t.Run("an empty result commits nothing",
		func(t *testing.T) {
			eng := gorules.New().MustRegister("silent",
				func(
					_ context.Context,
					_ service.DataReader,
				) (rules.Row, error) {
					return nil, nil
				})

			sink := &factSink{}
			re := mockrenv.NewMockRuntimeEnvironment(t)
			re.EXPECT().RuleEngine().Return(eng)
			re.EXPECT().Reporter().Return(sink)
			// no Put expectation: the strict mock fails the test on any call.

			_, err := newBRT(t, "silent").Exec(ctx, re)
			require.NoError(t, err)
			require.Len(t, sink.facts, 1)
			require.Equal(t, "0",
				sink.facts[0].Details[observability.AttrRowCount])
			require.Empty(t,
				sink.facts[0].Details[observability.AttrResultVariable])
		})

	t.Run("an empty output name fails the commit",
		func(t *testing.T) {
			eng := gorules.New().MustRegister("anon",
				func(
					_ context.Context,
					_ service.DataReader,
				) (rules.Row, error) {
					return rules.Row{"": values.NewVariable(1)}, nil
				})

			re := mockrenv.NewMockRuntimeEnvironment(t)
			re.EXPECT().RuleEngine().Return(eng)

			_, err := newBRT(t, "anon").Exec(ctx, re)
			require.Error(t, err)
			require.Contains(t, err.Error(), "anon")
		})

	t.Run("an empty output name in a multi-output row fails the fold",
		func(t *testing.T) {
			eng := gorules.New().MustRegister("anon2",
				func(
					_ context.Context,
					_ service.DataReader,
				) (rules.Row, error) {
					return rules.Row{
						"":   values.NewVariable(1),
						"ok": values.NewVariable(2),
					}, nil
				})

			re := mockrenv.NewMockRuntimeEnvironment(t)
			re.EXPECT().RuleEngine().Return(eng)

			_, err := newBRT(t, "anon2").Exec(ctx, re)
			require.Error(t, err)
			require.Contains(t, err.Error(), "couldn't fold decision result")
		})

	t.Run("engine error fails the task and reports the Failed fact",
		func(t *testing.T) {
			sink := &factSink{}
			re := mockrenv.NewMockRuntimeEnvironment(t)
			re.EXPECT().RuleEngine().Return(gorules.New())
			re.EXPECT().Reporter().Return(sink)

			_, err := newBRT(t, "unknown").Exec(ctx, re)
			require.Error(t, err)
			require.Contains(t, err.Error(), "unknown")

			require.Len(t, sink.facts, 1)
			f := sink.facts[0]
			require.Equal(t, observability.KindRules, f.Kind)
			require.Equal(t, observability.PhaseFailed, f.Phase)
			require.Equal(t, "unknown",
				f.Details[observability.AttrDecisionRef])
			require.NotEmpty(t, f.Details[observability.AttrError])
		})

	t.Run("commit failure is wrapped with the task identity",
		func(t *testing.T) {
			eng := gorules.New().MustRegister("discount",
				func(
					_ context.Context,
					_ service.DataReader,
				) (rules.Row, error) {
					return rules.Row{
						"discount_pct": values.NewVariable(15),
					}, nil
				})

			re := mockrenv.NewMockRuntimeEnvironment(t)
			re.EXPECT().RuleEngine().Return(eng)
			re.EXPECT().Put(mock.Anything).
				Return(errs.New(errs.M("scope rejected the parameter")))

			_, err := newBRT(t, "discount").Exec(ctx, re)
			require.Error(t, err)
			require.Contains(t, err.Error(), "couldn't commit decision result")
			require.Contains(t, err.Error(), "check")
		})
}
