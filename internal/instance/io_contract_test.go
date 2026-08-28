package instance

import (
	"context"
	"sync"
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
	"github.com/dr-dobermann/gobpm/pkg/observability"
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

	t.Run("a delivered optional input keeps its optionality",
		func(t *testing.T) {
			inst, err := New(contractedSnapshot(t), scope.EmptyDataPath, rt,
				failEventProducer{}, nil,
				WithRootData([]data.Data{
					delivered(t, "subtotal", 1), delivered(t, "discount", 5)}))
			require.NoError(t, err)

			d, err := inst.sc.plane.GetData(inst.sc.root, "discount")
			require.NoError(t, err)
			require.Equal(t, 5, d.Value().Get(context.Background()))

			p, ok := d.(*data.Parameter)
			require.True(t, ok, "bound as the declared parameter")
			require.True(t, p.IsOptional(),
				"the declaration's optionality survives the binding")
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

// outputSnapshot builds start → end declaring one output, required or
// optional, that nothing in the flow ever writes.
func outputSnapshot(t *testing.T, optional bool) *snapshot.Snapshot {
	t.Helper()

	var opts []data.ParameterOption
	if optional {
		opts = append(opts, data.Optional())
	}

	p, err := process.New("resulting",
		data.WithOutputs(intInput("total", opts...)))
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

// runToEnd constructs and runs an instance to a terminal state.
func runToEnd(t *testing.T, s *snapshot.Snapshot) *Instance {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	rt := enginert.Default()

	eh, err := eventhub.New(rt)
	require.NoError(t, err)
	require.NoError(t, eh.Start(ctx))

	go func() { _ = eh.Run(ctx) }()

	inst, err := New(s, scope.EmptyDataPath, rt, eh, nil)
	require.NoError(t, err)
	require.NoError(t, inst.Run(ctx))

	require.Eventually(t,
		func() bool {
			st := inst.State()

			return st == Completed || st == Terminated
		},
		2*time.Second, 5*time.Millisecond)

	return inst
}

// TestOutputsCollected — SRD-093 T-10 at the instance level: a declared
// output the flow left Ready in the root scope is collected into the result
// at completion, and every Outputs() call hands out its own copy.
func TestOutputsCollected(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rt := enginert.Default()

	eh, err := eventhub.New(rt)
	require.NoError(t, err)
	require.NoError(t, eh.Start(ctx))

	go func() { _ = eh.Run(ctx) }()

	inst, err := New(outputSnapshot(t, false), scope.EmptyDataPath, rt, eh, nil)
	require.NoError(t, err)

	// what a task's output association would leave behind
	require.NoError(t, inst.sc.bindRootData(
		[]data.Data{delivered(t, "total", 42)}))

	require.Empty(t, inst.Outputs(), "no result before completion")
	require.NoError(t, inst.Run(ctx))

	require.Eventually(t,
		func() bool { return inst.State() == Completed },
		2*time.Second, 5*time.Millisecond)
	require.NoError(t, inst.LastErr())

	outs := inst.Outputs()
	require.Len(t, outs, 1)
	require.Equal(t, "total", outs[0].Name())
	require.Equal(t, 42, outs[0].Value().Get(ctx))

	require.NoError(t, outs[0].Value().Update(ctx, 0))
	require.Equal(t, 42, inst.Outputs()[0].Value().Get(ctx),
		"a reader's copy never reaches the instance's record")
}

// TestMissingRequiredOutputFaults — SRD-093 T-11: a required output the flow
// never produced faults the instance at completion in the terminal shape —
// Terminated, LastErr naming the output, no result; an optional one absent is
// skipped and the instance completes with an empty result.
func TestMissingRequiredOutputFaults(t *testing.T) {
	t.Run("a required output absent faults", func(t *testing.T) {
		inst := runToEnd(t, outputSnapshot(t, false))

		require.Equal(t, Terminated, inst.State())
		require.ErrorContains(t, inst.LastErr(), `required output "total"`)
		require.ErrorContains(t, inst.LastErr(), "resulting")
		require.Empty(t, inst.Outputs())
	})

	t.Run("an optional output absent is skipped", func(t *testing.T) {
		inst := runToEnd(t, outputSnapshot(t, true))

		require.Equal(t, Completed, inst.State())
		require.NoError(t, inst.LastErr())
		require.Empty(t, inst.Outputs())
	})
}

// TestEventBornLaunchWithRequiredInputRefused — SRD-093 T-9, re-tensed by
// SRD-094 FR-5: an event-born launch whose start is wired to nothing leaves
// a REQUIRED input unbound and refuses with the plain words — no capability
// to wait for any more; an optional one lets the instance run.
func TestEventBornLaunchWithRequiredInputRefused(t *testing.T) {
	t.Run("a required input nothing fills refuses the launch",
		func(t *testing.T) {
			s, start, fired := contractedMsgStart(t, intInput("subtotal"))

			// a refused launch never existed: it leaves no Created fact
			// behind (T-20)
			sink := &cpSink{}

			_, err := NewFromEvent(s, scope.EmptyDataPath,
				enginert.Default().WithReporter(sink),
				failEventProducer{}, nil, start.ID(), fired, "", "")
			require.Error(t, err)
			require.ErrorContains(t, err, `required input "subtotal"`)
			require.NotContains(t, err.Error(), "#329")
			require.False(t, sink.has(observability.PhaseCreated),
				"a refused launch announced Created")
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

// TestOutputTypeMismatchFaults — SRD-093 T-23: an output is bound through
// its declaration at completion exactly as an input is at launch, so a
// value the declared item cannot carry is the same broken promise as a
// missing one — Terminated, LastErr naming the output.
func TestOutputTypeMismatchFaults(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rt := enginert.Default()

	eh, err := eventhub.New(rt)
	require.NoError(t, err)
	require.NoError(t, eh.Start(ctx))

	go func() { _ = eh.Run(ctx) }()

	inst, err := New(outputSnapshot(t, false), scope.EmptyDataPath, rt, eh, nil)
	require.NoError(t, err)

	// a string where the declaration promised an int
	require.NoError(t, inst.sc.bindRootData(
		[]data.Data{delivered(t, "total", "forty-two")}))
	require.NoError(t, inst.Run(ctx))

	require.Eventually(t,
		func() bool { return inst.State() == Terminated },
		2*time.Second, 5*time.Millisecond)
	require.ErrorContains(t, inst.LastErr(),
		`output "total" holds a value its declaration cannot carry`)
	require.Nil(t, inst.Outputs(), "no result after a faulted completion")
}

// TestOutputsConcurrentReaders — SRD-093 T-24: Outputs() is read by hosts
// on their own goroutines while the loop stores the result; every reader
// gets its own copy, and the race detector sees nothing shared.
func TestOutputsConcurrentReaders(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rt := enginert.Default()

	eh, err := eventhub.New(rt)
	require.NoError(t, err)
	require.NoError(t, eh.Start(ctx))

	go func() { _ = eh.Run(ctx) }()

	inst, err := New(outputSnapshot(t, false), scope.EmptyDataPath, rt, eh, nil)
	require.NoError(t, err)
	require.NoError(t, inst.sc.bindRootData(
		[]data.Data{delivered(t, "total", 42)}))

	const readers = 8

	var wg sync.WaitGroup
	wg.Add(readers)

	// what each reader saw last, checked on the test goroutine
	seen := make([]any, readers)

	for i := range readers {
		go func() {
			defer wg.Done()

			for inst.State() != Completed {
				for _, d := range inst.Outputs() {
					_ = d.Value().Update(ctx, 0) // a private copy
				}
			}

			outs := inst.Outputs()
			if len(outs) == 1 {
				seen[i] = outs[0].Value().Get(ctx)
			}
		}()
	}

	require.NoError(t, inst.Run(ctx))
	wg.Wait()

	for i, v := range seen {
		require.Equal(t, 42, v, "reader %d", i)
	}

	require.Equal(t, 42, inst.Outputs()[0].Value().Get(ctx),
		"no reader's mutation reached the record")
}
