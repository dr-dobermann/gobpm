package tasks_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	exprengine "github.com/dr-dobermann/gobpm/pkg/model/expression/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/tasks"
	"github.com/stretchr/testify/require"
)

// readName builds a FormalExpression that reads name from the source and reports
// whether its string value equals want — the shape an ErrorMapper body-clause
// takes over the transient fault Source (SRD-037 §3.3).
func readName(t *testing.T, name, want string) data.FormalExpression {
	t.Helper()

	fe, err := goexpr.New(nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			d, err := ds.Find(ctx, name)
			if err != nil {
				return nil, err
			}

			s, _ := d.Value().Get(ctx).(string)

			return values.NewVariable(s == want), nil
		})
	require.NoError(t, err)

	return fe
}

func TestRuleMapperCodeOnlyToBpmnError(t *testing.T) {
	m, err := tasks.NewRuleMapper(
		tasks.Rule{Code: "409", Yield: tasks.BpmnError{Code: "ResourceConflict"}})
	require.NoError(t, err)

	out, err := m.Classify(context.Background(), exprengine.New(),
		tasks.Fault{Code: "409"})
	require.NoError(t, err)

	be, ok := out.(tasks.BpmnError)
	require.True(t, ok)
	require.Equal(t, "ResourceConflict", be.Code)
}

func TestRuleMapperBodyClauseToStatus(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	body := data.MustItemDefinition(values.NewVariable("NOT_FOUND"),
		foundation.WithID("body"))

	m, err := tasks.NewRuleMapper(
		tasks.Rule{
			Code:       "404",
			BodyClause: readName(t, "body", "NOT_FOUND"),
			Yield:      tasks.Status{Value: values.NewVariable("NOT_FOUND")},
		})
	require.NoError(t, err)

	out, err := m.Classify(context.Background(), exprengine.New(),
		tasks.Fault{Code: "404", Body: body})
	require.NoError(t, err)

	st, ok := out.(tasks.Status)
	require.True(t, ok)
	require.Equal(t, "NOT_FOUND", st.Value.Get(context.Background()))
}

// TestRuleMapperCodeClauseMatches covers the faultSource "code" datum path: a
// body-clause that reads the fault's code.
func TestRuleMapperCodeClauseMatches(t *testing.T) {
	m, err := tasks.NewRuleMapper(
		tasks.Rule{
			BodyClause: readName(t, "code", "418"),
			Yield:      tasks.BpmnError{Code: "Teapot"},
		})
	require.NoError(t, err)

	out, err := m.Classify(context.Background(), exprengine.New(),
		tasks.Fault{Code: "418"})
	require.NoError(t, err)
	require.IsType(t, tasks.BpmnError{}, out)
}

func TestRuleMapperNoMatchIsTechnical(t *testing.T) {
	m, err := tasks.NewRuleMapper(
		tasks.Rule{Code: "404", Yield: tasks.BpmnError{Code: "X"}})
	require.NoError(t, err)

	out, err := m.Classify(context.Background(), exprengine.New(),
		tasks.Fault{Code: "500"})
	require.NoError(t, err)
	require.IsType(t, tasks.Technical{}, out)
}

// TestRuleMapperBodyClauseFalseFallsThrough: a matching code but a false
// body-clause skips the rule and falls through to technical.
func TestRuleMapperBodyClauseFalseFallsThrough(t *testing.T) {
	body := data.MustItemDefinition(values.NewVariable("OTHER"),
		foundation.WithID("body"))

	m, err := tasks.NewRuleMapper(
		tasks.Rule{
			Code:       "404",
			BodyClause: readName(t, "body", "NOT_FOUND"),
			Yield:      tasks.Status{Value: values.NewVariable("x")},
		})
	require.NoError(t, err)

	out, err := m.Classify(context.Background(), exprengine.New(),
		tasks.Fault{Code: "404", Body: body})
	require.NoError(t, err)
	require.IsType(t, tasks.Technical{}, out)
}

func TestRuleMapperFirstMatchWins(t *testing.T) {
	m, err := tasks.NewRuleMapper(
		tasks.Rule{Yield: tasks.BpmnError{Code: "First"}}, // "" matches any
		tasks.Rule{Code: "409", Yield: tasks.BpmnError{Code: "Second"}})
	require.NoError(t, err)

	out, err := m.Classify(context.Background(), exprengine.New(),
		tasks.Fault{Code: "409"})
	require.NoError(t, err)
	require.Equal(t, "First", out.(tasks.BpmnError).Code)
}

func TestNewRuleMapperRejectsNilYield(t *testing.T) {
	_, err := tasks.NewRuleMapper(tasks.Rule{Code: "409"})
	require.Error(t, err)
}

// TestRuleMapperBodyClauseError: a body-clause reading a datum the fault lacks
// (nil body) surfaces the evaluation error from Classify.
func TestRuleMapperBodyClauseError(t *testing.T) {
	m, err := tasks.NewRuleMapper(
		tasks.Rule{
			Code:       "404",
			BodyClause: readName(t, "body", "x"),
			Yield:      tasks.Status{Value: values.NewVariable("x")},
		})
	require.NoError(t, err)

	// Fault carries no body → faultSource.Find("body") errors → Classify errors.
	_, err = m.Classify(context.Background(), exprengine.New(),
		tasks.Fault{Code: "404"})
	require.Error(t, err)
}

// TestRuleMapperUnknownDatumError: a body-clause reading an unknown datum name
// surfaces the faultSource not-found error.
func TestRuleMapperUnknownDatumError(t *testing.T) {
	m, err := tasks.NewRuleMapper(
		tasks.Rule{
			BodyClause: readName(t, "nope", "x"),
			Yield:      tasks.Technical{},
		})
	require.NoError(t, err)

	_, err = m.Classify(context.Background(), exprengine.New(),
		tasks.Fault{Code: "404"})
	require.Error(t, err)
}
