package activities_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockrenv"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/msgflow"
	"github.com/dr-dobermann/gobpm/pkg/renv"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func recvMessage(t *testing.T) *bpmncommon.Message {
	t.Helper()

	return bpmncommon.MustMessage("order placed",
		data.MustItemDefinition(values.NewVariable(""),
			foundation.WithID("recv_item")))
}

// firedDef builds a MessageEventDefinition carrying value under the receive
// task's item id, as the MessageWaiter delivers it on fire.
func firedDef(t *testing.T, value any) *events.MessageEventDefinition {
	t.Helper()

	return events.MustMessageEventDefinition(
		bpmncommon.MustMessage("order placed",
			data.MustItemDefinition(values.NewVariable(value),
				foundation.WithID("recv_item"))),
		nil)
}

func TestNewReceiveTask(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	t.Run("happy path",
		func(t *testing.T) {
			msg := recvMessage(t)

			rt, err := activities.NewReceiveTask("await", msg,
				activities.WithoutParams())
			require.NoError(t, err)
			require.Equal(t, flow.ReceiveTask, rt.TaskType())
			require.Equal(t, rt, rt.Node())
			require.Equal(t, msg, rt.Message())
			require.Equal(t, msg, rt.ExpectedMessage())
			require.Empty(t, rt.Implementation())
			require.False(t, rt.Instantiate())
			require.Equal(t, flow.IntermediateEventClass, rt.EventClass())

			defs := rt.Definitions()
			require.Len(t, defs, 1)
			require.Equal(t, flow.TriggerMessage, defs[0].Type())
		})

	t.Run("empty name is rejected",
		func(t *testing.T) {
			_, err := activities.NewReceiveTask("  ", recvMessage(t),
				activities.WithoutParams())
			require.Error(t, err)
		})

	t.Run("nil message is rejected",
		func(t *testing.T) {
			_, err := activities.NewReceiveTask("await", nil,
				activities.WithoutParams())
			require.Error(t, err)
		})

	t.Run("an invalid task option is rejected",
		func(t *testing.T) {
			_, err := activities.NewReceiveTask("await", recvMessage(t),
				events.WithParallel())
			require.Error(t, err)
		})
}

func TestReceiveTaskClone(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	rt, err := activities.NewReceiveTask("await", recvMessage(t),
		activities.WithoutParams())
	require.NoError(t, err)

	cn, err := rt.Clone()
	require.NoError(t, err)

	cl, ok := cn.(*activities.ReceiveTask)
	require.True(t, ok)
	require.Equal(t, "order placed", cl.Message().Name())
	require.NotSame(t, rt.Message(), cl.Message())
	require.Len(t, cl.Definitions(), 1)
}

func TestReceiveTaskProcessThenExec(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()

	t.Run("the delivery's payload is bound into scope on resume",
		func(t *testing.T) {
			rt, err := activities.NewReceiveTask("await", recvMessage(t),
				activities.WithoutParams())
			require.NoError(t, err)

			// the node's notification stores nothing (SRD-085 FR-1) —
			// the payload reaches Exec through the environment, carried
			// by the RECEIVING execution.
			require.NoError(t, rt.ProcessEvent(ctx, firedDef(t, "ORD-5")))

			var put data.Data

			re := mockrenv.NewMockRuntimeEnvironment(t)
			re.EXPECT().
				Put(mock.Anything).
				RunAndReturn(func(dd ...data.Data) error {
					put = dd[0]

					return nil
				})

			env := &receivedEnv{
				RuntimeEnvironment: re,
				item:               msgflow.CaptureItem(firedDef(t, "ORD-5")),
			}

			flows, err := rt.Exec(ctx, env)
			require.NoError(t, err)
			require.Empty(t, flows)

			require.Equal(t, "recv_item", put.ItemDefinition().ID())
			require.Equal(t, "ORD-5", put.Value().Get(ctx))
		})

	t.Run("without a received message Exec is a no-op completion",
		func(t *testing.T) {
			rt, err := activities.NewReceiveTask("await", recvMessage(t),
				activities.WithoutParams())
			require.NoError(t, err)

			re := mockrenv.NewMockRuntimeEnvironment(t)

			flows, err := rt.Exec(ctx, re)
			require.NoError(t, err)
			require.Empty(t, flows)
		})

	t.Run("a payload-less event leaves nothing to bind",
		func(t *testing.T) {
			rt, err := activities.NewReceiveTask("await", recvMessage(t),
				activities.WithoutParams())
			require.NoError(t, err)

			// an event definition with no items.
			require.NoError(t, rt.ProcessEvent(ctx,
				events.MustSignalEventDefinition(&events.Signal{})))

			re := mockrenv.NewMockRuntimeEnvironment(t)

			_, err = rt.Exec(ctx, re)
			require.NoError(t, err)
		})

	t.Run("nil runtime environment is rejected",
		func(t *testing.T) {
			rt, err := activities.NewReceiveTask("await", recvMessage(t),
				activities.WithoutParams())
			require.NoError(t, err)

			_, err = rt.Exec(ctx, nil)
			require.Error(t, err)
		})

	t.Run("a scope bind failure is wrapped",
		func(t *testing.T) {
			rt, err := activities.NewReceiveTask("await", recvMessage(t),
				activities.WithoutParams())
			require.NoError(t, err)

			re := mockrenv.NewMockRuntimeEnvironment(t)
			re.EXPECT().
				Put(mock.Anything).
				Return(fmt.Errorf("commit failed"))

			env := &receivedEnv{
				RuntimeEnvironment: re,
				item:               msgflow.CaptureItem(firedDef(t, "x")),
			}

			_, err = rt.Exec(ctx, env)
			require.Error(t, err)
		})
}

// TestReceiveTaskInstantiate covers the WithInstantiate option (SRD-015 M4): it
// marks the task as instantiating, the flag survives Clone, and the default is
// false.
func TestReceiveTaskInstantiate(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	// default: not instantiating.
	plain, err := activities.NewReceiveTask("recv", recvMessage(t),
		activities.WithoutParams())
	require.NoError(t, err)
	require.False(t, plain.Instantiate())

	// WithInstantiate sets the flag.
	inst, err := activities.NewReceiveTask("recv-inst", recvMessage(t),
		activities.WithoutParams(), activities.WithInstantiate())
	require.NoError(t, err)
	require.True(t, inst.Instantiate())

	// the flag survives Clone.
	cn, err := inst.Clone()
	require.NoError(t, err)

	cl, ok := cn.(*activities.ReceiveTask)
	require.True(t, ok)
	require.True(t, cl.Instantiate())
}

// receivedEnv wraps a runtime environment with the delivery-payload
// capability the receiving execution provides (SRD-085 FR-1).
type receivedEnv struct {
	renv.RuntimeEnvironment
	item *data.ItemDefinition
}

func (e *receivedEnv) ReceivedItem() *data.ItemDefinition { return e.item }

// TestReceiveTaskIterationCorrelation pins the SRD-086 FR-4 option:
// the declared pair, the empty answer, the half-pair refusal, and
// clone survival.
func TestReceiveTaskIterationCorrelation(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	expr := goexpr.Must(nil, data.MustItemDefinition(values.NewVariable("")),
		func(_ context.Context, _ data.Source) (data.Value, error) {
			return values.NewVariable("v"), nil
		})

	rt, err := activities.NewReceiveTask("await", recvMessage(t),
		activities.WithoutParams(),
		activities.WithIterationCorrelation("k", expr))
	require.NoError(t, err)

	name, e := rt.IterationCorrelation()
	require.Equal(t, "k", name)
	require.NotNil(t, e)

	cl, err := rt.Clone()
	require.NoError(t, err)

	crt, ok := cl.(*activities.ReceiveTask)
	require.True(t, ok)

	name, e = crt.IterationCorrelation()
	require.Equal(t, "k", name)
	require.NotNil(t, e)

	plain, err := activities.NewReceiveTask("await", recvMessage(t),
		activities.WithoutParams())
	require.NoError(t, err)

	name, e = plain.IterationCorrelation()
	require.Empty(t, name)
	require.Nil(t, e)

	_, err = activities.NewReceiveTask("await", recvMessage(t),
		activities.WithoutParams(),
		activities.WithIterationCorrelation("k", nil))
	require.ErrorContains(t, err, "both the key name and the expression")
}
