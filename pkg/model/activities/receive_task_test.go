package activities_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockrenv"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
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

	t.Run("captured payload is bound into scope on resume",
		func(t *testing.T) {
			rt, err := activities.NewReceiveTask("await", recvMessage(t),
				activities.WithoutParams())
			require.NoError(t, err)

			require.NoError(t, rt.ProcessEvent(ctx, firedDef(t, "ORD-5")))

			var put data.Data

			re := mockrenv.NewMockRuntimeEnvironment(t)
			re.EXPECT().
				Put(mock.Anything).
				RunAndReturn(func(dd ...data.Data) error {
					put = dd[0]

					return nil
				})

			flows, err := rt.Exec(ctx, re)
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

			require.NoError(t, rt.ProcessEvent(ctx, firedDef(t, "x")))

			re := mockrenv.NewMockRuntimeEnvironment(t)
			re.EXPECT().
				Put(mock.Anything).
				Return(fmt.Errorf("commit failed"))

			_, err = rt.Exec(ctx, re)
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
