package thresher_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
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
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/repository"
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
	awaitParked(t, h, 2)

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

// awaitParked polls until n tokens of the instance are waiting for an
// event. A fixed sleep here is timing-dependent in both directions: too
// short and the delivery is published before the wait is armed (and
// dropped by subscribe-before-publish), too long and the test pays for
// it on every run.
func awaitParked(t *testing.T, h *thresher.InstanceHandle, n int) {
	t.Helper()

	require.Eventually(t, func() bool {
		parked := 0

		for _, tk := range h.Tokens() {
			if tk.State == thresher.TokenWaitForEvent {
				parked++
			}
		}

		return parked >= n
	}, 5*time.Second, 5*time.Millisecond,
		"%d token(s) must be parked before the trigger is published", n)
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

	awaitParked(t, h, 2)

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

// iterCorrEngine boots a checkpoint-armed engine over the SHARED repo
// and broker in the recovery group — the T-7 pair.
func iterCorrEngine(
	t *testing.T, name string, repo repository.Repository,
	broker messaging.MessageBroker, ttl time.Duration,
	p *process.Process,
) (*thresher.Thresher, *factWatch, context.CancelFunc) {
	t.Helper()

	th, err := thresher.New(name,
		thresher.WithoutBanner(),
		thresher.WithoutStartupConfig(),
		thresher.WithRepository(repo),
		thresher.WithMessageBroker(broker),
		thresher.WithEngineGroup(recoveryGroup),
		thresher.WithLeaseTTL(ttl))
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

// cuttableBroker wraps a broker so a test can sever ONE engine's
// subscriptions — the crash shape: the zombie's process memory stays
// (its late saves are CAS-fenced) but its network is gone, so it can
// no longer steal point-to-point messages from the recovering engine.
type cuttableBroker struct {
	messaging.MessageBroker

	mu   sync.Mutex
	subs []messaging.Subscription
}

func (b *cuttableBroker) Subscribe(
	ctx context.Context, name string, keys ...string,
) (messaging.Subscription, error) {
	sub, err := b.MessageBroker.Subscribe(ctx, name, keys...)
	if err != nil {
		return nil, err
	}

	b.mu.Lock()
	b.subs = append(b.subs, sub)
	b.mu.Unlock()

	return sub, nil
}

// cut severs every subscription made through this wrapper.
func (b *cuttableBroker) cut() {
	b.mu.Lock()
	defer b.mu.Unlock()

	for _, sub := range b.subs {
		_ = sub.Unsubscribe()
	}
}

// liveIterations counts the instances a document records as still running,
// across every iterated activity in it. The position lives on the host's
// track record now (SRD-090.A FR-6), not in a group table of its own.
func liveIterations(doc *checkpoint.Document) int {
	live := 0

	for _, tr := range doc.Tracks {
		if tr.Iteration == nil {
			continue
		}

		for _, inst := range tr.Iteration.Instances {
			if inst.State == "running" {
				live++
			}
		}
	}

	return live
}

// TestIterationRoutingKillAndResume is SRD-085 T-7: the worked trace
// across a crash — one envelope served pre-kill, the engine abandoned,
// and the RECOVERED iteration's key re-derives from its restored
// scope's item, so the second envelope still routes to exactly the
// matching iteration.
func TestIterationRoutingKillAndResume(t *testing.T) {
	repo := memrepo.New()
	broker := membroker.New()

	got1 := make(chan string, 2)
	p1 := iterCorrProcess(t, "dr-kr", got1, true)

	broker1 := &cuttableBroker{MessageBroker: broker}

	th1, _, cancel1 := iterCorrEngine(t, "engine-1", repo, broker1,
		80*time.Millisecond, p1)
	defer cancel1() // teardown only; the "crash" is the cut + abandonment

	h, err := th1.StartLatest(p1.ID())
	require.NoError(t, err)

	instID := h.ID()

	awaitParked(t, h, 2)

	ctx := context.Background()

	require.NoError(t, broker.Publish(ctx, messaging.Envelope{
		Name: "confirm", Payload: "b", CorrelationKey: "b"}))

	select {
	case pair := <-got1:
		require.Equal(t, "b=b", pair)
	case <-time.After(3 * time.Second):
		t.Fatal("the pre-kill envelope reached no iteration")
	}

	// the checkpoint must hold the position: iteration b drained,
	// iteration a still open and waiting.
	require.Eventually(t, func() bool {
		rec, ok, _ := repo.Load(ctx, instID)
		if !ok {
			return false
		}

		doc, derr := checkpoint.Unmarshal(rec.Payload)
		if derr != nil {
			return false
		}

		return liveIterations(doc) == 1
	}, 3*time.Second, 500*time.Millisecond,
		"the checkpoint must freeze the one-open-iteration position")

	// the crash: sever engine-1's network and ABANDON it — a graceful
	// cancel would persist a terminal record and recovery would rightly
	// skip it; a live zombie on the broker would steal the
	// point-to-point envelope from the recovering engine. The cut keeps
	// the zombie's memory (its late saves stay CAS-fenced) while its
	// subscriptions vanish, which is what a crashed process looks like
	// from the broker's side.
	broker1.cut()
	time.Sleep(120 * time.Millisecond) // > engine-1's lease TTL

	got2 := make(chan string, 2)
	p2 := iterCorrProcess(t, "dr-kr", got2, true)

	th2, fw2, cancel2 := iterCorrEngine(t, "engine-2", repo, broker,
		time.Minute, p2)
	defer cancel2()

	require.Eventually(t, func() bool {
		return fw2.saw(observability.KindInstanceState,
			observability.PhaseRecovered)
	}, 2*time.Second, 5*time.Millisecond,
		"engine-2 must claim and recover the abandoned instance")

	// the restored iteration must re-register its wait before the
	// envelope is published — poll the recovered instance's tokens
	// rather than guessing a duration.
	require.Eventually(t, func() bool {
		rh, ok := th2.Instance(instID)
		if !ok {
			return false
		}

		// count BOTH iterations, never "any one token": the kill lands
		// before either delivery, so both re-arm on restore. Returning
		// on the FIRST parked token would publish the envelope keyed "a"
		// while only one iteration has re-armed — and a half-armed set
		// can match it into the OTHER instance. The broker does not lose
		// it (an unmatched envelope waits in its inbox for a matching
		// subscription, per the messaging conformance suite); what a
		// premature publish risks is the wrong iteration being served,
		// which is silent where a delay is not.
		parked := 0

		for _, tk := range rh.Tokens() {
			if tk.State == thresher.TokenWaitForEvent {
				parked++
			}
		}

		return parked == 2
	}, 5*time.Second, 5*time.Millisecond,
		"both restored iterations must re-arm their waits")

	require.NoError(t, broker.Publish(ctx, messaging.Envelope{
		Name: "confirm", Payload: "a", CorrelationKey: "a"}))

	select {
	case pair := <-got2:
		require.Equal(t, "a=a", pair,
			"the recovered iteration's re-derived key must route the envelope")
	case <-time.After(3 * time.Second):
		t.Fatal("the post-recovery envelope reached no iteration")
	}

	require.Eventually(t, func() bool {
		rec, ok, _ := repo.Load(ctx, instID)

		return ok && rec.Status == repository.StatusCompleted &&
			rec.Lease.Owner == "engine-2"
	}, 3*time.Second, 10*time.Millisecond,
		"the recovered instance must complete on engine-2")
}

// TestLeafReceiveTaskMIRefused: a ReceiveTask under a PARALLEL
// Multi-Instance that declares no iteration correlation is refused at
// registration — several instances waiting on one point-to-point message
// with nothing to say which envelope is whose (SRD-090.B FR-4).
//
// The subject is unchanged by SRD-090.B; only the reason narrowed. The
// refusal used to cover any activity that both iterated and waited,
// because a wait was armed on ARRIVAL and an in-place iteration never
// re-arrives. That is fixed — a SEQUENTIAL MI ReceiveTask over the same
// message now builds and consumes one envelope per pass — so what is left
// refused is the ambiguity rather than the mechanism.
//
// The test this one replaced asserted the pattern WORKED, and passed
// vacuously: the iteration tracks never armed their waits, so the tasks
// completed without consuming the envelopes it published — the published
// messages were irrelevant to its result.
func TestLeafReceiveTaskMIRefused(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	mi, err := activities.NewMultiInstance(
		activities.WithInputCollection("items", "item"))
	require.NoError(t, err)

	p, err := process.New("dr-leaf", foundation.WithID("dr-leaf"),
		data.WithProperties(
			data.MustProperty("items",
				data.MustItemDefinition(values.NewArray("a", "b"),
					foundation.WithID("items")),
				data.ReadyDataState)))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start",
		foundation.WithID("dr-leaf-start"))
	require.NoError(t, err)

	recv, err := activities.NewReceiveTask("await",
		bpmncommon.MustMessage("confirm", data.MustItemDefinition(
			values.NewVariable(""), foundation.WithID("confirm_in"))),
		activities.WithoutParams(), activities.WithLoop(mi),
		foundation.WithID("dr-leaf-await"))
	require.NoError(t, err)

	end, err := events.NewEndEvent("end", foundation.WithID("dr-leaf-end"))
	require.NoError(t, err)

	for _, e := range []flow.Element{start, recv, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, recv)
	link(t, recv, end)

	th, err := thresher.New("engine-LF", thresher.WithoutBanner(),
		thresher.WithoutStartupConfig())
	require.NoError(t, err)

	_, err = th.RegisterProcess(p)
	require.ErrorContains(t, err, "declares no iteration correlation")
	require.ErrorContains(t, err, "WithIterationCorrelation",
		"the refusal names the declaration that would make it buildable")
}
