package thresher_test

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/messaging"
	"github.com/dr-dobermann/gobpm/pkg/messaging/membroker"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	dataobjects "github.com/dr-dobermann/gobpm/pkg/model/data_objects"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

// quoteProcess builds the §3.6 path: "order placed" starts the process and
// its payload fills the declared input "order" through the start's output
// association; price computes "total"; the end's input association sources
// the declared output and the end throws "quote ready" carrying it. With
// wire false the two process ends stay unwired.
func quoteProcess(t *testing.T, wire bool) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	orderMsg := bpmncommon.MustMessage("order placed",
		data.MustItemDefinition(values.NewVariable(""), foundation.WithID("order_in")))
	quoteMsg := bpmncommon.MustMessage("quote ready",
		data.MustItemDefinition(values.NewVariable(0), foundation.WithID("quote_out")))

	p, err := process.New("quote",
		data.WithInputs(data.MustParameter("order",
			data.MustItemAwareElement(
				data.MustItemDefinition(values.NewVariable(""),
					foundation.WithID("order-item")),
				data.ReadyDataState))),
		data.WithOutputs(data.MustParameter("total",
			data.MustItemAwareElement(
				data.MustItemDefinition(values.NewVariable(0),
					foundation.WithID("total-item")),
				data.ReadyDataState))))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start",
		events.WithMessageTrigger(events.MustMessageEventDefinition(orderMsg, nil)))
	require.NoError(t, err)

	op, err := gooper.New("price",
		func(ctx context.Context, ds service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			d, err := ds.GetData("order")
			if err != nil {
				return nil, err
			}

			order, _ := d.Value().Get(ctx).(string)

			return data.MustItemDefinition(values.NewVariable(len(order)*10),
				foundation.WithID("total")), nil
		})
	require.NoError(t, err)

	price, err := activities.NewServiceTask("price", op, activities.WithoutParams())
	require.NoError(t, err)

	end, err := events.NewEndEvent("end",
		events.WithMessageTrigger(events.MustMessageEventDefinition(quoteMsg, nil)),
		// declared Unavailable and required: only the association from the
		// process output can make the throw fire
		events.WithDataInputs(data.MustParameter("quote",
			data.MustItemAwareElement(
				data.MustItemDefinition(values.NewVariable(0),
					foundation.WithID("quote_out")),
				data.UnavailableDataState))))
	require.NoError(t, err)

	for _, e := range []flow.Element{start, price, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, price)
	link(t, price, end)

	if wire {
		require.NoError(t, p.AssociateInput("order", start, start.Outputs()[0].ID()))
		require.NoError(t, p.AssociateOutput("total", end, end.Inputs()[0].ID()))
	}

	return p
}

// TestThrowBindsFromScopeWithoutAssociation pins the shape
// examples/message-intermediate-events runs — a message intermediate throw
// with NO association publishes the scope datum of its item id, as it did
// before events could carry data: the auto-declared input is a slot for an
// association, never a shadow over the scope (SRD-094 FR-2, engine note).
func TestThrowBindsFromScopeWithoutAssociation(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New("thrower",
		data.WithProperties(data.MustProperty("order_out",
			data.MustItemDefinition(values.NewVariable("ORD-42"),
				foundation.WithID("order_out")),
			data.ReadyDataState)))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	throw, err := events.NewIntermediateThrowEvent("throw",
		events.MustMessageEventDefinition(bpmncommon.MustMessage("order placed",
			data.MustItemDefinition(values.NewVariable(""),
				foundation.WithID("order_out"))), nil))
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, throw, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, throw)
	link(t, throw, end)

	broker := membroker.New()

	th, err := thresher.New("throw-scope", thresher.WithMessageBroker(broker))
	require.NoError(t, err)

	_, err = th.RegisterProcess(p)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, th.Run(ctx))

	sub, err := broker.Subscribe(ctx, "order placed")
	require.NoError(t, err)

	_, err = th.StartLatest(p.ID())
	require.NoError(t, err)

	select {
	case env := <-sub.C():
		require.Equal(t, "ORD-42", env.Payload, "the scope property, not the item's zero")
	case <-time.After(5 * time.Second):
		t.Fatal("no message arrived")
	}
}

// TestCatchAndThrowThroughDataObjects — a non-start catch and a throw on a
// running engine (the review's gap): an intermediate message catch pushes
// its payload into a data object when it fires, and an intermediate
// message throw's input is filled from a data object through the
// id-addressed AssociateTargetInput — the message it publishes carries the
// data object's value.
func TestCatchAndThrowThroughDataObjects(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New("relay")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	catch, err := events.NewIntermediateCatchEvent("catch",
		events.MustMessageEventDefinition(bpmncommon.MustMessage("order placed",
			data.MustItemDefinition(values.NewVariable(""),
				foundation.WithID("order_in"))), nil))
	require.NoError(t, err)

	throw, err := events.NewIntermediateThrowEvent("throw",
		events.MustMessageEventDefinition(bpmncommon.MustMessage("order relayed",
			data.MustItemDefinition(values.NewVariable(""),
				foundation.WithID("order_relay"))), nil))
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	held, err := dataobjects.New("held",
		data.MustItemDefinition(values.NewVariable(""), foundation.WithID("held-item")),
		data.ReadyDataState)
	require.NoError(t, err)

	for _, e := range []flow.Element{start, catch, throw, end} {
		require.NoError(t, p.Add(e))
	}

	require.NoError(t, p.Add(held))
	link(t, start, catch)
	link(t, catch, throw)
	link(t, throw, end)

	// catch → data object (by item), data object → throw's input (by id)
	require.NoError(t, held.AssociateSource(catch, []string{"order_in"}, nil))
	require.NoError(t, held.AssociateTargetInput(throw, throw.Inputs()[0].ID(), nil))

	broker := membroker.New()

	th, err := thresher.New("relay", thresher.WithMessageBroker(broker))
	require.NoError(t, err)

	_, err = th.RegisterProcess(p)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, th.Run(ctx))

	sub, err := broker.Subscribe(ctx, "order relayed")
	require.NoError(t, err)

	h, err := th.StartLatest(p.ID())
	require.NoError(t, err)

	awaitParked(t, h, 1)
	require.NoError(t, broker.Publish(ctx,
		messaging.Envelope{Name: "order placed", Payload: "ORD-77"}))

	select {
	case env := <-sub.C():
		require.Equal(t, "ORD-77", env.Payload,
			"the data object's value, filled by the catch, thrown by the throw")
	case <-time.After(5 * time.Second):
		t.Fatal("no relayed message arrived")
	}

	wctx, wc := context.WithTimeout(ctx, 5*time.Second)
	defer wc()

	state, err := h.WaitCompletion(wctx)
	require.NoError(t, err)
	require.Equal(t, thresher.StateCompleted, state)
}

// TestEventDataRoundTrip — SRD-094 T-17: on a real engine and broker, the
// message route reaches the contract the call route binds directly: the
// start's payload fills the declared input, the end's throw carries the
// declared output. Unwired, the launch is refused (the required input is
// unbound) and no quote is ever published.
func TestEventDataRoundTrip(t *testing.T) {
	run := func(t *testing.T, wire bool) (messaging.Subscription, context.Context) {
		t.Helper()

		broker := membroker.New()

		th, err := thresher.New("event-data-"+map[bool]string{true: "wired", false: "bare"}[wire],
			thresher.WithMessageBroker(broker))
		require.NoError(t, err)

		_, err = th.RegisterProcess(quoteProcess(t, wire))
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		require.NoError(t, th.Run(ctx))

		sub, err := broker.Subscribe(ctx, "quote ready")
		require.NoError(t, err)

		require.NoError(t, broker.Publish(ctx,
			messaging.Envelope{Name: "order placed", Payload: "ORD-7"}))

		return sub, ctx
	}

	t.Run("wired: the quote carries the declared output", func(t *testing.T) {
		sub, ctx := run(t, true)

		select {
		case env := <-sub.C():
			require.Equal(t, 50, env.Payload, `len("ORD-7") * 10`)
		case <-time.After(5 * time.Second):
			t.Fatal("no quote arrived")
		case <-ctx.Done():
			t.Fatal("engine stopped")
		}
	})

	t.Run("unwired: the launch is refused, nothing is quoted", func(t *testing.T) {
		sub, _ := run(t, false)

		select {
		case env := <-sub.C():
			t.Fatalf("a quote arrived from a launch that should have been refused: %v", env)
		case <-time.After(700 * time.Millisecond):
		}
	})
}
