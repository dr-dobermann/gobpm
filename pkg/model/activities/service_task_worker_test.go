package activities_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockrenv"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
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
		tasks.NewWorkerFail("job-1", errors.New("boom"))))

	re := mockrenv.NewMockRuntimeEnvironment(t) // no Put expected
	_, err := st.Exec(context.Background(), re)
	require.ErrorContains(t, err, "worker reported a failure")
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
