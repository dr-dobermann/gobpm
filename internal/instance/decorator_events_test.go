package instance

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/eventproc"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// sigDefN builds a distinct signal definition, for a subscription set that
// must tell one definition from another.
func sigDefN(t *testing.T, name string) *events.SignalEventDefinition {
	t.Helper()

	sig, err := events.NewSignal(name, nil)
	require.NoError(t, err)

	def, err := events.NewSignalEventDefinition(sig)
	require.NoError(t, err)

	return def
}

// TestTheDecoratorsIdentityIsTheActivitys (SRD-090.B FR-1/FR-2): the hub tells
// subscribers apart by ID (`waiters/waiters.go:147`), so the decorator's
// identity decides whether a second registration JOINS the existing
// subscription or creates another.
//
// It must be stable across the activity's passes — a fresh id per pass would
// produce one subscription per pass, which is the N-subscriptions design
// ADR-006 §2.9.5 rejects — and distinct per process instance, because a
// snapshot clone preserves element ids and two instances of one process would
// otherwise unregister each other's waits.
func TestTheDecoratorsIdentityIsTheActivitys(t *testing.T) {
	a := newEventSubs("inst-1", "node-A")
	b := newEventSubs("inst-1", "node-A")
	c := newEventSubs("inst-2", "node-A")
	d := newEventSubs("inst-1", "node-B")

	require.Equal(t, a.ID(), b.ID(),
		"the same activity of the same instance is ONE subscriber, however "+
			"many passes build it")
	require.NotEqual(t, a.ID(), c.ID(),
		"two instances of one process are distinct subscribers — a clone "+
			"preserves element ids")
	require.NotEqual(t, a.ID(), d.ID(),
		"two activities of one instance are distinct subscribers")
}

// TestTheSubscriptionLivesWhileAnyInstanceAwaits (SRD-090.B FR-2): the
// decorator registers when its FIRST instance waits and unregisters when the
// LAST stops — not per pass, and not for the activity's whole execution.
func TestTheSubscriptionLivesWhileAnyInstanceAwaits(t *testing.T) {
	es := newEventSubs("inst", "node")
	def := sigDefN(t, "sig")

	require.True(t, es.awaiting(def, 0), "the first waiter registers")
	require.False(t, es.awaiting(def, 1), "the second joins, silently")
	require.False(t, es.awaiting(def, 2))

	require.False(t, es.stopped(def, 1), "a sibling still waits")
	require.False(t, es.stopped(def, 0))
	require.True(t, es.stopped(def, 2), "the last one out unregisters")

	// and the activity can wait again — a later pass re-registers rather
	// than finding a stale entry.
	require.True(t, es.awaiting(def, 3))
}

// TestOneSubscriptionPerDefinition: two definitions are two subscriptions,
// and neither's lifetime touches the other's.
func TestOneSubscriptionPerDefinition(t *testing.T) {
	es := newEventSubs("inst", "node")
	a, b := sigDefN(t, "sig-a"), sigDefN(t, "sig-b")

	require.True(t, es.awaiting(a, 0))
	require.True(t, es.awaiting(b, 0),
		"a second definition is its own subscription, not a join")

	require.True(t, es.stopped(a, 0))
	require.Equal(t, []int{0}, es.waitingOn(b.ID()),
		"unregistering one definition leaves the other's waiter parked")
}

// TestTheWaitingSetIsInOrdinalOrder (SRD-090.B FR-3): the dispatch order is
// ascending ordinal, whatever order the instances happened to park in.
//
// It is the only order available when nothing distinguishes the instances,
// and therefore the specified one: two runs of one model must not disagree
// about which instance an indistinguishable envelope reached.
func TestTheWaitingSetIsInOrdinalOrder(t *testing.T) {
	es := newEventSubs("inst", "node")
	def := sigDefN(t, "sig")

	// parked out of order, as a parallel fan-out does.
	for _, ord := range []int{4, 0, 3, 1} {
		es.awaiting(def, ord)
	}

	require.Equal(t, []int{0, 1, 3, 4}, es.waitingOn(def.ID()))

	es.stopped(def, 3)
	require.Equal(t, []int{0, 1, 4}, es.waitingOn(def.ID()),
		"a departure keeps the rest ordered")
}

// TestTheWaitingSetIsACopy: the loop reads it while the decorator may still
// be arming, so a returned slice must not alias the set's own.
func TestTheWaitingSetIsACopy(t *testing.T) {
	es := newEventSubs("inst", "node")
	def := sigDefN(t, "sig")

	es.awaiting(def, 0)
	es.awaiting(def, 1)

	got := es.waitingOn(def.ID())
	got[0] = 99

	require.Equal(t, []int{0, 1}, es.waitingOn(def.ID()),
		"a caller mutating what it read must not corrupt the set")
}

// TestAnUnknownWithdrawalIsNotAnError: an instance that faulted before it
// parked has nothing to withdraw, and says so rather than reporting that the
// last waiter left.
func TestAnUnknownWithdrawalIsNotAnError(t *testing.T) {
	es := newEventSubs("inst", "node")
	def := sigDefN(t, "sig")

	require.False(t, es.stopped(def, 0), "a definition nobody waits on")

	es.awaiting(def, 0)
	require.False(t, es.stopped(def, 7), "an ordinal that never parked")
	require.Equal(t, []int{0}, es.waitingOn(def.ID()))

	require.Nil(t, es.waitingOn("no-such-definition"))
}

// TestWhoOwnsAnActivitysWaits (SRD-090.B FR-1): the executor answers who
// subscribes, and a driver never tests the node.
//
// A leaf and a composite instance own their own wait and answer nil, leaving
// armWaiters' per-trigger rule exactly as it was — which is what NFR-1 turns
// on. Both decorators answer themselves: one subscriber per iterated
// activity, held across its passes.
func TestWhoOwnsAnActivitysWaits(t *testing.T) {
	_, _, node, host := decoratorFixture(t)
	step := &stepInfo{node: node}

	require.Nil(t, newNodeExec(host, step, 0).subscriber(),
		"a leaf execution is its own subscriber")
	require.Nil(t, newPlainScopeExec(host, step).subscriber(),
		"a composite registers nothing — its body's tracks own their waits")

	iter := newIterDecorator(host, step, nil, false)
	require.Equal(t, activitySubscriber(iter), iter.subscriber(),
		"an iterated activity subscribes as itself")

	loop := newLoopDecorator(host, step, nil, false)
	require.Equal(t, activitySubscriber(loop), loop.subscriber())
}

// TestTheExecutorIsResolvedOncePerStep (SRD-090.B FR-1): one object from
// arrival until the step ends, and a NEW one when the token moves.
//
// Both halves are load-bearing. Reusing it within a step is what lets arming
// and execution address the same subscription set — two objects would carry
// one identity, and the set would live on whichever was not dispatching.
// Rebuilding on a move is what the Ad-Hoc suites caught when the first
// version of this cache was keyed to nothing: an advancing track was handed
// the previous node's executor and re-ran it.
func TestTheExecutorIsResolvedOncePerStep(t *testing.T) {
	_, _, node, host := decoratorFixture(t)

	first := host.resolveExec(&stepInfo{node: node})
	require.Same(t, first, host.resolveExec(&stepInfo{node: node}),
		"the same step resolves to the same executor")

	other, err := activities.NewSubProcess("elsewhere")
	require.NoError(t, err)

	moved := host.resolveExec(&stepInfo{node: other})
	require.NotSame(t, first, moved,
		"a token that moved gets its NEW node's executor, never the old one")
}

// TestTheHoldOutlivesEveryInstanceButTheLast (SRD-090.B FR-2, M4): the engine
// hold belongs to the ACTIVITY, so one instance finishing must not withdraw
// what its siblings are waiting on.
//
// A hold is keyed (instanceID, trackID) and `ReleaseWaits` withdraws EVERY
// hold taken for a track — there is no room in that key for an ordinal. With
// several instances of one activity waiting against one host track, releasing
// on the first delivery would leave the rest waiting with nothing able to
// wake a released instance: the sibling-teardown failure ADR-006 §2.9.5
// names, one layer below the hub, where the subscription bookkeeping does not
// reach.
//
// anyWaiting is what the release consults, so this pins the predicate rather
// than the plumbing that reads it.
func TestTheHoldOutlivesEveryInstanceButTheLast(t *testing.T) {
	es := newEventSubs("inst", "node")
	def := sigDefN(t, "sig")

	require.False(t, es.anyWaiting(),
		"an activity that has not parked holds nothing")

	for _, ord := range []int{0, 1, 2} {
		es.awaiting(def, ord)
	}

	require.True(t, es.anyWaiting())

	es.stopped(def, 0)
	require.True(t, es.anyWaiting(),
		"instance 0 delivered — its siblings still need the hold")

	es.stopped(def, 1)
	require.True(t, es.anyWaiting())

	es.stopped(def, 2)
	require.False(t, es.anyWaiting(),
		"the last instance out releases the activity's hold")
}

// TestTheHoldSpansDefinitions: an activity waiting on two definitions holds
// until BOTH are done, because ReleaseWaits does not name one.
func TestTheHoldSpansDefinitions(t *testing.T) {
	es := newEventSubs("inst", "node")
	a, b := sigDefN(t, "sig-a"), sigDefN(t, "sig-b")

	es.awaiting(a, 0)
	es.awaiting(b, 0)

	require.True(t, es.stopped(a, 0), "a's own subscription is done")
	require.True(t, es.anyWaiting(),
		"but the activity still waits on b, and one ReleaseWaits would take "+
			"both")

	require.True(t, es.stopped(b, 0))
	require.False(t, es.anyWaiting())
}

// TestAnInstanceTakesDeliveryOnItsOwnBox (SRD-090.B M5b): a waiting instance
// has its own delivery channel, opened when it parks and closed when it stops.
//
// Per instance rather than per track, for the reason M3b recorded for the
// composite drain: one shared channel can neither say which instance an
// envelope was meant for nor reach more than one of them.
func TestAnInstanceTakesDeliveryOnItsOwnBox(t *testing.T) {
	es := newEventSubs("inst", "node")
	def := sigDefN(t, "sig")

	require.Nil(t, es.boxFor(0), "an instance that has not parked has no box")

	es.awaiting(def, 0)
	es.awaiting(def, 1)

	box0, box1 := es.boxFor(0), es.boxFor(1)
	require.NotNil(t, box0)
	require.NotNil(t, box1)
	require.NotEqual(t, box0, box1, "one box per instance, never shared")

	require.True(t, es.deliverTo(1, def))
	require.Same(t, def, <-box1)
	require.Empty(t, box0, "instance 0's box is untouched by 1's delivery")

	es.stopped(def, 1)
	require.Nil(t, es.boxFor(1), "the box closes with the wait")
	require.False(t, es.deliverTo(1, def),
		"a delivery to an instance that stopped waiting is dropped, not an "+
			"error — a losing arm, or a sibling that completed in flight")
}

// TestABoxSurvivesWhileTheInstanceWaitsOnAnyDefinition: an instance parked on
// two definitions keeps its box until it has stopped waiting on both.
func TestABoxSurvivesWhileTheInstanceWaitsOnAnyDefinition(t *testing.T) {
	es := newEventSubs("inst", "node")
	a, b := sigDefN(t, "sig-a"), sigDefN(t, "sig-b")

	es.awaiting(a, 0)
	es.awaiting(b, 0)

	es.stopped(a, 0)
	require.NotNil(t, es.boxFor(0),
		"still parked on b — an Event-Based Gateway races its arms this way")

	es.stopped(b, 0)
	require.Nil(t, es.boxFor(0))
}

// TestAFullBoxDropsRatherThanStalls: the LOOP is the sender, so a box that
// cannot take another occurrence must never block the single writer.
func TestAFullBoxDropsRatherThanStalls(t *testing.T) {
	es := newEventSubs("inst", "node")
	def := sigDefN(t, "sig")

	es.awaiting(def, 0)

	delivered := 0
	for range eventBufferDepth + 2 {
		if es.deliverTo(0, def) {
			delivered++
		}
	}

	require.Equal(t, eventBufferDepth, delivered,
		"the box takes what it can hold and the rest drop — the loop never "+
			"stalls on a runner that is not reading")
}

// TestTheDecoratorsDoorbellEmitsToTheLoop (ADR-006 §2.9.5): ProcessEvent runs
// on the HUB's goroutine and delivers nothing itself — it hands the
// occurrence to the loop, which owns routing. Same contract
// track.ProcessEvent already has, which is the point: the decorator
// substitutes into a chain that exists.
func TestTheDecoratorsDoorbellEmitsToTheLoop(t *testing.T) {
	_, _, node, host := decoratorFixture(t)
	step := &stepInfo{node: node}
	def := sigDefN(t, "sig")

	for name, proc := range map[string]eventproc.EventProcessor{
		"multi-instance": newIterDecorator(host, step, nil, false),
		"standard loop":  newLoopDecorator(host, step, nil, false),
	} {
		t.Run(name, func(t *testing.T) {
			got := make(chan trackEvent, 1)

			go func() { got <- <-host.instance.events }()

			require.NoError(t, proc.ProcessEvent(t.Context(), def))

			ev := <-got
			require.Equal(t, evDeliver, ev.kind)
			require.Same(t, def, ev.eDef)
			require.NotNil(t, ev.iterProc,
				"the delivery names the decorator, so the loop can ask it "+
					"which instances wait")
		})
	}
}

// fakeIterProc is a stand-in decorator for the LOOP's routing decision: it
// answers the three questions the loop asks and records what it was handed.
type fakeIterProc struct {
	ords      []int
	delivered []int
	waiting   bool
}

func (f *fakeIterProc) waitingOn(string) []int { return f.ords }
func (f *fakeIterProc) anyWaiting() bool       { return f.waiting }

func (f *fakeIterProc) deliverTo(ord int, _ flow.EventDefinition) bool {
	f.delivered = append(f.delivered, ord)

	return true
}

// TestOneOccurrenceReachesOneInstance (SRD-090.B FR-3): the loop serves the
// FIRST instance still waiting, in ordinal order.
//
// A Message is point-to-point, and a Message is the only trigger an iterated
// leaf can carry — so "exactly one" is the whole rule, and ordinal order is
// what makes the choice reproducible when nothing distinguishes the
// instances.
func TestOneOccurrenceReachesOneInstance(t *testing.T) {
	inst, tr, ls := userTaskArmed(t)
	def := sigDefN(t, "sig")

	proc := &fakeIterProc{ords: []int{1, 2, 4}, waiting: true}

	ls.dispatchToInstances(tr, trackEvent{
		kind: evDeliver, track: tr, eDef: def, iterProc: proc,
	})

	require.Equal(t, []int{1}, proc.delivered,
		"the lowest ordinal still waiting, not an arbitrary one")
	require.Contains(t, ls.waiting, tr.ID(),
		"its siblings still wait, so the track stays parked")
	require.NotNil(t, inst)
}

// TestTheTrackLeavesTheParkedSetWithItsLastWaiter: the track is ONE entry
// standing for N waiters, so flipping it out on the first delivery would drop
// every later occurrence at the dispatch gate.
func TestTheTrackLeavesTheParkedSetWithItsLastWaiter(t *testing.T) {
	_, tr, ls := userTaskArmed(t)
	def := sigDefN(t, "sig")

	proc := &fakeIterProc{ords: []int{0}, waiting: false}

	ls.dispatchToInstances(tr, trackEvent{
		kind: evDeliver, track: tr, eDef: def, iterProc: proc,
	})

	require.Equal(t, []int{0}, proc.delivered)
	require.NotContains(t, ls.waiting, tr.ID(),
		"nobody waits any more — the track leaves the parked set")
}

// TestADeliveryNobodyAwaitsIsDropped: an occurrence for an activity whose
// instances have all moved on is a drop, exactly as a losing arm's is.
func TestADeliveryNobodyAwaitsIsDropped(t *testing.T) {
	_, tr, ls := userTaskArmed(t)

	proc := &fakeIterProc{waiting: false}

	ls.dispatchToInstances(tr, trackEvent{
		kind: evDeliver, track: tr, eDef: sigDefN(t, "sig"), iterProc: proc,
	})

	require.Empty(t, proc.delivered)
	require.Contains(t, ls.waiting, tr.ID(),
		"a drop changes nothing — the track's parked state is not the "+
			"delivery's to decide")
}

// TestTheOrdinalComesFromTheExecutor (SRD-090.B FR-2): the ordinal a waiter
// is recorded under is asked of the executor, which is the only thing that
// knows — a decorator's live instance reports its own, and a plain activity
// is instance zero of one.
func TestTheOrdinalComesFromTheExecutor(t *testing.T) {
	_, _, node, host := decoratorFixture(t)
	step := &stepInfo{node: node}

	require.Zero(t, host.execOrdinal(),
		"a track between steps executes nothing, so it reports zero")

	host.exec.Store(&execHandle{e: newNodeExec(host, step, 3), node: node})
	require.Equal(t, 3, host.execOrdinal())
}

// decoratedWaiter puts a track at a ReceiveTask under a sequential
// Multi-Instance, with the decorator installed as its executor — the state a
// pass is in when it arms, parks and withdraws.
func decoratedWaiter(
	t *testing.T, ord int,
) (*Instance, *track, *iterDecorator, flow.EventDefinition) {
	t.Helper()
	require.NoError(t, data.CreateDefaultStates())

	mi, err := activities.NewMultiInstance(activities.WithSequential(),
		activities.WithCardinality(cardExpr(t, 2)))
	require.NoError(t, err)

	recv, err := activities.NewReceiveTask("await",
		bpmncommon.MustMessage("confirm", data.MustItemDefinition(
			values.NewVariable(""), foundation.WithID("arm-confirm"))),
		activities.WithoutParams(), activities.WithLoop(mi))
	require.NoError(t, err)

	p, err := process.New("arm-proc")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, recv, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, recv)
	link(t, recv, end)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	inst, err := New(s, scope.EmptyDataPath, cpRuntime(t), laxEP(t), nil)
	require.NoError(t, err)
	inst.tracks = map[string]*track{}

	node := findNode(t, inst.s, "await")

	tr, err := newTrack(node, inst, nil)
	require.NoError(t, err)

	d := newIterDecorator(tr, &stepInfo{node: node}, multiInstanceOf(node), false)
	d.live.Store(&execHandle{e: newNodeExec(tr, &stepInfo{node: node}, ord)})
	tr.exec.Store(&execHandle{e: d, node: node})

	return inst, tr, d, node.(flow.EventNode).Definitions()[0]
}

// TestArmingRegistersTheDecoratorOncePerActivity (SRD-090.B FR-1/FR-2): the
// first instance to wait registers the DECORATOR with the hub; a sibling
// joins the subscription that already exists and registers nothing.
func TestArmingRegistersTheDecoratorOncePerActivity(t *testing.T) {
	_, tr, d, def := decoratedWaiter(t, 0)

	en, ok := tr.currentStep().node.(flow.EventNode)
	require.True(t, ok)

	require.NoError(t, tr.armWaiters(en, en.Definitions()))
	require.Equal(t, []int{0}, d.waitingOn(def.ID()),
		"the pass that armed is recorded under its own ordinal")
	require.True(t, d.anyWaiting(),
		"and the activity's engine hold is due")

	// a second instance of the SAME activity arms while the first waits.
	d.live.Store(&execHandle{
		e: newNodeExec(tr, &stepInfo{node: tr.currentStep().node}, 1),
	})

	require.NoError(t, tr.armWaiters(en, en.Definitions()))
	require.Equal(t, []int{0, 1}, d.waitingOn(def.ID()),
		"the sibling joins — one subscription, two waiters")
}

// TestAPassParksOnItsOwnBox (SRD-090.B M5b): a pass of an iterated activity
// blocks on the box its ordinal owns, not on the track's shared channel.
func TestAPassParksOnItsOwnBox(t *testing.T) {
	_, tr, d, def := decoratedWaiter(t, 1)

	en, ok := tr.currentStep().node.(flow.EventNode)
	require.True(t, ok)
	require.NoError(t, tr.armWaiters(en, en.Definitions()))

	tr.updateState(TrackWaitForEvent)

	done := make(chan error, 1)

	go func() {
		_, err := tr.parkForDelivery(t.Context(), tr.currentStep())
		done <- err
	}()

	// delivered to ordinal 1's box — the track's own channel is untouched.
	require.Eventually(t, func() bool { return d.deliverTo(1, def) },
		2*time.Second, 5*time.Millisecond)

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("the pass never took the delivery from its own box")
	}

	require.Empty(t, tr.evtCh,
		"nothing was routed through the track's shared channel")
}

// TestTheWithdrawalNamesTheOrdinal (SRD-090.B FR-2): a pass finishing
// withdraws ITS wait; the activity's subscription and hold go only when the
// last instance stops.
func TestTheWithdrawalNamesTheOrdinal(t *testing.T) {
	_, tr, d, def := decoratedWaiter(t, 0)

	en, ok := tr.currentStep().node.(flow.EventNode)
	require.True(t, ok)

	require.NoError(t, tr.armWaiters(en, en.Definitions()))

	d.live.Store(&execHandle{
		e: newNodeExec(tr, &stepInfo{node: tr.currentStep().node}, 1),
	})
	require.NoError(t, tr.armWaiters(en, en.Definitions()))

	// instance 1 delivers and withdraws.
	require.NoError(t, tr.unregisterEvent(en))
	require.Equal(t, []int{0}, d.waitingOn(def.ID()),
		"instance 0 still waits — its wait is not 1's to withdraw")
	require.True(t, d.anyWaiting())

	tr.releaseHolds()
	require.True(t, d.anyWaiting(),
		"and the engine hold stands while it does")

	// instance 0 follows.
	d.live.Store(&execHandle{
		e: newNodeExec(tr, &stepInfo{node: tr.currentStep().node}, 0),
	})
	require.NoError(t, tr.unregisterEvent(en))
	require.False(t, d.anyWaiting(),
		"the last one out releases the activity's subscription and hold")
}
