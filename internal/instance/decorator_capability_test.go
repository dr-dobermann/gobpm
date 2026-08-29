package instance

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A CAPABILITY WAIT is one addressed by a task identity rather than an event
// definition — a human task awaiting a completion, an external-worker task
// awaiting a report (ADR-025 §2.15a). These exercise the decorator's half of
// it directly: the integration case lives in pkg/thresher, whose coverage
// credits that package rather than this one.

// TestEachParkedInstanceGetsItsOwnIdentity (ADR-020 §2.12): N instances of one
// activity park at once, so the parked-work identity cannot be the activity's.
//
// One slot per activity is what made three approvals announce a single task:
// only that one was addressable, and the rest completed without anyone doing
// them.
func TestEachParkedInstanceGetsItsOwnIdentity(t *testing.T) {
	es := newEventSubs("inst", "node")

	es.parking(0)
	es.parking(1)
	require.True(t, es.anyWaiting())

	first, second := es.taskIDFor(0), es.taskIDFor(1)
	require.NotEmpty(t, first)
	require.NotEqual(t, first, second,
		"an identity per instance, not per activity")

	require.Equal(t, first, es.taskIDFor(0),
		"and STABLE while it stays parked: the id is a reference a person or "+
			"a UI is holding, so re-minting it mid-wait would invalidate the "+
			"very thing they are about to act on")
}

// TestARestoredInstanceKeepsTheIdentityItWasAnnouncedUnder (SRD-071 FR-8): a
// rehydrated instance is given its recorded id before anything asks it for
// one, so the handle in somebody's inbox still names its work.
func TestARestoredInstanceKeepsTheIdentityItWasAnnouncedUnder(t *testing.T) {
	es := newEventSubs("inst", "node")

	es.adoptTaskID(1, "recorded-1")
	require.Equal(t, "recorded-1", es.taskIDFor(1),
		"the recorded value wins over a fresh mint")

	es.adoptTaskID(2, "")
	require.NotEmpty(t, es.taskIDFor(2),
		"an EMPTY recorded id adopts nothing — a track carrying no identity "+
			"for this ordinal must not blank one that is about to be minted")
}

// TestACompletionWaitsInTheDecoratorsQueue (ADR-025 §2.15a): a completion is
// queued for the DECORATOR, naming the instance it belongs to, and waits there
// until the decorator applies it.
//
// It holds whether or not that instance has parked yet. A restored fan-out is
// rebuilt by the very action being applied to it, so a completion routinely
// reaches the loop first — and while each instance owned a channel, such a
// completion was dropped: the work was marked performed, the task withdrawn,
// and the activity waited forever for an approval nobody could give again.
func TestACompletionWaitsInTheDecoratorsQueue(t *testing.T) {
	es := newEventSubs("inst", "node")
	early, late := sigDefN(t, "early"), sigDefN(t, "late")

	es.deliver(2, early) // before instance 2 has parked
	es.parking(0)
	es.deliver(0, late)

	held := es.takeDeliveries()
	require.Len(t, held, 2, "neither is dropped for arriving out of order")

	require.Equal(t, 2, held[0].ord)
	require.Same(t, early, held[0].def)
	require.Equal(t, 0, held[1].ord)
	require.Same(t, late, held[1].def,
		"and they queue in arrival order, so the decorator applies them in "+
			"the order the work was actually done")
}

// TestTheDoorbellRingsForAQueuedDelivery: the decorator selects on it rather
// than polling, and the LOOP rings it without ever blocking.
func TestTheDoorbellRingsForAQueuedDelivery(t *testing.T) {
	es := newEventSubs("inst", "node")

	select {
	case <-es.deliveries():
		t.Fatal("nothing queued, nothing to wake for")
	default:
	}

	es.parking(0)
	es.deliver(0, sigDefN(t, "done"))

	select {
	case <-es.deliveries():
	default:
		t.Fatal("a queued delivery must wake the decorator")
	}
}

// TestTheActivityStaysWaitingWhileAnyInstanceHoldsWork: one entry stands for N
// waiters, so it stops waiting only when the last of them is done.
func TestTheActivityStaysWaitingWhileAnyInstanceHoldsWork(t *testing.T) {
	es := newEventSubs("inst", "node")
	def := sigDefN(t, "done")

	es.parking(0)
	es.parking(1)
	require.True(t, es.anyWaiting())

	es.deliver(0, def)
	require.True(t, es.anyWaiting(),
		"one instance is done; the other still holds an approval")

	es.deliver(1, def)
	require.False(t, es.anyWaiting(), "now nobody does")
}

// TestAnActivityReadsBusyWhileItHasWorkToApply: between the loop queuing a
// completion and the decorator finishing with it, the activity is NOT idle —
// and a dehydration there would take the track away mid-application.
func TestAnActivityReadsBusyWhileItHasWorkToApply(t *testing.T) {
	es := newEventSubs("inst", "node")

	require.False(t, es.busy(), "nothing queued, nothing in flight")

	es.parking(0)
	es.deliver(0, sigDefN(t, "done"))
	require.True(t, es.busy(), "a completion is waiting to be applied")

	es.takeDeliveries()
	es.delivering()
	require.True(t, es.busy(), "and now it is being applied")

	es.delivered()
	require.False(t, es.busy())

	es.delivered()
	require.False(t, es.busy(),
		"the count never goes negative — an unbalanced drop would leave the "+
			"activity permanently idle-looking")
}

// TestTheIdentitySetIsSnapshottedPerOrdinal (ADR-020 §2.12): the checkpoint
// records what each instance was announced under, keyed by ordinal, because
// the track's single recorded id can only ever carry one of N.
func TestTheIdentitySetIsSnapshottedPerOrdinal(t *testing.T) {
	es := newEventSubs("inst", "node")

	require.Nil(t, es.taskIDSnapshot(), "nothing parked, nothing to record")

	zero, one := es.taskIDFor(0), es.taskIDFor(1)

	snap := es.taskIDSnapshot()
	require.Equal(t, map[int]string{0: zero, 1: one}, snap)

	snap[0] = "tampered"
	require.Equal(t, zero, es.taskIDFor(0),
		"the snapshot is a COPY: the capture must not be able to reach into "+
			"the live set the instances are using")

	es.dropTaskID(0)
	require.Equal(t, map[int]string{1: one}, es.taskIDSnapshot(),
		"an accounted-for instance keeps no identity — a later pass of the "+
			"same activity mints its own rather than reusing a handle that "+
			"now names nothing")
}

// TestAWaitIsAWaitWhicheverKindItIs: an instance holds one until it holds
// none, and the two kinds are asked about together — a capability wait has no
// definition to register, and an event wait no task identity.
func TestAWaitIsAWaitWhicheverKindItIs(t *testing.T) {
	es := newEventSubs("inst", "node")
	def := sigDefN(t, "sig")

	require.False(t, es.waitingFor(0), "nothing yet")

	es.parking(0)
	require.True(t, es.waitingFor(0), "a capability wait counts")

	es.awaiting(def, 1)
	require.True(t, es.waitingFor(1), "and so does a subscription")

	es.deliver(0, def)
	require.False(t, es.waitingFor(0), "delivered, and no longer waiting")
}
