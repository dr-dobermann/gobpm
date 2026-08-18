package instance

import (
	"testing"

	"github.com/stretchr/testify/require"

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
