package localdispatcher_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/clock/clocktest"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	dgoexpr "github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	expengine "github.com/dr-dobermann/gobpm/pkg/model/expression/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/tasks"
	"github.com/dr-dobermann/gobpm/pkg/tasks/localdispatcher"
	"github.com/stretchr/testify/require"
)

// mustRuleMapper builds a tasks.RuleMapper from rules or fails the test.
func mustRuleMapper(t *testing.T, rules ...tasks.Rule) tasks.ErrorMapper {
	t.Helper()

	m, err := tasks.NewRuleMapper(rules...)
	require.NoError(t, err)

	return m
}

// enqueueAndLock enqueues job "j1" on topic "charge" carrying policy and locks it
// to worker "w1", leaving the dispatcher ready for a Fail report.
func enqueueAndLock(
	t *testing.T, d *localdispatcher.Dispatcher, policy *tasks.Policy,
) {
	t.Helper()

	ctx := context.Background()
	require.NoError(t, d.Enqueue(ctx,
		tasks.Job{ID: "j1", Topic: "charge", Policy: policy}))
	_, err := d.FetchAndLock(ctx, "w1", topics("charge"), time.Minute)
	require.NoError(t, err)
}

// bodyRequiringClause builds a body-clause that reads "body" — it errors when the
// fault carries none, exercising the ErrorMapper evaluation-error path.
func bodyRequiringClause(t *testing.T) data.FormalExpression {
	t.Helper()

	fe, err := dgoexpr.New(nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			if _, err := ds.Find(ctx, "body"); err != nil {
				return nil, err
			}

			return values.NewVariable(true), nil
		})
	require.NoError(t, err)

	return fe
}

// TestDispatcherClassifiesFaultToBpmnError: a raw Fail is classified engine-side
// by the job's Policy.ErrorMapper into a Business Error terminal (SRD-038 FR-1).
func TestDispatcherClassifiesFaultToBpmnError(t *testing.T) {
	sink := &recordSink{}
	d := localdispatcher.New(clocktest.New(base), time.Minute)
	d.BindSink(sink)
	d.BindExpressionEngine(expengine.New())

	enqueueAndLock(t, d, &tasks.Policy{ErrorMapper: mustRuleMapper(t,
		tasks.Rule{Code: "409", Yield: tasks.BpmnError{Code: "Conflict", Message: "dup"}})})

	require.NoError(t, d.Fail(context.Background(), "j1", "w1",
		tasks.Fault{Code: "409"}))

	out := sink.last()
	require.Equal(t, tasks.OutcomeBpmnError, out.Kind())
	code, msg := out.BpmnError()
	require.Equal(t, "Conflict", code)
	require.Equal(t, "dup", msg)
}

// TestDispatcherClassifiesFaultToStatus: a raw Fail mapped to a Business Status
// terminal (SRD-038 FR-1).
func TestDispatcherClassifiesFaultToStatus(t *testing.T) {
	sink := &recordSink{}
	d := localdispatcher.New(clocktest.New(base), time.Minute)
	d.BindSink(sink)
	d.BindExpressionEngine(expengine.New())

	enqueueAndLock(t, d, &tasks.Policy{ErrorMapper: mustRuleMapper(t,
		tasks.Rule{Code: "404", Yield: tasks.Status{Value: values.NewVariable("NOT_FOUND")}})})

	require.NoError(t, d.Fail(context.Background(), "j1", "w1",
		tasks.Fault{Code: "404"}))

	out := sink.last()
	require.Equal(t, tasks.OutcomeStatus, out.Kind())
	require.NotNil(t, out.StatusValue())
}

// TestDispatcherUnmatchedFaultIsTechnical: a fault matching no rule falls through
// to the default Technical outcome — a terminal raw fault (SRD-038 §3.4).
func TestDispatcherUnmatchedFaultIsTechnical(t *testing.T) {
	sink := &recordSink{}
	d := localdispatcher.New(clocktest.New(base), time.Minute)
	d.BindSink(sink)
	d.BindExpressionEngine(expengine.New())

	enqueueAndLock(t, d, &tasks.Policy{ErrorMapper: mustRuleMapper(t,
		tasks.Rule{Code: "409", Yield: tasks.BpmnError{Code: "Conflict"}})})

	require.NoError(t, d.Fail(context.Background(), "j1", "w1",
		tasks.Fault{Code: "500", Cause: errors.New("boom")}))

	require.Equal(t, tasks.OutcomeFault, sink.last().Kind())
}

// TestDispatcherFaultWithoutEngineDefaultsTechnical: a mapper is configured but no
// expression engine is bound, so the dispatcher can't run it and falls through to
// the technical outcome (SRD-038 §3.4, nil-engine guard).
func TestDispatcherFaultWithoutEngineDefaultsTechnical(t *testing.T) {
	sink := &recordSink{}
	d := localdispatcher.New(clocktest.New(base), time.Minute)
	d.BindSink(sink)
	// no BindExpressionEngine.

	enqueueAndLock(t, d, &tasks.Policy{ErrorMapper: mustRuleMapper(t,
		tasks.Rule{Code: "409", Yield: tasks.BpmnError{Code: "Conflict"}})})

	require.NoError(t, d.Fail(context.Background(), "j1", "w1",
		tasks.Fault{Code: "409", Cause: errors.New("boom")}))

	require.Equal(t, tasks.OutcomeFault, sink.last().Kind())
}

// TestDispatcherFaultMapperErrorFallsBackTechnical: an ErrorMapper whose
// evaluation errors is treated as a technical fault, not a failed report (SRD-038
// §3.4).
func TestDispatcherFaultMapperErrorFallsBackTechnical(t *testing.T) {
	sink := &recordSink{}
	d := localdispatcher.New(clocktest.New(base), time.Minute)
	d.BindSink(sink)
	d.BindExpressionEngine(expengine.New())

	enqueueAndLock(t, d, &tasks.Policy{ErrorMapper: mustRuleMapper(t,
		tasks.Rule{Code: "404", BodyClause: bodyRequiringClause(t),
			Yield: tasks.Status{Value: values.NewVariable("x")}})})

	// the fault carries no body → the clause's Find errors → classify errors.
	require.NoError(t, d.Fail(context.Background(), "j1", "w1",
		tasks.Fault{Code: "404"}))

	require.Equal(t, tasks.OutcomeFault, sink.last().Kind())
}

// TestBindExpressionEngineSeam: a nil engine bind is ignored (the previously bound
// engine is kept), so classification still runs (SRD-038 §3.4).
func TestBindExpressionEngineSeam(t *testing.T) {
	sink := &recordSink{}
	d := localdispatcher.New(clocktest.New(base), time.Minute)
	d.BindSink(sink)
	d.BindExpressionEngine(expengine.New())
	d.BindExpressionEngine(nil) // ignored — the bound engine is kept

	enqueueAndLock(t, d, &tasks.Policy{ErrorMapper: mustRuleMapper(t,
		tasks.Rule{Code: "409", Yield: tasks.BpmnError{Code: "Conflict"}})})

	require.NoError(t, d.Fail(context.Background(), "j1", "w1",
		tasks.Fault{Code: "409"}))

	require.Equal(t, tasks.OutcomeBpmnError, sink.last().Kind())
}
