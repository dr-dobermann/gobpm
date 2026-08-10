package instance

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
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
)

// SRD-085 M2 — the in-instance half of iteration-correlated routing,
// driven WITHOUT an engine: the fired definition is handed straight to
// Instance.ProcessEvent, so the loop's subscription index, the key
// derivation and the per-delivery binding are exercised where the
// coverage gate counts them.

// routedMIProcess builds a parallel MI whose body waits at a shared
// message catch; withIterCorr toggles the FR-3 declaration.
func routedMIProcess(
	t *testing.T, key string, got chan<- string, withIterCorr bool,
) (*snapshot.Snapshot, string) {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New(key, foundation.WithID(key),
		data.WithProperties(
			data.MustProperty("items",
				data.MustItemDefinition(values.NewArray("a", "b"),
					foundation.WithID("items")),
				data.ReadyDataState)))
	require.NoError(t, err)

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

	iterExpr := goexpr.Must(nil,
		data.MustItemDefinition(values.NewVariable("")),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			d, err := ds.Find(ctx, "item")
			if err != nil {
				return nil, err
			}

			return values.NewVariable(fmt.Sprint(d.Value().Get(ctx))), nil
		})

	med := events.MustMessageEventDefinition(
		bpmncommon.MustMessage("confirm", data.MustItemDefinition(
			values.NewVariable(""), foundation.WithID("confirm_in"))),
		nil)

	_ = med
	catchOpts := []options.Option{foundation.WithID(key + "-catch")}
	if withIterCorr {
		catchOpts = append(catchOpts,
			events.WithIterationCorrelation("iterKey", iterExpr))
	}

	catch, err := events.NewIntermediateCatchEvent("confirm-wait", med,
		catchOpts...)
	require.NoError(t, err)

	reportOp, err := gooper.New(key+"-report",
		func(ctx context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			pay, err := r.GetDataByID("confirm_in")
			if err != nil {
				return nil, err
			}

			item, err := r.GetData("item")
			if err != nil {
				return nil, err
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

	s, err := snapshot.New(p)
	require.NoError(t, err)

	return s, key + "-catch"
}

// registeredConfirmID reads the catch's REGISTERED definition id from
// the instance's own graph: every clone step (process → snapshot →
// instance) may re-mint definition ids, and the delivery must carry
// the id the tracks actually registered.
func registeredConfirmID(
	t *testing.T, inst *Instance, catchID string,
) string {
	t.Helper()

	for _, n := range inst.s.Nodes {
		inner, ok := n.(interface{ Nodes() []flow.Node })
		if !ok {
			continue
		}

		for _, in := range inner.Nodes() {
			if in.ID() != catchID {
				continue
			}

			en, isEN := in.(flow.EventNode)
			require.True(t, isEN)
			require.Len(t, en.Definitions(), 1)

			return en.Definitions()[0].ID()
		}
	}

	t.Fatalf("the catch %q wasn't found in the instance graph", catchID)

	return ""
}

// firedConfirm mints a delivery of the REGISTERED definition carrying
// the payload — what the message waiter hands the instance.
func firedConfirm(
	t *testing.T, registeredID, payload string,
) flow.EventDefinition {
	t.Helper()

	return events.MustMessageEventDefinition(
		bpmncommon.MustMessage("confirm", data.MustItemDefinition(
			values.NewVariable(payload), foundation.WithID("confirm_in"))),
		nil, foundation.WithID(registeredID))
}

// TestIterationRoutingInInstance is SRD-085 T-2's in-instance half:
// out-of-order deliveries serve exactly the matching iterations.
func TestIterationRoutingInInstance(t *testing.T) {
	got := make(chan string, 2)

	s, catchID := routedMIProcess(t, "mr-ok", got, true)

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(),
		&recordingProducer{}, nil)
	require.NoError(t, err)

	defID := registeredConfirmID(t, inst, catchID)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, inst.Run(ctx))

	time.Sleep(200 * time.Millisecond) // both iterations parked

	// OUT OF ORDER: "b" first.
	require.NoError(t, inst.ProcessEvent(ctx, firedConfirm(t, defID, "b")))

	select {
	case pair := <-got:
		require.Equal(t, "b=b", pair)
	case <-time.After(3 * time.Second):
		t.Fatal("the b delivery reached no iteration")
	}

	require.NoError(t, inst.ProcessEvent(ctx, firedConfirm(t, defID, "a")))

	select {
	case pair := <-got:
		require.Equal(t, "a=a", pair)
	case <-time.After(3 * time.Second):
		t.Fatal("the a delivery reached no iteration")
	}

	select {
	case <-inst.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("the instance did not finish")
	}

	require.Equal(t, Completed, inst.State())

	// deriveNamed's miss branches (SRD-085 FR-2): a non-message
	// definition and an undeclared key name both answer false.
	sig, err := events.NewSignal("mr-sig", nil)
	require.NoError(t, err)

	_, ok := inst.corr.deriveNamed(ctx,
		events.MustSignalEventDefinition(sig), "iterKey")
	require.False(t, ok, "a non-message definition derives nothing")

	_, ok = inst.corr.deriveNamed(ctx,
		firedConfirm(t, defID, "x"), "no-such-key")
	require.False(t, ok, "an undeclared key derives nothing")
}

// failingAdder is a parent EventProducer whose subscription-extension
// capability always fails, counting the receivers it was asked about —
// the extendNode degradation branch.
type failingAdder struct {
	*recordingProducer

	mu    sync.Mutex
	asked []string
}

func (fa *failingAdder) AddEventKey(eDefID, _ string) error {
	fa.mu.Lock()
	fa.asked = append(fa.asked, eDefID)
	fa.mu.Unlock()

	return fmt.Errorf("adder boom")
}

func (fa *failingAdder) askedNames() []string {
	fa.mu.Lock()
	defer fa.mu.Unlock()

	return append([]string(nil), fa.asked...)
}

func (fa *failingAdder) askedCount() int {
	fa.mu.Lock()
	defer fa.mu.Unlock()

	return len(fa.asked)
}

// TestExtendReceiversDescendsDespiteAdderFailure: a failing AddEventKey
// is DEGRADATION, not a fault (ADR-022 v.1 §2.3(2)) — the walk reaches
// the message receiver NESTED inside the composite body and reports no
// error to the caller.
//
// It asserts the EXACT number of receivers asked, not a positive count:
// the
// fixture holds exactly ONE message receiver, so "every receiver was
// visited" is not observable here and this test does not claim it. What
// a broken walk looks like is zero asks — a walk that never descends
// into composites, which is the defect this pins.
func TestExtendReceiversDescendsDespiteAdderFailure(t *testing.T) {
	got := make(chan string, 2)

	s, _ := routedMIProcess(t, "mr-ext", got, true)

	adder := &failingAdder{recordingProducer: &recordingProducer{}}

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(),
		adder, nil)
	require.NoError(t, err)

	inst.corr.extendReceivers("v1")

	// the catch lives INSIDE the MI body, so a walk that never descended
	// into composites asks about nothing.
	require.Len(t, adder.askedNames(), 1,
		"the ONE nested message receiver is offered the new key despite "+
			"the adder's failure")
}

// TestKeylessRoutingRefusedInInstance is SRD-085 T-3's in-instance
// half: the SECOND keyless subscription faults the instance loud.
func TestKeylessRoutingRefusedInInstance(t *testing.T) {
	got := make(chan string, 2)

	s, _ := routedMIProcess(t, "mr-nk", got, false)

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(),
		&recordingProducer{}, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, inst.Run(ctx))

	select {
	case <-inst.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("the refusal did not end the instance")
	}

	require.NotEqual(t, Completed, inst.State())
	require.ErrorContains(t, inst.LastErr(), "delivery would be ambiguous")
}

// TestInstanceProcessID pins the discovery process axis on the
// instance itself (SRD-084 FR-3).
func TestInstanceProcessID(t *testing.T) {
	got := make(chan string, 2)

	s, _ := routedMIProcess(t, "mr-pid", got, true)

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(),
		&recordingProducer{}, nil)
	require.NoError(t, err)

	require.Equal(t, s.ProcessID, inst.ProcessID())
}

// TestIterationRoutingSurvivesRestore closes SRD-085's restore gap
// (FIX-040 §1.3): T-7 publishes ONE envelope after its restore, so a
// rebuilt subscription index could be present without being right.
// Here BOTH iterations are parked when the checkpoint is taken and BOTH
// keys are delivered afterwards, so the restored instance must rebuild
// two distinct subscriptions and route each delivery to its own.
//
// It is also the regression pin for the Stringer (see stringer.go).
// Before it, this test could not exist: the capturing instance stays
// live while the restored one runs, both register message waits, and
// the mock EventProducer's argument matcher formatted the live Instance
// with %v on the registering goroutine — a reflective read of the
// correlator's maps and mutexes racing the engine's own writes. The
// test reported a data race in engine code that was innocent.
func TestIterationRoutingSurvivesRestore(t *testing.T) {
	got := make(chan string, 2)

	s, catchID := routedMIProcess(t, "mr-restore", got, true)

	doc := captureAt(t, s, func(d *checkpoint.Document) bool {
		parked := 0

		for _, tr := range d.Tracks {
			if tr.NodeID == catchID && tr.State == "TrackWaitForEvent" {
				parked++
			}
		}

		return parked >= 2
	})

	restored, err := Restore(doc, s, scope.EmptyDataPath,
		cpRuntime(t), laxEP(t), nil, nil)
	require.NoError(t, err)

	// read the registered definition id BEFORE Run: the node graph is
	// engine-owned once tracks are walking it.
	defID := registeredConfirmID(t, restored, catchID)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, restored.Run(ctx))

	require.NotEqual(t, Terminated, restored.State(),
		"two restored correlated iterations must not trip the ambiguity "+
			"guard")

	require.Eventually(t, func() bool {
		parked := 0

		for _, tk := range restored.GetTokens() {
			if tk.State == TokenWaitForEvent {
				parked++
			}
		}

		return parked >= 2
	}, 3*time.Second, 5*time.Millisecond,
		"both restored iterations must re-arm their waits")

	// out of order, as in the pre-restore half: routing must depend on
	// the correlation value, never on arrival order.
	for _, want := range []string{"b=b", "a=a"} {
		key := want[:1]

		require.NoError(t, restored.ProcessEvent(ctx,
			firedConfirm(t, defID, key)))

		select {
		case pair := <-got:
			require.Equal(t, want, pair)
		case <-time.After(3 * time.Second):
			t.Fatalf("the %q delivery reached no restored iteration", key)
		}
	}
}
