package thresher_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/clock/clocktest"
	"github.com/dr-dobermann/gobpm/pkg/messaging"
	"github.com/dr-dobermann/gobpm/pkg/messaging/membroker"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/repository"
	"github.com/dr-dobermann/gobpm/pkg/repository/memrepo"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// ebgRaceProcess builds the canonical Event-Based Gateway shape with one TIMER
// arm and one MESSAGE arm racing:
//
//	start -> (X) -+-> timer(deadline)  -> onTimeout -> end
//	              +-> catch("cancel")  -> onCancel  -> end
//
// Both arms are engine-holdable (FR-6/FR-7), so the gateway — Dehydratable
// unconditionally (FR-1a) — releases the instance with a holder SET.
// withCondArm swaps the message arm for a CONDITIONAL one, which no holder can
// ever stand for, so the whole gateway must stay resident.
func ebgRaceProcess(
	t *testing.T, key string, deadline time.Time,
	timedOut, canceled *atomic.Bool, withCondArm bool,
) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New(key, foundation.WithID(key))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start",
		foundation.WithID(key+"-start"))
	require.NoError(t, err)

	gate, err := gateways.NewEventBasedGateway(
		gateways.WithDirection(gateways.Diverging),
		foundation.WithID(key+"-gate"))
	require.NoError(t, err)

	timerExpr, err := goexpr.New(nil,
		data.MustItemDefinition(values.NewVariable(time.Time{})),
		func(context.Context, data.Source) (data.Value, error) {
			return values.NewVariable(deadline), nil
		})
	require.NoError(t, err)

	timerDef, err := events.NewTimerEventDefinition(timerExpr, nil, nil)
	require.NoError(t, err)

	timerArm, err := events.NewIntermediateCatchEvent("on-time", timerDef,
		foundation.WithID(key+"-timerarm"))
	require.NoError(t, err)

	otherArm := ebgOtherArm(t, key, withCondArm)

	onTimeout := pinnedLane(t, key+"-timeout", timedOut)
	onCancel := pinnedLane(t, key+"-cancel", canceled)

	endT, err := events.NewEndEvent("endT", foundation.WithID(key+"-endT"))
	require.NoError(t, err)

	endC, err := events.NewEndEvent("endC", foundation.WithID(key+"-endC"))
	require.NoError(t, err)

	for _, e := range []flow.Element{
		start, gate, timerArm, otherArm, onTimeout, onCancel, endT, endC,
	} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, gate)
	link(t, gate, timerArm)
	link(t, gate, otherArm)
	link(t, timerArm, onTimeout)
	link(t, otherArm, onCancel)
	link(t, onTimeout, endT)
	link(t, onCancel, endC)

	return p
}

// ebgOtherArm builds the gateway's second arm — a holdable MESSAGE catch, or a
// CONDITIONAL catch that no holder can stand for.
func ebgOtherArm(t *testing.T, key string, cond bool) flow.Node {
	t.Helper()

	if cond {
		val := false
		evals := 0

		def, err := events.NewConditionalEventDefinition(
			condFlagExpr(t, &val, &evals))
		require.NoError(t, err)

		arm, err := events.NewIntermediateCatchEvent("on-cond", def,
			foundation.WithID(key+"-condarm"))
		require.NoError(t, err)

		return arm
	}

	arm, err := events.NewIntermediateCatchEvent("on-cancel",
		events.MustMessageEventDefinition(
			bpmncommon.MustMessage("cancel", data.MustItemDefinition(
				values.NewVariable(""), foundation.WithID("cancel_in"))), nil),
		foundation.WithID(key+"-msgarm"))
	require.NoError(t, err)

	return arm
}

// condFlagExpr builds a bool expression the test controls.
func condFlagExpr(
	t *testing.T, val *bool, evals *int,
) data.FormalExpression {
	t.Helper()

	e, err := goexpr.New(nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(context.Context, data.Source) (data.Value, error) {
			*evals++

			return values.NewVariable(*val), nil
		})
	require.NoError(t, err)

	return e
}

// TestDehydrationEBGWakesWinningArm covers SRD-071 T-EBG (FR-1a/FR-3a): an
// Event-Based Gateway racing a timer against a message dehydrates with a
// holder SET — one per armed arm — and the message firing wakes it down THAT
// arm, the losing timer's hold withdrawn so it can never fire afterwards.
func TestDehydrationEBGWakesWinningArm(t *testing.T) {
	repo := memrepo.New()
	broker := membroker.New()

	deadline := dehydrationEpoch.Add(4 * time.Hour)

	var timedOut, canceled atomic.Bool

	p := ebgRaceProcess(t, "dehy-ebg", deadline, &timedOut, &canceled, false)

	th, fw, clk, cancel := bootDehydrationEngineWithBroker(t, "engine-EBG",
		repo, broker, p)
	defer cancel()

	h, err := th.StartLatest(p.ID())
	require.NoError(t, err)

	// both arms are holdable, so the gateway releases the instance.
	require.Eventually(t, func() bool {
		return fw.saw(observability.KindInstanceState,
			observability.PhaseDehydrated)
	}, 3*time.Second, 10*time.Millisecond,
		"an all-holdable Event-Based Gateway must dehydrate")

	// the MESSAGE arm wins the race.
	require.NoError(t, broker.Publish(context.Background(),
		messaging.Envelope{Name: "cancel", Payload: "stop"}))

	require.Eventually(t, canceled.Load, 3*time.Second, 10*time.Millisecond,
		"the winning arm's flow must run")

	require.Eventually(t, func() bool {
		rec, ok, _ := repo.Load(context.Background(), h.ID())

		return ok && rec.Status == repository.StatusCompleted
	}, 3*time.Second, 10*time.Millisecond,
		"the woken instance must finish down the winning arm")

	// exactly one wake happened — the winning arm's.
	require.Equal(t, 1, countFacts(fw, observability.KindInstanceState,
		observability.PhaseHydrated))

	// The LOSING timer arm must never take effect: pushing the clock far past
	// its deadline fires nothing and wakes nothing. (That its HOLD is actually
	// withdrawn is asserted against the engine's holder registry in
	// TestWakeWithdrawsTheTracksHolds — from out here only the consequence is
	// visible.)
	clk.Advance(8 * time.Hour)

	require.Never(t, func() bool {
		return timedOut.Load() ||
			countFacts(fw, observability.KindInstanceState,
				observability.PhaseHydrated) > 1
	}, 500*time.Millisecond, 25*time.Millisecond,
		"a losing arm's hold must be withdrawn on the wake — it must never "+
			"fire, nor wake the instance again")
}

// TestDehydrationEBGUnholdableArmStaysResident covers the per-arm holder guard
// (ADR-007 v.2 §2.4): a gateway is released only when EVERY arm has a holder.
// A conditional arm — whose trigger is the instance's own data — can never be
// held, so the whole gateway stays resident and still races normally.
func TestDehydrationEBGUnholdableArmStaysResident(t *testing.T) {
	repo := memrepo.New()

	deadline := dehydrationEpoch.Add(4 * time.Hour)

	var timedOut, canceled atomic.Bool

	p := ebgRaceProcess(t, "dehy-ebg-cond", deadline,
		&timedOut, &canceled, true)

	th, fw, clk, cancel := bootDehydrationEngineWithBroker(t, "engine-EBGC",
		repo, membroker.New(), p)
	defer cancel()

	h, err := th.StartLatest(p.ID())
	require.NoError(t, err)

	// it parks, but one arm has no possible holder — so it must NOT release.
	require.Eventually(t, func() bool {
		rec, ok, _ := repo.Load(context.Background(), h.ID())

		return ok && rec.Status == repository.StatusActive
	}, 3*time.Second, 10*time.Millisecond,
		"the gateway must reach its park checkpoint")

	require.Never(t, func() bool {
		return fw.saw(observability.KindInstanceState,
			observability.PhaseDehydrated)
	}, 500*time.Millisecond, 25*time.Millisecond,
		"an Event-Based Gateway with an unholdable arm stays resident")

	// resident, it still races: the timer arm fires normally.
	clk.Advance(5 * time.Hour)

	require.Eventually(t, timedOut.Load, 3*time.Second, 10*time.Millisecond,
		"a resident gateway must still fire its timer arm")
}

// bootDehydrationEngineWithBroker is the controlled-clock, checkpoint-armed
// engine plus a message broker — the gateway traces race a message against a
// timer, so they need both.
func bootDehydrationEngineWithBroker(
	t *testing.T, name string, repo repository.Repository,
	broker messaging.MessageBroker, p *process.Process,
) (*thresher.Thresher, *factWatch, *clocktest.Clock, context.CancelFunc) {
	t.Helper()

	clk := clocktest.New(dehydrationEpoch)

	th, err := thresher.New(name,
		thresher.WithoutBanner(),
		thresher.WithoutStartupConfig(),
		thresher.WithRepository(repo),
		thresher.WithMessageBroker(broker),
		thresher.WithClock(clk),
		thresher.WithLeaseTTL(time.Minute))
	require.NoError(t, err)

	fw := &factWatch{}
	sub := th.Observe(fw)
	t.Cleanup(sub.Cancel)

	ctx, cancel := context.WithCancel(context.Background())

	_, err = th.RegisterProcess(p)
	require.NoError(t, err)
	require.NoError(t, th.Run(ctx))

	return th, fw, clk, cancel
}
