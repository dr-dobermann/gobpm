package thresher_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/dr-dobermann/gobpm/pkg/repository/memrepo"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// SRD-085 M1 (ADR-006 v.5 §2.9.1/§2.9.2): one signal broadcast wakes
// EVERY parallel-MI iteration waiting at the SHARED catch node — the
// hub fans out to each iteration's own track processor, and with the
// node-resident payload slot gone each delivery is captured on its
// receiving track (raced writes to the shared node made exactly this
// scenario a -race failure before). The frame-carried BIND itself is
// pinned at unit level (catchevent_internal_test, the ReceiveTask
// suite) and end-to-end by the message-path suites; a signal catch
// declares no payload output, so nothing lands in scope here.
func TestParallelMISignalPayloadPerDelivery(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	const key = "dp-mi"

	sigItem := data.MustItemDefinition(values.NewVariable(""),
		foundation.WithID("sig_item"))

	sig, err := events.NewSignal("dp-go", sigItem)
	require.NoError(t, err)

	p, err := process.New(key, foundation.WithID(key),
		data.WithProperties(
			data.MustProperty("items",
				data.MustItemDefinition(values.NewArray("a", "b"),
					foundation.WithID("items")),
				data.ReadyDataState)))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start",
		foundation.WithID(key+"-start"))
	require.NoError(t, err)

	mi, err := activities.NewMultiInstance(
		activities.WithInputCollection("items", "item"))
	require.NoError(t, err)

	body, err := activities.NewSubProcess("body",
		activities.WithLoop(mi), foundation.WithID(key+"-body"))
	require.NoError(t, err)

	sDef, err := events.NewSignalEventDefinition(sig,
		foundation.WithID(key+"-sdef"))
	require.NoError(t, err)

	bStart, err := events.NewStartEvent("b-start",
		foundation.WithID(key+"-b-start"))
	require.NoError(t, err)

	catch, err := events.NewIntermediateCatchEvent("wait", sDef,
		foundation.WithID(key+"-catch"))
	require.NoError(t, err)

	bEnd, err := events.NewEndEvent("b-end",
		foundation.WithID(key+"-b-end"))
	require.NoError(t, err)

	for _, e := range []flow.Element{bStart, catch, bEnd} {
		require.NoError(t, body.Add(e))
	}

	link(t, bStart, catch)
	link(t, catch, bEnd)

	end, err := events.NewEndEvent("end", foundation.WithID(key+"-end"))
	require.NoError(t, err)

	for _, e := range []flow.Element{start, body, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, body)
	link(t, body, end)

	th, cancel := runEngine(t, p)
	defer cancel()

	h, err := th.StartLatest(p.ID())
	require.NoError(t, err)

	// both iterations must be parked at the shared catch before the
	// broadcast, or a late registration misses it (subscribe-before-
	// publish, ADR-006 §2.4).
	time.Sleep(200 * time.Millisecond)

	firedSig, err := events.NewSignal("dp-go",
		data.MustItemDefinition(values.NewVariable("PAY-7"),
			foundation.WithID("sig_item")))
	require.NoError(t, err)

	fired, err := events.NewSignalEventDefinition(firedSig)
	require.NoError(t, err)

	require.NoError(t, th.PropagateEvent(context.Background(), fired))

	ctx, cc := context.WithTimeout(context.Background(), 3*time.Second)
	defer cc()

	st, err := h.WaitCompletion(ctx)
	require.NoError(t, err)
	require.Equal(t, thresher.StateCompleted, st,
		"ONE broadcast must wake BOTH iterations at the shared catch")
}

// iterCorrProcess builds the SRD-085 §3 worked trace: a parallel MI
// over items, whose body waits at a SHARED message catch declaring
// iteration correlation over the split item, then reports
// "<item>=<payload>" — pairing what the iteration IS with what it
// BOUND.
func iterCorrProcess(
	t *testing.T, key string, got chan<- string, withIterCorr bool,
) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New(key, foundation.WithID(key),
		data.WithProperties(
			data.MustProperty("items",
				data.MustItemDefinition(values.NewArray("a", "b"),
					foundation.WithID("items")),
				data.ReadyDataState)))
	require.NoError(t, err)

	// the declared key: the envelope side derives the payload value
	// itself.
	mp := goexpr.Must(nil, data.MustItemDefinition(values.NewVariable("")),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			d, err := ds.Find(ctx, "confirm_in")
			if err != nil {
				return nil, err
			}

			return values.NewVariable(fmt.Sprint(d.Value().Get(ctx))), nil
		})

	re, err := bpmncommon.NewCorrelationPropertyRetrievalExpression(mp,
		bpmncommon.MustMessage("confirm", data.MustItemDefinition(
			values.NewVariable(""), foundation.WithID("confirm_in"))))
	require.NoError(t, err)

	prop, err := bpmncommon.NewCorrelationProperty("iterProp", "string",
		[]bpmncommon.CorrelationPropertyRetrievalExpression{*re})
	require.NoError(t, err)

	iterKey, err := bpmncommon.NewCorrelationKey("iterKey",
		[]bpmncommon.CorrelationProperty{*prop})
	require.NoError(t, err)

	p.CorrelationSubscriptions = []*bpmncommon.CorrelationSubscription{
		{Key: iterKey},
	}

	start, err := events.NewStartEvent("start",
		foundation.WithID(key+"-start"))
	require.NoError(t, err)

	mi, err := activities.NewMultiInstance(
		activities.WithInputCollection("items", "item"))
	require.NoError(t, err)

	body, err := activities.NewSubProcess("body",
		activities.WithLoop(mi), foundation.WithID(key+"-body"))
	require.NoError(t, err)

	bStart, err := events.NewStartEvent("b-start",
		foundation.WithID(key+"-b-start"))
	require.NoError(t, err)

	// the subscription side: this iteration's split item IS its value.
	iterExpr := goexpr.Must(nil,
		data.MustItemDefinition(values.NewVariable("")),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			d, err := ds.Find(ctx, "item")
			if err != nil {
				return nil, err
			}

			return values.NewVariable(fmt.Sprint(d.Value().Get(ctx))), nil
		})

	catchOpts := []options.Option{foundation.WithID(key + "-catch")}
	if withIterCorr {
		catchOpts = append(catchOpts,
			events.WithIterationCorrelation("iterKey", iterExpr))
	}

	catch, err := events.NewIntermediateCatchEvent("confirm-wait",
		events.MustMessageEventDefinition(
			bpmncommon.MustMessage("confirm", data.MustItemDefinition(
				values.NewVariable(""), foundation.WithID("confirm_in"))),
			nil),
		catchOpts...)
	require.NoError(t, err)

	reportOp, err := gooper.New(key+"-report",
		func(ctx context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			pay, err := r.GetDataByID("confirm_in")
			if err != nil {
				return nil, fmt.Errorf("read confirm_in: %w", err)
			}

			item, err := r.GetData("item")
			if err != nil {
				return nil, fmt.Errorf("read item: %w", err)
			}

			got <- fmt.Sprintf("%v=%v",
				item.Value().Get(ctx), pay.Value().Get(ctx))

			return nil, nil
		})
	require.NoError(t, err)

	report, err := activities.NewServiceTask(key+"-report", reportOp,
		activities.WithoutParams(), foundation.WithID(key+"-report"))
	require.NoError(t, err)

	bEnd, err := events.NewEndEvent("b-end",
		foundation.WithID(key+"-b-end"))
	require.NoError(t, err)

	for _, e := range []flow.Element{bStart, catch, report, bEnd} {
		require.NoError(t, body.Add(e))
	}

	link(t, bStart, catch)
	link(t, catch, report)
	link(t, report, bEnd)

	end, err := events.NewEndEvent("end", foundation.WithID(key+"-end"))
	require.NoError(t, err)

	for _, e := range []flow.Element{start, body, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, body)
	link(t, body, end)

	return p
}

// TestIterationCorrelatedRouting is SRD-085 T-2 — the §3 worked trace:
// two iterations wait at ONE message catch; out-of-order envelopes
// serve exactly the matching iterations, each binding ITS OWN payload.
func TestIterationCorrelatedRouting(t *testing.T) {
	broker := membroker.New()
	got := make(chan string, 2)

	p := iterCorrProcess(t, "dr-mi", got, true)

	th, _, cancel := msgEngine(t, "engine-IC", memrepo.New(), broker, p)
	defer cancel()

	h, err := th.StartLatest(p.ID())
	require.NoError(t, err)

	time.Sleep(200 * time.Millisecond) // both iterations parked

	ctx := context.Background()

	// OUT OF ORDER: the "b" envelope first — without the correlation
	// match it would land on whichever subscription was indexed first.
	require.NoError(t, broker.Publish(ctx, messaging.Envelope{
		Name: "confirm", Payload: "b", CorrelationKey: "b"}))

	select {
	case pair := <-got:
		require.Equal(t, "b=b", pair,
			"the b envelope must serve the b iteration")
	case <-time.After(3 * time.Second):
		t.Fatal("the b envelope reached no iteration")
	}

	require.NoError(t, broker.Publish(ctx, messaging.Envelope{
		Name: "confirm", Payload: "a", CorrelationKey: "a"}))

	select {
	case pair := <-got:
		require.Equal(t, "a=a", pair,
			"the a envelope must serve the a iteration")
	case <-time.After(3 * time.Second):
		t.Fatal("the a envelope reached no iteration")
	}

	wctx, cc := context.WithTimeout(ctx, 3*time.Second)
	defer cc()

	st, err := h.WaitCompletion(wctx)
	require.NoError(t, err)
	require.Equal(t, thresher.StateCompleted, st)
}

// TestKeylessConcurrentWaitersRefused is SRD-085 T-3: the SECOND
// keyless subscription for one message definition faults the instance
// loud — ambiguous delivery is a modeling error, never an arbitrary
// pick (ADR-006 v.5 §2.9.3).
func TestKeylessConcurrentWaitersRefused(t *testing.T) {
	broker := membroker.New()
	got := make(chan string, 2)

	p := iterCorrProcess(t, "dr-nk", got, false)

	th, _, cancel := msgEngine(t, "engine-NK", memrepo.New(), broker, p)
	defer cancel()

	h, err := th.StartLatest(p.ID())
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		// the refusal faults the instance and the fault lands terminal
		// (fail + stopAll → Terminated), never Completed.
		return h.State() == thresher.StateTerminated
	}, 3*time.Second, 5*time.Millisecond,
		"two keyless waiters on one definition must fault the instance")
}
