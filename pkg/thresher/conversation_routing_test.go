package thresher_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/messaging"
	"github.com/dr-dobermann/gobpm/pkg/messaging/membroker"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

// orderKeyFor builds the process's CorrelationKey: a single property reading the
// order id from a message payload bound under item id "order_in".
func orderKeyFor(t *testing.T, msgName string) *bpmncommon.CorrelationKey {
	t.Helper()

	mp := goexpr.Must(nil, data.MustItemDefinition(values.NewVariable("")),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			d, err := ds.Find(ctx, "order_in")
			if err != nil {
				return nil, err
			}

			return values.NewVariable(fmt.Sprint(d.Value().Get(ctx))), nil
		})

	re, err := bpmncommon.NewCorrelationPropertyRetrievalExpression(mp,
		bpmncommon.MustMessage(msgName, data.MustItemDefinition(
			values.NewVariable(""), foundation.WithID("order_in"))))
	require.NoError(t, err)

	prop, err := bpmncommon.NewCorrelationProperty("orderId", "string",
		[]bpmncommon.CorrelationPropertyRetrievalExpression{*re})
	require.NoError(t, err)

	key, err := bpmncommon.NewCorrelationKey("orderKey",
		[]bpmncommon.CorrelationProperty{*prop})
	require.NoError(t, err)

	return key
}

// orderConversationProcess builds the phase-2c handler:
//
//	start("order placed", keyed by orderId) -> recv("payment received")
//	  -> report(order_in + pay_in) -> end
//
// The keyed message start instantiates one handler per order and seeds its
// conversation key. The in-instance ReceiveTask then subscribes keyed to that
// instance's conversation, so a "payment received" message routes back to the
// originating handler. report pushes "<order>/<payment>" to done.
func orderConversationProcess(t *testing.T, done chan<- string) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	proc, err := process.New("order-conversation")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start",
		events.WithMessageTrigger(events.MustMessageEventDefinition(
			bpmncommon.MustMessage("order placed", data.MustItemDefinition(
				values.NewVariable(""), foundation.WithID("order_in"))), nil)),
		events.WithCorrelationKey(orderKeyFor(t, "order placed")))
	require.NoError(t, err)

	recv, err := activities.NewReceiveTask("await-payment",
		bpmncommon.MustMessage("payment received", data.MustItemDefinition(
			values.NewVariable(""), foundation.WithID("pay_in"))),
		activities.WithoutParams())
	require.NoError(t, err)

	reportOp, err := gooper.New("report-op",
		func(ctx context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			order, err := r.GetDataByID("order_in")
			if err != nil {
				return nil, fmt.Errorf("read order_in: %w", err)
			}

			pay, err := r.GetDataByID("pay_in")
			if err != nil {
				return nil, fmt.Errorf("read pay_in: %w", err)
			}

			done <- fmt.Sprintf("%v/%v",
				order.Value().Get(ctx), pay.Value().Get(ctx))

			return nil, nil
		})
	require.NoError(t, err)

	report, err := activities.NewServiceTask("report", reportOp,
		activities.WithoutParams())
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, recv, report, end} {
		require.NoError(t, proc.Add(e))
	}

	for _, l := range [][2]flow.Element{
		{start, recv}, {recv, report}, {report, end},
	} {
		_, err := flow.Link(
			l[0].(flow.SequenceSource), l[1].(flow.SequenceTarget))
		require.NoError(t, err)
	}

	return proc
}

// TestConversationRouting is SRD-017 V5: two handler instances born by distinct
// order keys each receive their own "payment received" follow-up — routed back
// to the originating instance with no cross-talk.
func TestConversationRouting(t *testing.T) {
	broker := membroker.New()

	th, err := thresher.New("conv-routing", thresher.WithMessageBroker(broker))
	require.NoError(t, err)

	done := make(chan string, 2)
	require.NoError(t, th.RegisterProcess(orderConversationProcess(t, done)))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))

	// two orders -> two handler instances, born and keyed by order id.
	require.NoError(t, broker.Publish(ctx, messaging.Envelope{
		Name: "order placed", Payload: "ORD-1", CorrelationKey: "ORD-1"}))
	require.NoError(t, broker.Publish(ctx, messaging.Envelope{
		Name: "order placed", Payload: "ORD-2", CorrelationKey: "ORD-2"}))

	// give both handlers a moment to reach and park their payment receiver.
	time.Sleep(200 * time.Millisecond)

	// each payment routes back to its own order's handler (payload = order id).
	require.NoError(t, broker.Publish(ctx, messaging.Envelope{
		Name: "payment received", Payload: "ORD-1", CorrelationKey: "ORD-1"}))
	require.NoError(t, broker.Publish(ctx, messaging.Envelope{
		Name: "payment received", Payload: "ORD-2", CorrelationKey: "ORD-2"}))

	got := map[string]bool{}
	for range 2 {
		select {
		case r := <-done:
			got[r] = true
		case <-time.After(3 * time.Second):
			t.Fatalf("a payment did not route to its handler; got %v", got)
		}
	}

	// no cross-talk: each handler reported its own order paired with its own
	// payment ("ORD-1/ORD-1", "ORD-2/ORD-2"), never a swapped pair.
	require.True(t, got["ORD-1/ORD-1"], "ORD-1 handler routing: got %v", got)
	require.True(t, got["ORD-2/ORD-2"], "ORD-2 handler routing: got %v", got)
}
