package instance

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/eventproc/eventhub"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/stretchr/testify/require"
)

// contractedMsgStart builds a message-start → end process declaring the
// given input, and returns its snapshot with the start node and a fired
// event definition — the msgStartSnapshot shape plus a contract.
func contractedMsgStart(
	t *testing.T, input *data.Parameter,
) (*snapshot.Snapshot, flow.Node, flow.EventDefinition) {
	t.Helper()

	_ = data.CreateDefaultStates()

	med := events.MustMessageEventDefinition(
		bpmncommon.MustMessage("order placed",
			data.MustItemDefinition(values.NewVariable(""),
				foundation.WithID("order_in"))),
		nil)

	p, err := process.New("born-contracted", data.WithInputs(input))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start", events.WithMessageTrigger(med))
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	require.NoError(t, p.Add(start))
	require.NoError(t, p.Add(end))
	_, err = flow.Link(start, end)
	require.NoError(t, err)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	item := med.Message().Item()
	datum := data.MustParameter(item.ID(),
		data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable("ORD-1"),
				foundation.WithID(item.ID())),
			data.ReadyDataState))

	firedDef, err := med.CloneEventDefinition([]data.Data{datum})
	require.NoError(t, err)

	return s, start, firedDef
}

func intInput(name string, opts ...data.ParameterOption) *data.Parameter {
	_ = data.CreateDefaultStates()

	return data.MustParameter(name,
		data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable(0)),
			data.ReadyDataState),
		opts...)
}

// contractedSnapshot builds start → end declaring subtotal (required, item
// id "it-subtotal") and discount (optional) as inputs.
func contractedSnapshot(t *testing.T) *snapshot.Snapshot {
	t.Helper()

	_ = data.CreateDefaultStates()

	sub := data.MustParameter("subtotal",
		data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable(0),
				foundation.WithID("it-subtotal")),
			data.ReadyDataState))

	p, err := process.New("contracted",
		data.WithInputs(sub, intInput("discount", data.Optional())))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	require.NoError(t, p.Add(start))
	require.NoError(t, p.Add(end))
	_, err = flow.Link(start, end)
	require.NoError(t, err)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	return s
}

// delivered builds a Ready datum the way a host or a caller delivers one.
func delivered(t *testing.T, name string, v any) data.Data {
	t.Helper()

	p, err := data.ReadyValueParameter(name, values.NewVariable(v))
	require.NoError(t, err)

	return p
}

// TestBindContract — SRD-093 FR-4/FR-5 at the instance boundary: the
// delivered datum is bound through the DECLARED parameter (its item id
// proves it), a required input unbound refuses, an undeclared datum refuses
// naming the declared set, and a value the declaration cannot hold refuses.
func TestBindContract(t *testing.T) {
	rt := enginert.Default()

	t.Run("bound through the declaration", func(t *testing.T) {
		inst, err := New(contractedSnapshot(t), scope.EmptyDataPath, rt,
			failEventProducer{}, nil,
			WithRootData([]data.Data{delivered(t, "subtotal", 120)}))
		require.NoError(t, err)

		d, err := inst.sc.plane.GetData(inst.sc.root, "subtotal")
		require.NoError(t, err)
		require.Equal(t, 120, d.Value().Get(context.Background()))
		require.Equal(t, "it-subtotal", d.ItemDefinition().ID())
		require.Equal(t, data.ReadyDataState.Name(), d.State().Name())

		_, err = inst.sc.plane.GetData(inst.sc.root, "discount")
		require.Error(t, err, "the optional input stays absent")
	})

	t.Run("a required input unbound refuses", func(t *testing.T) {
		_, err := New(contractedSnapshot(t), scope.EmptyDataPath, rt,
			failEventProducer{}, nil,
			WithRootData([]data.Data{delivered(t, "discount", 1)}))
		require.ErrorContains(t, err, `required input "subtotal"`)
		require.NotContains(t, err.Error(), "#329",
			"a host launch is not event-born")
	})

	t.Run("an undeclared datum refuses naming the declared set",
		func(t *testing.T) {
			_, err := New(contractedSnapshot(t), scope.EmptyDataPath, rt,
				failEventProducer{}, nil,
				WithRootData([]data.Data{
					delivered(t, "subtotal", 1), delivered(t, "subttl", 2)}))
			require.ErrorContains(t, err, `declares no input "subttl"`)
			require.ErrorContains(t, err, "subtotal, discount")
		})

	t.Run("a value the declaration cannot hold refuses", func(t *testing.T) {
		_, err := New(contractedSnapshot(t), scope.EmptyDataPath, rt,
			failEventProducer{}, nil,
			WithRootData([]data.Data{delivered(t, "subtotal", "120")}))
		require.ErrorContains(t, err, `input "subtotal" rejects the delivered value`)
	})
}

// TestEventBornLaunchWithRequiredInputRefused — SRD-093 T-9: an event-born
// launch cannot fill a process input until the attachment capability lands,
// so a REQUIRED input refuses the launch naming #329; an optional one lets
// the instance run.
func TestEventBornLaunchWithRequiredInputRefused(t *testing.T) {
	t.Run("a required input refuses the launch naming #329",
		func(t *testing.T) {
			s, start, fired := contractedMsgStart(t, intInput("subtotal"))

			_, err := NewFromEvent(s, scope.EmptyDataPath, enginert.Default(),
				failEventProducer{}, nil, start.ID(), fired, "", "")
			require.Error(t, err)
			require.ErrorContains(t, err, `required input "subtotal"`)
			require.ErrorContains(t, err, "#329")
		})

	t.Run("an optional input lets the event-born instance run",
		func(t *testing.T) {
			s, start, fired := contractedMsgStart(t,
				intInput("discount", data.Optional()))

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			rt := enginert.Default()

			eh, err := eventhub.New(rt)
			require.NoError(t, err)
			require.NoError(t, eh.Start(ctx))

			go func() { _ = eh.Run(ctx) }()

			inst, err := NewFromEvent(s, scope.EmptyDataPath, rt, eh, nil,
				start.ID(), fired, "", "")
			require.NoError(t, err)
			require.NoError(t, inst.Run(ctx))

			require.Eventually(t,
				func() bool { return inst.State() == Completed },
				2*time.Second, 5*time.Millisecond)
			require.NoError(t, inst.LastErr())

			_, err = inst.sc.plane.GetData(inst.sc.root, "discount")
			require.Error(t, err, "the optional input stays absent")
		})
}
