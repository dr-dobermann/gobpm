package snapshot_test

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/stretchr/testify/require"
)

// msgEDef builds a MessageEventDefinition named name for a start trigger.
func msgEDef(t *testing.T, name string) *events.MessageEventDefinition {
	t.Helper()

	return events.MustMessageEventDefinition(
		bpmncommon.MustMessage(name,
			data.MustItemDefinition(values.NewVariable(""),
				foundation.WithID(name+"_in"))),
		nil)
}

// sigEDef builds a SignalEventDefinition named name for a start trigger.
func sigEDef(t *testing.T, name string) *events.SignalEventDefinition {
	t.Helper()

	sig, err := events.NewSignal(name, nil)
	require.NoError(t, err)

	def, err := events.NewSignalEventDefinition(sig)
	require.NoError(t, err)

	return def
}

// timerEDef builds a date-only TimerEventDefinition — a non-instantiating start
// trigger, used to exercise the "other kinds don't instantiate" branch.
func timerEDef(t *testing.T) *events.TimerEventDefinition {
	t.Helper()

	return events.MustTimerEventDefinition(
		goexpr.Must(nil,
			data.MustItemDefinition(
				values.NewVariable(time.Now().Add(time.Second))),
			func(_ context.Context, _ data.Source) (data.Value, error) {
				return values.NewVariable(time.Now().Add(time.Second)), nil
			}),
		nil, nil)
}

// procWithStart wraps a single start node into a runnable process: start → end.
func procWithStart(t *testing.T, id string, start flow.Node) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	proc, err := process.New(id)
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	require.NoError(t, proc.Add(start))
	require.NoError(t, proc.Add(end))

	_, err = flow.Link(start.(flow.SequenceSource), end)
	require.NoError(t, err)

	return proc
}

// startNames lists the StartNode name of every discovered descriptor.
func startNames(starts []snapshot.InstantiatingStart) []string {
	names := make([]string, 0, len(starts))
	for _, s := range starts {
		names = append(names, s.StartNode.Name())
	}

	return names
}

// TestInstantiatingStarts_Kinds covers the discovery of each instantiating start
// kind (message / signal / instantiate ReceiveTask) and the exclusion of the
// non-instantiating ones (timer trigger, none start). This is the snapshot-side
// equivalent of the thresher's former scanInstantiatingStarts kind handling.
func TestInstantiatingStarts_Kinds(t *testing.T) {
	t.Run("message start is discovered", func(t *testing.T) {
		start, err := events.NewStartEvent("start",
			events.WithMessageTrigger(msgEDef(t, "order placed")))
		require.NoError(t, err)

		s, err := snapshot.New(procWithStart(t, "p-msg", start))
		require.NoError(t, err)

		require.Len(t, s.InstantiatingStarts, 1)
		require.Equal(t, "start", s.InstantiatingStarts[0].StartNode.Name())
		require.IsType(t, &events.MessageEventDefinition{},
			s.InstantiatingStarts[0].EventDef)
	})

	t.Run("signal start is discovered with no correlation key", func(t *testing.T) {
		start, err := events.NewStartEvent("start",
			events.WithSignalTrigger(sigEDef(t, "go signal")))
		require.NoError(t, err)

		s, err := snapshot.New(procWithStart(t, "p-sig", start))
		require.NoError(t, err)

		require.Len(t, s.InstantiatingStarts, 1)
		require.IsType(t, &events.SignalEventDefinition{},
			s.InstantiatingStarts[0].EventDef)
		require.Nil(t, s.InstantiatingStarts[0].CorrelationKey,
			"a signal carries no correlation key")
	})

	t.Run("instantiate ReceiveTask is discovered", func(t *testing.T) {
		require.NoError(t, data.CreateDefaultStates())

		recv, err := activities.NewReceiveTask("recv",
			bpmncommon.MustMessage("order placed",
				data.MustItemDefinition(values.NewVariable(""),
					foundation.WithID("in"))),
			activities.WithoutParams(), activities.WithInstantiate())
		require.NoError(t, err)

		s, err := snapshot.New(procWithStart(t, "p-recv", recv))
		require.NoError(t, err)

		require.Len(t, s.InstantiatingStarts, 1)
		require.Equal(t, "recv", s.InstantiatingStarts[0].StartNode.Name())
	})

	t.Run("timer start is not an instantiating trigger", func(t *testing.T) {
		start, err := events.NewStartEvent("start",
			events.WithTimerTrigger(timerEDef(t)))
		require.NoError(t, err)

		s, err := snapshot.New(procWithStart(t, "p-timer", start))
		require.NoError(t, err)

		require.Empty(t, s.InstantiatingStarts,
			"a timer start instantiates by schedule, not as a starter here")
	})

	t.Run("none start yields no descriptors", func(t *testing.T) {
		start, err := events.NewStartEvent("start")
		require.NoError(t, err)

		s, err := snapshot.New(procWithStart(t, "p-none", start))
		require.NoError(t, err)

		require.Empty(t, s.InstantiatingStarts)
	})
}

// ebArm builds a Message IntermediateCatchEvent arm named name on msgName.
func ebArm(t *testing.T, name, msgName string) *events.IntermediateCatchEvent {
	t.Helper()

	ice, err := events.NewIntermediateCatchEvent(name,
		events.MustMessageEventDefinition(
			bpmncommon.MustMessage(msgName,
				data.MustItemDefinition(values.NewVariable(""),
					foundation.WithID(name+"_in"))),
			nil))
	require.NoError(t, err)

	return ice
}

// gateProcess builds a process started by an instantiating Event-Based gateway
// with two message arms, each arm → end. parallel selects the gateway type.
func gateProcess(
	t *testing.T, id string, parallel bool,
) (*process.Process, flow.Node) {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	opts := []options.Option{gateways.WithInstantiate()}
	if parallel {
		opts = append(opts,
			gateways.WithEventGatewayType(gateways.ParallelEvents))
	}

	gate, err := gateways.NewEventBasedGateway(opts...)
	require.NoError(t, err)

	armA := ebArm(t, "armA", "order A")
	armB := ebArm(t, "armB", "order B")

	endA, err := events.NewEndEvent("endA")
	require.NoError(t, err)

	endB, err := events.NewEndEvent("endB")
	require.NoError(t, err)

	proc, err := process.New(id)
	require.NoError(t, err)

	for _, e := range []flow.Element{gate, armA, armB, endA, endB} {
		require.NoError(t, proc.Add(e))
	}

	for _, l := range [][2]flow.Element{
		{gate, armA}, {armA, endA},
		{gate, armB}, {armB, endB},
	} {
		_, err := flow.Link(
			l[0].(flow.SequenceSource), l[1].(flow.SequenceTarget))
		require.NoError(t, err)
	}

	return proc, gate
}

// TestInstantiatingStarts_EventBasedGate covers the Event-Based gateway arm
// resolution: an Exclusive-start gate yields one descriptor per arm with the ARM
// as the start node (SRD-025 §4.2); a Parallel-start gate yields one descriptor
// per arm but with the GATE as the start node (§4.3).
func TestInstantiatingStarts_EventBasedGate(t *testing.T) {
	t.Run("exclusive gate starts from each arm", func(t *testing.T) {
		proc, gate := gateProcess(t, "p-eb-excl", false)

		s, err := snapshot.New(proc)
		require.NoError(t, err)

		require.Len(t, s.InstantiatingStarts, 2)
		require.ElementsMatch(t,
			[]string{"armA", "armB"}, startNames(s.InstantiatingStarts))

		for _, is := range s.InstantiatingStarts {
			require.NotEqual(t, gate.ID(), is.StartNode.ID(),
				"exclusive start runs from the arm, not the gate")
		}
	})

	t.Run("parallel gate starts from the gate", func(t *testing.T) {
		proc, gate := gateProcess(t, "p-eb-par", true)

		s, err := snapshot.New(proc)
		require.NoError(t, err)

		require.Len(t, s.InstantiatingStarts, 2)
		for _, is := range s.InstantiatingStarts {
			require.Equal(t, gate.ID(), is.StartNode.ID(),
				"parallel start is born from the gate, not the arm")
		}
	})
}

// TestInstantiatingStarts_CloneSharesByReference verifies Clone shares the
// instantiating-starts section by reference and keeps the descriptors bound to
// the registration template (no aliasing into the per-instance node clones).
func TestInstantiatingStarts_CloneSharesByReference(t *testing.T) {
	start, err := events.NewStartEvent("start",
		events.WithMessageTrigger(msgEDef(t, "order placed")))
	require.NoError(t, err)

	s, err := snapshot.New(procWithStart(t, "p-clone", start))
	require.NoError(t, err)
	require.Len(t, s.InstantiatingStarts, 1)

	clone, err := s.Clone()
	require.NoError(t, err)
	require.Len(t, clone.InstantiatingStarts, 1)

	// the descriptor's StartNode is the same template node in both — the section
	// is shared by reference, not deep-copied.
	tmplNode := s.InstantiatingStarts[0].StartNode
	require.Same(t, tmplNode, clone.InstantiatingStarts[0].StartNode)

	// and that template node is NOT the clone's own per-instance node of the same
	// id — the descriptor stays template-bound, it does not alias into the clone.
	require.NotSame(t, tmplNode, clone.Nodes[tmplNode.ID()],
		"descriptor must not alias into the per-instance node clone")
}
