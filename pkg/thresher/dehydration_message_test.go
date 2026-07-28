package thresher_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/messaging"
	"github.com/dr-dobermann/gobpm/pkg/messaging/membroker"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/repository"
	"github.com/dr-dobermann/gobpm/pkg/repository/memrepo"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// msgWaitProcess builds a conversation handler whose middle wait is a MESSAGE
// INTERMEDIATE CATCH — the dehydratable shape (SRD-071 FR-7):
//
//	start("order placed", keyed by orderId) -> catch("payment received")
//	  -> report(pay_in) -> end
//
// The keyed message start seeds the instance's conversation key; the catch then
// waits for that conversation's follow-up. report publishes the bound payload,
// proving the woken continuation fork bound the message.
func msgWaitProcess(
	t *testing.T, key string, got chan<- string,
) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	proc, err := process.New(key, foundation.WithID(key))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start",
		events.WithMessageTrigger(events.MustMessageEventDefinition(
			bpmncommon.MustMessage("order placed", data.MustItemDefinition(
				values.NewVariable(""), foundation.WithID("order_in"))), nil)),
		events.WithCorrelationKey(orderKeyFor(t, "order placed")),
		foundation.WithID(key+"-start"))
	require.NoError(t, err)

	catch, err := events.NewIntermediateCatchEvent("await-payment",
		events.MustMessageEventDefinition(
			bpmncommon.MustMessage("payment received", data.MustItemDefinition(
				values.NewVariable(""), foundation.WithID("pay_in"))), nil),
		foundation.WithID(key+"-catch"))
	require.NoError(t, err)

	reportOp, err := gooper.New(key+"-report",
		func(ctx context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			pay, err := r.GetDataByID("pay_in")
			if err != nil {
				return nil, fmt.Errorf("read pay_in: %w", err)
			}

			got <- fmt.Sprint(pay.Value().Get(ctx))

			return nil, nil
		})
	require.NoError(t, err)

	report, err := activities.NewServiceTask(key+"-report", reportOp,
		activities.WithoutParams(), foundation.WithID(key+"-report"))
	require.NoError(t, err)

	end, err := events.NewEndEvent("end", foundation.WithID(key+"-end"))
	require.NoError(t, err)

	for _, e := range []flow.Element{start, catch, report, end} {
		require.NoError(t, proc.Add(e))
	}

	link(t, start, catch)
	link(t, catch, report)
	link(t, report, end)

	return proc
}

// msgEngine boots a checkpoint-armed engine over repo and broker.
func msgEngine(
	t *testing.T, name string, repo repository.Repository,
	broker messaging.MessageBroker, p *process.Process,
) (*thresher.Thresher, *factWatch, context.CancelFunc) {
	t.Helper()

	th, err := thresher.New(name,
		thresher.WithoutBanner(),
		thresher.WithoutStartupConfig(),
		thresher.WithRepository(repo),
		thresher.WithMessageBroker(broker),
		thresher.WithLeaseTTL(time.Minute))
	require.NoError(t, err)

	fw := &factWatch{}
	sub := th.Observe(fw)
	t.Cleanup(sub.Cancel)

	ctx, cancel := context.WithCancel(context.Background())

	_, err = th.RegisterProcess(p)
	require.NoError(t, err)
	require.NoError(t, th.Run(ctx))

	return th, fw, cancel
}

// TestDehydrationMessageWake covers SRD-071 T-5 (FR-7): an instance parked on a
// MESSAGE catch dehydrates — the engine holds its hub subscription — and a
// CORRELATED message wakes it, the continuation fork binding the payload and
// the flow continuing. A message for ANOTHER conversation does not wake it: the
// holder gates correlation before rebuilding, so a foreign key costs nothing.
func TestDehydrationMessageWake(t *testing.T) {
	repo := memrepo.New()
	broker := membroker.New()
	got := make(chan string, 2)

	p := msgWaitProcess(t, "dehy-msg", got)

	_, fw, cancel := msgEngine(t, "engine-M", repo, broker, p)
	defer cancel()

	ctx := context.Background()

	// the keyed start instantiates the handler and seeds its conversation.
	require.NoError(t, broker.Publish(ctx, messaging.Envelope{
		Name: "order placed", Payload: "ORD-1", CorrelationKey: "ORD-1"}))

	// idle on a held message wait → it releases its goroutines.
	require.Eventually(t, func() bool {
		return fw.saw(observability.KindInstanceState,
			observability.PhaseDehydrated)
	}, 3*time.Second, 10*time.Millisecond,
		"an instance parked on a message catch must dehydrate")

	// a message for a DIFFERENT conversation must not wake it.
	require.NoError(t, broker.Publish(ctx, messaging.Envelope{
		Name: "payment received", Payload: "ORD-9", CorrelationKey: "ORD-9"}))

	require.Never(t, func() bool {
		return fw.saw(observability.KindInstanceState,
			observability.PhaseHydrated)
	}, 500*time.Millisecond, 25*time.Millisecond,
		"a foreign conversation must not wake a dehydrated instance")

	select {
	case unexpected := <-got:
		t.Fatalf("a foreign message reached the flow: %q", unexpected)
	default:
	}

	// the CORRELATED follow-up wakes it, binds the payload and continues.
	require.NoError(t, broker.Publish(ctx, messaging.Envelope{
		Name: "payment received", Payload: "ORD-1", CorrelationKey: "ORD-1"}))

	select {
	case payload := <-got:
		require.Equal(t, "ORD-1", payload,
			"the continuation fork must bind the woken message's payload")
	case <-time.After(3 * time.Second):
		t.Fatal("the correlated message did not wake the dehydrated instance")
	}

	require.Eventually(t, func() bool {
		return fw.saw(observability.KindInstanceState,
			observability.PhaseHydrated)
	}, 2*time.Second, 10*time.Millisecond,
		"the wake must be observable")
}

// signalWaitProcess builds start → signal catch → lane → end: the signal half
// of FR-7 (a broadcast wait, no conversation).
func signalWaitProcess(
	t *testing.T, key string, hit *atomic.Bool,
) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	proc, err := process.New(key, foundation.WithID(key))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start",
		foundation.WithID(key+"-start"))
	require.NoError(t, err)

	catch, err := events.NewIntermediateCatchEvent("await-signal",
		sigDef(t, "go-live"), foundation.WithID(key+"-catch"))
	require.NoError(t, err)

	lane := pinnedLane(t, key+"-lane", hit)

	end, err := events.NewEndEvent("end", foundation.WithID(key+"-end"))
	require.NoError(t, err)

	for _, e := range []flow.Element{start, catch, lane, end} {
		require.NoError(t, proc.Add(e))
	}

	link(t, start, catch)
	link(t, catch, lane)
	link(t, lane, end)

	return proc
}

// TestDehydrationSignalWake covers the signal half of SRD-071 T-5/FR-7: a
// signal wait dehydrates too, and the broadcast wakes it — no correlation
// involved, the trigger simply reaches the engine-held subscription.
func TestDehydrationSignalWake(t *testing.T) {
	repo := memrepo.New()

	var hit atomic.Bool

	p := signalWaitProcess(t, "dehy-signal", &hit)

	th, fw, cancel := msgEngine(t, "engine-S2", repo, membroker.New(), p)
	defer cancel()

	h, err := th.StartLatest(p.ID())
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return fw.saw(observability.KindInstanceState,
			observability.PhaseDehydrated)
	}, 3*time.Second, 10*time.Millisecond,
		"an instance parked on a signal catch must dehydrate")

	require.NoError(t,
		th.PropagateEvent(context.Background(), sigDef(t, "go-live")))

	require.Eventually(t, hit.Load, 3*time.Second, 10*time.Millisecond,
		"the broadcast signal must wake the dehydrated instance")

	require.Eventually(t, func() bool {
		rec, ok, _ := repo.Load(context.Background(), h.ID())

		return ok && rec.Status == repository.StatusCompleted
	}, 3*time.Second, 10*time.Millisecond,
		"the woken instance must run to completion")
}
