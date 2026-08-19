package instance

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
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
