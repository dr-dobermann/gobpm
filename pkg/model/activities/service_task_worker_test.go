package activities_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockrenv"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	exprengine "github.com/dr-dobermann/gobpm/pkg/model/expression/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/dr-dobermann/gobpm/pkg/tasks"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// workerTask builds a WithWorker ServiceTask over a no-message operation.
func workerTask(t *testing.T) *activities.ServiceTask {
	t.Helper()
	require.NoError(t, data.CreateDefaultStates())

	st, err := activities.NewServiceTask("svc",
		service.MustOperation("op", nil, nil, nil),
		activities.WithWorker("topic-x"), activities.WithoutParams())
	require.NoError(t, err)

	return st
}

// TestServiceTaskWorkerExecBindsCompletedOutput: on resume, ProcessEvent stashes
// a Complete outcome and Exec binds its output to the frame (execWorkerOutcome).
func TestServiceTaskWorkerExecBindsCompletedOutput(t *testing.T) {
	st := workerTask(t)

	output := data.MustItemDefinition(values.NewVariable("res"),
		foundation.WithID("res"))
	require.NoError(t, st.ProcessEvent(context.Background(),
		tasks.NewWorkerComplete("job-1", output)))

	var put data.Data

	re := mockrenv.NewMockRuntimeEnvironment(t)
	re.EXPECT().Put(mock.Anything).RunAndReturn(func(dd ...data.Data) error {
		require.Len(t, dd, 1)
		put = dd[0]

		return nil
	})

	flows, err := st.Exec(context.Background(), re)
	require.NoError(t, err)
	require.Empty(t, flows)
	require.Equal(t, "res", put.ItemDefinition().ID())
}

// TestServiceTaskWorkerExecFaultsOnCause: a Fail outcome stashed by ProcessEvent
// makes Exec return a wrapped fault (no output committed).
func TestServiceTaskWorkerExecFaultsOnCause(t *testing.T) {
	st := workerTask(t)

	require.NoError(t, st.ProcessEvent(context.Background(),
		tasks.NewWorkerFault("job-1", tasks.Fault{Cause: errors.New("boom")})))

	re := mockrenv.NewMockRuntimeEnvironment(t) // no Put expected
	re.EXPECT().WorkerErrorMapper().Return(nil) // no engine-wide default
	_, err := st.Exec(context.Background(), re)
	require.ErrorContains(t, err, "worker reported a technical fault")
}

// TestServiceTaskWorkerExecNoOutputAdvances: a Complete outcome with no output
// commits nothing and advances (execWorkerOutcome's nil-output path).
func TestServiceTaskWorkerExecNoOutputAdvances(t *testing.T) {
	st := workerTask(t)

	require.NoError(t, st.ProcessEvent(context.Background(),
		tasks.NewWorkerComplete("job-1", nil)))

	re := mockrenv.NewMockRuntimeEnvironment(t) // no Put expected
	flows, err := st.Exec(context.Background(), re)
	require.NoError(t, err)
	require.Empty(t, flows)
}

// TestServiceTaskWorkerExecPutError: a failing commit of the worker output surfaces
// a wrapped error (execWorkerOutcome's re.Put error path).
func TestServiceTaskWorkerExecPutError(t *testing.T) {
	st := workerTask(t)

	output := data.MustItemDefinition(values.NewVariable("res"),
		foundation.WithID("res"))
	require.NoError(t, st.ProcessEvent(context.Background(),
		tasks.NewWorkerComplete("job-1", output)))

	re := mockrenv.NewMockRuntimeEnvironment(t)
	re.EXPECT().Put(mock.Anything).Return(fmt.Errorf("commit failed"))

	_, err := st.Exec(context.Background(), re)
	require.ErrorContains(t, err, "couldn't commit worker result")
}

// TestServiceTaskWorkerCloneCopiesTopic: Clone carries the worker topic, so a
// per-instance clone is still worker-dispatched.
func TestServiceTaskWorkerCloneCopiesTopic(t *testing.T) {
	st := workerTask(t)

	clone, err := st.Clone()
	require.NoError(t, err)

	cst, ok := clone.(*activities.ServiceTask)
	require.True(t, ok)

	topic, isWorker := cst.WorkerTopic()
	require.True(t, isWorker)
	require.Equal(t, tasks.Topic("topic-x"), topic)
}

// TestServiceTaskWithWorkerRejectsGoOperation covers the §2.3 build guard: a Go
// operation is an in-process closure with no shippable message boundary, so
// combining it with WithWorker is a construction error.
func TestServiceTaskWithWorkerRejectsGoOperation(t *testing.T) {
	goOp, err := gooper.New("go",
		func(
			context.Context, service.DataReader, *data.ItemDefinition,
		) (*data.ItemDefinition, error) {
			return nil, nil
		})
	require.NoError(t, err)

	_, err = activities.NewServiceTask("svc", goOp,
		activities.WithWorker("topic-x"))
	require.Error(t, err)
	require.ErrorContains(t, err, "WithWorker requires a message-operation")
}

// TestServiceTaskWithWorkerAcceptsMessageOperation: a message operation with
// WithWorker builds and reports itself worker-dispatched on its topic.
func TestServiceTaskWithWorkerAcceptsMessageOperation(t *testing.T) {
	st, err := activities.NewServiceTask("svc",
		service.MustOperation("op", nil, nil, nil),
		activities.WithWorker("topic-x"))
	require.NoError(t, err)

	topic, ok := st.WorkerTopic()
	require.True(t, ok, "a WithWorker task is worker-dispatched")
	require.Equal(t, tasks.Topic("topic-x"), topic)
}

// TestServiceTaskWithoutWorkerIsInProcess: without WithWorker the task is
// in-process — WorkerTopic reports ok == false and an empty topic.
func TestServiceTaskWithoutWorkerIsInProcess(t *testing.T) {
	st, err := activities.NewServiceTask("svc",
		service.MustOperation("op", nil, nil, nil))
	require.NoError(t, err)

	topic, ok := st.WorkerTopic()
	require.False(t, ok, "a plain ServiceTask runs in-process")
	require.Empty(t, topic)
}

// TestServiceTaskBindJobInput: BindJobInput binds the operation's input without
// executing it. With no input message it binds nothing and returns (nil, nil),
// never touching the reader.
func TestServiceTaskBindJobInput(t *testing.T) {
	st, err := activities.NewServiceTask("svc",
		service.MustOperation("op", nil, nil, nil),
		activities.WithWorker("topic-x"))
	require.NoError(t, err)

	in, err := st.BindJobInput(context.Background(),
		mockrenv.NewMockRuntimeEnvironment(t))
	require.NoError(t, err)
	require.Nil(t, in)
}

// TestServiceTaskProcessEventRejectsNonOutcome: the worker-outcome ProcessEvent
// only accepts a WorkerOutcome; any other event definition is a type error.
func TestServiceTaskProcessEventRejectsNonOutcome(t *testing.T) {
	st, err := activities.NewServiceTask("svc",
		service.MustOperation("op", nil, nil, nil),
		activities.WithWorker("topic-x"))
	require.NoError(t, err)

	sig, err := events.NewSignal("s", nil)
	require.NoError(t, err)

	def, err := events.NewSignalEventDefinition(sig)
	require.NoError(t, err)

	err = st.ProcessEvent(context.Background(), def)
	require.Error(t, err)
	require.ErrorContains(t, err, "worker-outcome")
}

// workerTaskOpts builds a WithWorker ServiceTask over a no-message operation with
// extra worker options (WithStatus / WithErrorMapper).
func workerTaskOpts(
	t *testing.T,
	opts ...activities.SrvTaskOption,
) *activities.ServiceTask {
	t.Helper()
	require.NoError(t, data.CreateDefaultStates())

	all := []options.Option{
		activities.WithWorker("topic-x"), activities.WithoutParams(),
	}
	for _, o := range opts {
		all = append(all, o)
	}

	st, err := activities.NewServiceTask("svc",
		service.MustOperation("op", nil, nil, nil), all...)
	require.NoError(t, err)

	return st
}

// bodyClauseNeedingBody builds a body-clause that reads "body" — it errors when
// the fault carries none, exercising the ErrorMapper evaluation-error path.
func bodyClauseNeedingBody(t *testing.T) data.FormalExpression {
	t.Helper()

	fe, err := goexpr.New(nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			b, err := ds.Find(ctx, "body")
			if err != nil {
				return nil, err
			}

			s, _ := b.Value().Get(ctx).(string)

			return values.NewVariable(s == "x"), nil
		})
	require.NoError(t, err)

	return fe
}

// TestServiceTaskWorkerExecRaisesBpmnError: a Business Error outcome makes Exec
// return a *events.BpmnError (caught by a boundary at the instance level).
func TestServiceTaskWorkerExecRaisesBpmnError(t *testing.T) {
	st := workerTask(t)

	require.NoError(t, st.ProcessEvent(context.Background(),
		tasks.NewWorkerBpmnError("job-1", "ResourceConflict", "already exists")))

	re := mockrenv.NewMockRuntimeEnvironment(t)
	_, err := st.Exec(context.Background(), re)

	var be *events.BpmnError
	require.True(t, errors.As(err, &be))
	require.Equal(t, "ResourceConflict", be.Code)
}

// TestServiceTaskWorkerExecWritesStatus: a Business Status outcome writes the
// WithStatus variable (no collision) and completes.
func TestServiceTaskWorkerExecWritesStatus(t *testing.T) {
	st := workerTaskOpts(t, activities.WithStatus("orderStatus", false))

	require.NoError(t, st.ProcessEvent(context.Background(),
		tasks.NewWorkerStatus("job-1", values.NewVariable("NOT_FOUND"))))

	var put data.Data

	re := mockrenv.NewMockRuntimeEnvironment(t)
	re.EXPECT().Find(mock.Anything, "orderStatus").
		Return(nil, errors.New("not found")) // no collision
	re.EXPECT().Put(mock.Anything).RunAndReturn(func(dd ...data.Data) error {
		put = dd[0]

		return nil
	})

	flows, err := st.Exec(context.Background(), re)
	require.NoError(t, err)
	require.Empty(t, flows)
	require.Equal(t, "orderStatus", put.Name())
}

// TestServiceTaskWorkerStatusOverwriteCollision: overwrite=false + a pre-existing
// variable is a runtime fault (no silent clobber).
func TestServiceTaskWorkerStatusOverwriteCollision(t *testing.T) {
	st := workerTaskOpts(t, activities.WithStatus("s", false))

	require.NoError(t, st.ProcessEvent(context.Background(),
		tasks.NewWorkerStatus("job-1", values.NewVariable("X"))))

	re := mockrenv.NewMockRuntimeEnvironment(t)
	re.EXPECT().Find(mock.Anything, "s").Return(
		data.MustParameter("s", data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable("old")),
			data.ReadyDataState)), nil) // exists → collision

	_, err := st.Exec(context.Background(), re)
	require.ErrorContains(t, err, "already exists")
}

// TestServiceTaskWorkerStatusOverwriteTrue: overwrite=true upserts without the
// collision check.
func TestServiceTaskWorkerStatusOverwriteTrue(t *testing.T) {
	st := workerTaskOpts(t, activities.WithStatus("s", true))

	require.NoError(t, st.ProcessEvent(context.Background(),
		tasks.NewWorkerStatus("job-1", values.NewVariable("X"))))

	re := mockrenv.NewMockRuntimeEnvironment(t)
	re.EXPECT().Put(mock.Anything).Return(nil) // no Find (overwrite skips the guard)

	_, err := st.Exec(context.Background(), re)
	require.NoError(t, err)
}

// TestServiceTaskWorkerStatusWithoutWithStatusFaults: a worker Status report on a
// task with no WithStatus is a runtime fault.
func TestServiceTaskWorkerStatusWithoutWithStatusFaults(t *testing.T) {
	st := workerTask(t) // no WithStatus

	require.NoError(t, st.ProcessEvent(context.Background(),
		tasks.NewWorkerStatus("job-1", values.NewVariable("X"))))

	re := mockrenv.NewMockRuntimeEnvironment(t)
	_, err := st.Exec(context.Background(), re)
	require.ErrorContains(t, err, "needs WithStatus")
}

// TestServiceTaskWorkerFaultClassifiedByMapper: a raw fault is run through the
// ErrorMapper, which yields a Business Error here.
func TestServiceTaskWorkerFaultClassifiedByMapper(t *testing.T) {
	mapper, err := tasks.NewRuleMapper(
		tasks.Rule{Code: "409", Yield: tasks.BpmnError{Code: "Conflict"}})
	require.NoError(t, err)

	st := workerTaskOpts(t, activities.WithErrorMapper(mapper))

	require.NoError(t, st.ProcessEvent(context.Background(),
		tasks.NewWorkerFault("job-1", tasks.Fault{Code: "409"})))

	re := mockrenv.NewMockRuntimeEnvironment(t)
	re.EXPECT().ExpressionEngine().Return(exprengine.New())

	_, err = st.Exec(context.Background(), re)

	var be *events.BpmnError
	require.True(t, errors.As(err, &be))
	require.Equal(t, "Conflict", be.Code)
}

// TestServiceTaskWorkerFaultMapperError: an ErrorMapper whose evaluation errors
// surfaces from Exec.
func TestServiceTaskWorkerFaultMapperError(t *testing.T) {
	mapper, err := tasks.NewRuleMapper(
		tasks.Rule{Code: "404", BodyClause: bodyClauseNeedingBody(t),
			Yield: tasks.Status{Value: values.NewVariable("x")}})
	require.NoError(t, err)

	st := workerTaskOpts(t, activities.WithErrorMapper(mapper))

	// Fault has no body → the clause's Find("body") errors → classify errors.
	require.NoError(t, st.ProcessEvent(context.Background(),
		tasks.NewWorkerFault("job-1", tasks.Fault{Code: "404"})))

	re := mockrenv.NewMockRuntimeEnvironment(t)
	re.EXPECT().ExpressionEngine().Return(exprengine.New())

	_, err = st.Exec(context.Background(), re)
	require.ErrorContains(t, err, "error-mapping failed")
}

// TestWithErrorMapperRejectsNil: a nil ErrorMapper is rejected at construction.
func TestWithErrorMapperRejectsNil(t *testing.T) {
	_, err := activities.NewServiceTask("svc",
		service.MustOperation("op", nil, nil, nil),
		activities.WithWorker("t"), activities.WithErrorMapper(nil))
	require.Error(t, err)
}

// TestWithStatusRejectsEmpty: an empty status variable name is rejected.
func TestWithStatusRejectsEmpty(t *testing.T) {
	_, err := activities.NewServiceTask("svc",
		service.MustOperation("op", nil, nil, nil),
		activities.WithWorker("t"), activities.WithStatus("  ", false))
	require.Error(t, err)
}

// TestClassificationOptionsRejectNonWorker: WithStatus/WithErrorMapper require a
// worker-dispatched ServiceTask.
func TestClassificationOptionsRejectNonWorker(t *testing.T) {
	_, err := activities.NewServiceTask("svc",
		service.MustOperation("op", nil, nil, nil),
		activities.WithStatus("s", false))
	require.ErrorContains(t, err, "require a worker-dispatched")

	mapper, merr := tasks.NewRuleMapper(
		tasks.Rule{Code: "1", Yield: tasks.Technical{}})
	require.NoError(t, merr)

	_, err = activities.NewServiceTask("svc",
		service.MustOperation("op", nil, nil, nil),
		activities.WithErrorMapper(mapper))
	require.ErrorContains(t, err, "require a worker-dispatched")
}

// TestServiceTaskWorkerBpmnErrorEmptyCodeFaults: a Business Error with an empty
// code can't be raised (NewBpmnError rejects it) — surfaced as a fault.
func TestServiceTaskWorkerBpmnErrorEmptyCodeFaults(t *testing.T) {
	st := workerTask(t)

	require.NoError(t, st.ProcessEvent(context.Background(),
		tasks.NewWorkerBpmnError("job-1", "", "no code")))

	re := mockrenv.NewMockRuntimeEnvironment(t)
	_, err := st.Exec(context.Background(), re)
	require.ErrorContains(t, err, "invalid business-error code")
}

// TestServiceTaskWorkerStatusPutError: a failing commit of the status variable
// surfaces a wrapped error.
func TestServiceTaskWorkerStatusPutError(t *testing.T) {
	st := workerTaskOpts(t, activities.WithStatus("s", true))

	require.NoError(t, st.ProcessEvent(context.Background(),
		tasks.NewWorkerStatus("job-1", values.NewVariable("X"))))

	re := mockrenv.NewMockRuntimeEnvironment(t)
	re.EXPECT().Put(mock.Anything).Return(fmt.Errorf("boom"))

	_, err := st.Exec(context.Background(), re)
	require.ErrorContains(t, err, "couldn't write status variable")
}

// TestServiceTaskWorkerFaultMappedToStatus: the ErrorMapper yields a Business
// Status for a raw fault, which is written to the WithStatus variable.
func TestServiceTaskWorkerFaultMappedToStatus(t *testing.T) {
	mapper, err := tasks.NewRuleMapper(
		tasks.Rule{Code: "404",
			Yield: tasks.Status{Value: values.NewVariable("NOT_FOUND")}})
	require.NoError(t, err)

	st := workerTaskOpts(t,
		activities.WithErrorMapper(mapper), activities.WithStatus("s", false))

	require.NoError(t, st.ProcessEvent(context.Background(),
		tasks.NewWorkerFault("job-1", tasks.Fault{Code: "404"})))

	re := mockrenv.NewMockRuntimeEnvironment(t)
	re.EXPECT().ExpressionEngine().Return(exprengine.New())
	re.EXPECT().Find(mock.Anything, "s").Return(nil, errors.New("nf"))
	re.EXPECT().Put(mock.Anything).Return(nil)

	flows, err := st.Exec(context.Background(), re)
	require.NoError(t, err)
	require.Empty(t, flows)
}

// bodyValueExpr builds a FormalExpression that returns the body's value — the
// shape a WithOutputMapping rule's Path takes.
func bodyValueExpr(t *testing.T) data.FormalExpression {
	t.Helper()

	fe, err := goexpr.New(nil,
		data.MustItemDefinition(values.NewVariable("")),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			b, err := ds.Find(ctx, "body")
			if err != nil {
				return nil, err
			}

			return values.NewVariable(b.Value().Get(ctx)), nil
		})
	require.NoError(t, err)

	return fe
}

// TestServiceTaskWorkerOutputMappingShapesBody covers FR-7: WithOutputMapping
// extracts the raw body into a declared output variable.
func TestServiceTaskWorkerOutputMappingShapesBody(t *testing.T) {
	st := workerTaskOpts(t, activities.WithOutputMapping(
		tasks.OutputRule{Path: bodyValueExpr(t), Var: "orderId"}))

	body := data.MustItemDefinition(values.NewVariable("order-42"),
		foundation.WithID("body"))
	require.NoError(t, st.ProcessEvent(context.Background(),
		tasks.NewWorkerComplete("job-1", body)))

	var put data.Data

	re := mockrenv.NewMockRuntimeEnvironment(t)
	re.EXPECT().ExpressionEngine().Return(exprengine.New())
	re.EXPECT().Put(mock.Anything).RunAndReturn(func(dd ...data.Data) error {
		put = dd[0]

		return nil
	})

	flows, err := st.Exec(context.Background(), re)
	require.NoError(t, err)
	require.Empty(t, flows)
	require.Equal(t, "orderId", put.Name())
	require.Equal(t, "order-42", put.Value().Get(context.Background()))
}

// missingDatumExpr reads a datum the fault/body source never exposes, so its
// evaluation always errors (used to force a required-path failure).
func missingDatumExpr(t *testing.T) data.FormalExpression {
	t.Helper()

	fe, err := goexpr.New(nil,
		data.MustItemDefinition(values.NewVariable("")),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			d, err := ds.Find(ctx, "nested")
			if err != nil {
				return nil, err
			}

			return values.NewVariable(d), nil
		})
	require.NoError(t, err)

	return fe
}

// TestServiceTaskWorkerOutputMappingRequiredFaults covers FR-7: a required output
// path the body doesn't satisfy faults the task.
func TestServiceTaskWorkerOutputMappingRequiredFaults(t *testing.T) {
	st := workerTaskOpts(t, activities.WithOutputMapping(
		tasks.OutputRule{Path: missingDatumExpr(t), Var: "v", Required: true}))

	// the required path reads a datum the body doesn't provide → unsatisfied.
	require.NoError(t, st.ProcessEvent(context.Background(),
		tasks.NewWorkerComplete("job-1",
			data.MustItemDefinition(values.NewVariable("x")))))

	re := mockrenv.NewMockRuntimeEnvironment(t)
	re.EXPECT().ExpressionEngine().Return(exprengine.New())

	_, err := st.Exec(context.Background(), re)
	require.ErrorContains(t, err, "output mapping failed")
}

// TestWithOutputMappingRejectsInvalidRule: a nil Path or empty Var is rejected.
func TestWithOutputMappingRejectsInvalidRule(t *testing.T) {
	_, err := activities.NewServiceTask("svc",
		service.MustOperation("op", nil, nil, nil),
		activities.WithWorker("t"),
		activities.WithOutputMapping(tasks.OutputRule{Var: "v"})) // nil Path
	require.Error(t, err)

	_, err = activities.NewServiceTask("svc",
		service.MustOperation("op", nil, nil, nil),
		activities.WithWorker("t"),
		activities.WithOutputMapping(
			tasks.OutputRule{Path: bodyValueExpr(t), Var: "  "})) // empty Var
	require.Error(t, err)
}

// TestWithOutputMappingRejectsNonWorker: WithOutputMapping requires WithWorker.
func TestWithOutputMappingRejectsNonWorker(t *testing.T) {
	_, err := activities.NewServiceTask("svc",
		service.MustOperation("op", nil, nil, nil),
		activities.WithOutputMapping(
			tasks.OutputRule{Path: bodyValueExpr(t), Var: "v"}))
	require.ErrorContains(t, err, "require a")
}

// TestWorkerClassificationBeatsMapper covers the FR-2/FR-3 precedence: an explicit
// worker classification (ReportBpmnError) is honored directly — the ErrorMapper is
// not consulted (it would map differently).
func TestWorkerClassificationBeatsMapper(t *testing.T) {
	mapper, err := tasks.NewRuleMapper(
		tasks.Rule{Yield: tasks.Status{Value: values.NewVariable("mapped")}})
	require.NoError(t, err)

	st := workerTaskOpts(t,
		activities.WithErrorMapper(mapper), activities.WithStatus("s", false))

	// the worker self-classifies a Business Error → the mapper is bypassed.
	require.NoError(t, st.ProcessEvent(context.Background(),
		tasks.NewWorkerBpmnError("job-1", "Conflict", "dup")))

	// no ExpressionEngine / Put expected — the mapper never runs.
	re := mockrenv.NewMockRuntimeEnvironment(t)
	_, err = st.Exec(context.Background(), re)

	var be *events.BpmnError
	require.True(t, errors.As(err, &be))
	require.Equal(t, "Conflict", be.Code)
}

// TestTwoLevelErrorMapperOverride covers FR-3: the per-service WithErrorMapper
// overrides the engine-wide default (the engine default is not consulted).
func TestTwoLevelErrorMapperOverride(t *testing.T) {
	perService, err := tasks.NewRuleMapper(
		tasks.Rule{Code: "409", Yield: tasks.BpmnError{Code: "PerService"}})
	require.NoError(t, err)

	st := workerTaskOpts(t, activities.WithErrorMapper(perService))

	require.NoError(t, st.ProcessEvent(context.Background(),
		tasks.NewWorkerFault("job-1", tasks.Fault{Code: "409"})))

	re := mockrenv.NewMockRuntimeEnvironment(t)
	re.EXPECT().ExpressionEngine().Return(exprengine.New())
	// WorkerErrorMapper NOT expected — per-service overrides, the fallback is skipped.

	_, err = st.Exec(context.Background(), re)

	var be *events.BpmnError
	require.True(t, errors.As(err, &be))
	require.Equal(t, "PerService", be.Code)
}

// TestEngineDefaultErrorMapperUsed covers FR-3: absent a per-service mapper, the
// engine-wide WorkerErrorMapper default classifies the raw fault.
func TestEngineDefaultErrorMapperUsed(t *testing.T) {
	engineWide, err := tasks.NewRuleMapper(
		tasks.Rule{Code: "409", Yield: tasks.BpmnError{Code: "EngineWide"}})
	require.NoError(t, err)

	st := workerTask(t) // no per-service mapper

	require.NoError(t, st.ProcessEvent(context.Background(),
		tasks.NewWorkerFault("job-1", tasks.Fault{Code: "409"})))

	re := mockrenv.NewMockRuntimeEnvironment(t)
	re.EXPECT().WorkerErrorMapper().Return(engineWide)
	re.EXPECT().ExpressionEngine().Return(exprengine.New())

	_, err = st.Exec(context.Background(), re)

	var be *events.BpmnError
	require.True(t, errors.As(err, &be))
	require.Equal(t, "EngineWide", be.Code)
}
