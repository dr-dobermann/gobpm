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
	dataobjects "github.com/dr-dobermann/gobpm/pkg/model/data_objects"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/stretchr/testify/require"
)

// strParam declares a string parameter named name over item id, in state st.
func strParam(name, id string, st *data.SrcState, opts ...data.ParameterOption) *data.Parameter {
	_ = data.CreateDefaultStates()

	return data.MustParameter(name,
		data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable(""), foundation.WithID(id)),
			st),
		opts...)
}

// wiredMsgStart builds a message-start → end process declaring the required
// input "order", with the start's payload output wired into it when wire is
// set (SRD-094 FR-4), and returns the snapshot, the start, and a fired
// definition carrying "ORD-1".
func wiredMsgStart(
	t *testing.T, wire bool,
) (*snapshot.Snapshot, flow.Node, flow.EventDefinition) {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	med := events.MustMessageEventDefinition(
		bpmncommon.MustMessage("order placed",
			data.MustItemDefinition(values.NewVariable(""),
				foundation.WithID("order_in"))),
		nil)

	p, err := process.New("born-wired",
		data.WithInputs(strParam("order", "order-item", data.ReadyDataState)))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start", events.WithMessageTrigger(med))
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	require.NoError(t, p.Add(start))
	require.NoError(t, p.Add(end))
	_, err = flow.Link(start, end)
	require.NoError(t, err)

	if wire {
		require.NoError(t, p.AssociateInput("order", start, start.Outputs()[0].ID()))
	}

	s, err := snapshot.New(p)
	require.NoError(t, err)

	item := med.Message().Item()
	datum := data.MustParameter(item.ID(),
		data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable("ORD-1"),
				foundation.WithID(item.ID())),
			data.ReadyDataState))

	fired, err := med.CloneEventDefinition([]data.Data{datum})
	require.NoError(t, err)

	return s, start, fired
}

// TestBornStartFillsProcessInput — SRD-094 T-11: a message-born launch
// whose start's output association targets a required process input binds
// it from the payload at the seed, before the contract check; the same
// process unwired refuses with the plain words.
func TestBornStartFillsProcessInput(t *testing.T) {
	ctx := context.Background()

	t.Run("wired: the payload fills the required input", func(t *testing.T) {
		s, start, fired := wiredMsgStart(t, true)

		inst, err := NewFromEvent(s, scope.EmptyDataPath, enginert.Default(),
			failEventProducer{}, nil, start.ID(), fired, "", "")
		require.NoError(t, err)

		d, err := inst.sc.plane.GetData(inst.sc.root, "order")
		require.NoError(t, err)
		require.Equal(t, "ORD-1", d.Value().Get(ctx))
		require.Equal(t, "order-item", d.ItemDefinition().ID(),
			"bound through the declaration")
		require.Equal(t, data.ReadyDataState.Name(), d.State().Name())
	})

	t.Run("unwired: the required input is unbound", func(t *testing.T) {
		s, start, fired := wiredMsgStart(t, false)

		_, err := NewFromEvent(s, scope.EmptyDataPath, enginert.Default(),
			failEventProducer{}, nil, start.ID(), fired, "", "")
		require.ErrorContains(t, err, `required input "order" is unbound`)
		require.NotContains(t, err.Error(), "#329")
	})
}

// startToDataObject builds a message-start → end process whose start pushes
// its payload into the data object "received"; with a contract, the process
// also declares an unrelated optional input, so the seed's staging finds no
// declared target.
func startToDataObject(
	t *testing.T, contracted bool, payload any,
) (*snapshot.Snapshot, flow.Node, flow.EventDefinition) {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	med := events.MustMessageEventDefinition(
		bpmncommon.MustMessage("order placed",
			data.MustItemDefinition(values.NewVariable(""),
				foundation.WithID("order_in"))),
		nil)

	var opts []options.Option
	if contracted {
		opts = append(opts, data.WithInputs(
			strParam("note", "note-item", data.ReadyDataState, data.Optional())))
	}

	p, err := process.New("born-to-object", opts...)
	require.NoError(t, err)

	start, err := events.NewStartEvent("start", events.WithMessageTrigger(med))
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	received, err := dataobjects.New("received",
		data.MustItemDefinition(values.NewVariable(""),
			foundation.WithID("received-item")),
		nil)
	require.NoError(t, err)

	require.NoError(t, p.Add(start))
	require.NoError(t, p.Add(end))
	require.NoError(t, p.Add(received))
	_, err = flow.Link(start, end)
	require.NoError(t, err)

	require.NoError(t, received.AssociateSource(start, []string{"order_in"}, nil))

	s, err := snapshot.New(p)
	require.NoError(t, err)

	datum := data.MustParameter("order_in",
		data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable(payload),
				foundation.WithID("order_in")),
			data.ReadyDataState))

	fired, err := med.CloneEventDefinition([]data.Data{datum})
	require.NoError(t, err)

	return s, start, fired
}

// TestBornStartFillsDataObject — SRD-094 FR-5's other target: a born
// start's association into a data object updates it in place at the seed,
// with or without a contract; a payload the start's output cannot hold
// fails the seed naming the start.
func TestBornStartFillsDataObject(t *testing.T) {
	ctx := context.Background()

	for name, contracted := range map[string]bool{
		"contract-less": false, "contracted, no input targeted": true,
	} {
		t.Run(name, func(t *testing.T) {
			s, start, fired := startToDataObject(t, contracted, "ORD-7")

			inst, err := NewFromEvent(s, scope.EmptyDataPath, enginert.Default(),
				failEventProducer{}, nil, start.ID(), fired, "", "")
			require.NoError(t, err)

			d, err := inst.sc.plane.GetData(inst.sc.root, "received")
			require.NoError(t, err)
			require.Equal(t, "ORD-7", d.Value().Get(ctx))
		})
	}

	t.Run("a payload the output cannot hold fails the seed", func(t *testing.T) {
		s, start, fired := startToDataObject(t, false, 42)

		_, err := NewFromEvent(s, scope.EmptyDataPath, enginert.Default(),
			failEventProducer{}, nil, start.ID(), fired, "", "")
		require.ErrorContains(t, err, `born start "start"`)
		require.ErrorContains(t, err, "output associations couldn't run")
	})
}

// outputToEnd builds start → end where the end is a signal throw whose
// required data input is wired from the process output "total" when wire
// is set (SRD-094 FR-6).
func outputToEnd(t *testing.T, wire bool) *snapshot.Snapshot {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	sig, err := events.NewSignal("done",
		data.MustItemDefinition(values.NewVariable(""),
			foundation.WithID("total-item")))
	require.NoError(t, err)

	p, err := process.New("ending",
		data.WithOutputs(strParam("total", "total-item", data.ReadyDataState)))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	end, err := events.NewEndEvent("end",
		events.WithSignalTrigger(events.MustSignalEventDefinition(sig)),
		// declared Unavailable and required: only an association can
		// make the throw fire
		events.WithDataInputs(
			strParam("quote", "total-item", data.UnavailableDataState)))
	require.NoError(t, err)

	require.NoError(t, p.Add(start))
	require.NoError(t, p.Add(end))
	_, err = flow.Link(start, end)
	require.NoError(t, err)

	if wire {
		require.NoError(t, p.AssociateOutput("total", end, end.Inputs()[0].ID()))
	}

	s, err := snapshot.New(p)
	require.NoError(t, err)

	return s
}

// TestEndEventSourcesProcessOutput — SRD-094 T-12: the end event's input
// association fills from the process output the flow left in the root
// scope, so the throw fires and the instance completes; unwired, the
// required input stays unavailable and the end faults.
func TestEndEventSourcesProcessOutput(t *testing.T) {
	run := func(t *testing.T, wire bool) *Instance {
		t.Helper()

		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)

		rt := enginert.Default()

		eh, err := eventhub.New(rt)
		require.NoError(t, err)
		require.NoError(t, eh.Start(ctx))

		go func() { _ = eh.Run(ctx) }()

		inst, err := New(outputToEnd(t, wire), scope.EmptyDataPath, rt, eh, nil)
		require.NoError(t, err)

		// what the flow would have left behind
		require.NoError(t, inst.sc.bindRootData(
			[]data.Data{delivered(t, "total", "120")}))
		require.NoError(t, inst.Run(ctx))

		return inst
	}

	t.Run("wired: the end fires from the process output", func(t *testing.T) {
		inst := run(t, true)

		require.Eventually(t,
			func() bool { return inst.State() == Completed },
			2*time.Second, 5*time.Millisecond)
		require.NoError(t, inst.LastErr())
	})

	t.Run("unwired: the end's required input stays unavailable",
		func(t *testing.T) {
			inst := run(t, false)

			require.Eventually(t,
				func() bool {
					return inst.State() == Terminated || inst.OpenIncidents() > 0
				},
				2*time.Second, 5*time.Millisecond)
			require.NotEqual(t, Completed, inst.State())
		})
}
