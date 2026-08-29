package instance

import (
	"context"
	"sort"
	"sync"

	"github.com/dr-dobermann/gobpm/pkg/eventproc"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// iterSubscription is one definition an iterated activity waits on, and the
// instances currently parked on it (ADR-006 §2.9.5).
//
// ONE entry per definition, never one per instance. The hub sees a single
// processor holding a single subscription — exactly what it sees for a plain
// node — which is what keeps the iteration invisible to it: an entry per
// iteration would need an ordinal to tell the entries apart, and an ordinal in
// the hub's registry is iteration vocabulary in a driver (ADR-025 §2.13a).
type iterSubscription struct {
	def flow.EventDefinition

	// waiting is the ordinals parked on this definition, ascending. Empty
	// means the subscription is due to be unregistered: its lifetime is
	// "while any iteration awaits", not "while the activity runs"
	// (SRD-090.B FR-2).
	waiting []int
}

// iterationDelivery is one delivery waiting to be applied, and the instance it
// belongs to.
type iterationDelivery struct {
	def flow.EventDefinition
	ord int
}

// eventSubs is the decorator's half of the EventProcessor contract, embedded
// by every decorator that can hold waiting instances.
//
// The identity is the load-bearing part. `sameProcessor` compares processors
// by ID (`waiters/waiters.go:147`), so this value decides whether a second
// registration joins the existing subscription or creates another. It must
// therefore be STABLE across passes — a fresh id per pass would create one
// subscription per pass, which is the N-subscriptions design ADR-006 §2.9.5
// rejects — and UNIQUE per process instance, because a snapshot clone
// preserves element ids and two instances of one process would otherwise
// present the same identity and unregister each other's waits (the reasoning
// SRD-090.A M2d records for rejecting identity-by-ID at the waiter).
type eventSubs struct {
	subs map[string]*iterSubscription

	// capParked is the ordinals parked on a CAPABILITY rather than an event
	// subscription — a human task, an external-worker Service Task.
	//
	// It exists because a delivery box must belong to PARKING, not to
	// subscription bookkeeping. Opening one as a side effect of registering an
	// event definition works only for instances that have a definition; a User
	// Task has none, so its box was never opened and its completion was routed
	// into a nil channel and dropped. That is the defect that reverted the
	// first attempt at the human fan-out (SRD-090.B §7), and keeping the two
	// lifetimes separate is what prevents it recurring.
	capParked map[int]bool

	// taskIDs is each capability-parked iteration's own parked-work identity,
	// keyed by ordinal (ADR-020 §2.12).
	//
	// The identity is what a person or a UI is holding, and what Withdraw and
	// Complete both name. One slot per ACTIVITY would make N instances
	// announce a single task between them: only one would be addressable and
	// the rest would complete without anyone doing them — three approvals
	// modeled, none performed. So an instance mints its own, keeps it while it
	// is parked (re-minting would invalidate a reference someone is about to
	// act on), and drops it when its work is done.
	taskIDs map[int]string

	// inFlight counts deliveries the loop has handed to an instance that has
	// not woken to take them yet.
	//
	// It exists because the loop's `waiting` entry is ONE entry standing for
	// N waiters, so it cannot say "this activity is parked, except for the
	// iteration now being handed its completion". Between the send and the
	// iteration waking, the activity looks fully parked from the loop — and a
	// dehydration there takes the track away mid-delivery: the instance wakes
	// on dehydrateCh instead of its box, its completion is lost, and the
	// person who did the work is asked to do it again.
	//
	// Raised by the loop before the send, dropped by the instance once it has
	// the envelope, after which its own `parked` flag carries the fact.
	//
	// Declared after the maps so the struct's pointer fields stay contiguous
	// (govet fieldalignment).
	// staged holds a completion that arrived BEFORE its iteration had a box to
	// receive it, keyed by ordinal.
	//
	// A restored fan-out is rebuilt by the very action being applied to it: a
	// person completes a task, that hydrates the instance, and the decorator
	// then re-launches its instances on their own goroutines. The completion
	// can therefore reach the loop while the instance it belongs to has not
	// parked yet — and a delivery to a box that does not exist yet was
	// silently dropped, so the work was marked done, the task withdrawn, and
	// the activity waited forever for an instance nobody could complete
	// again.
	//
	// ready is the doorbell. Buffered by one and rung without blocking: the
	// LOOP is the sender and must never stall on an activity's goroutine.
	ready chan struct{}

	// id is "<iteration>/<node>": stable across the activity's passes and
	// across a restore, distinct per process instance.
	id string

	// pending is the deliveries the decorator has yet to apply, each naming
	// the instance it belongs to.
	//
	// ONE queue for the activity, not a channel per instance, because the
	// DECORATOR applies them — serially, on its own goroutine (ADR-025
	// §2.15a). The instances are state it owns, not goroutines with mailboxes
	// of their own, so what an arriving delivery needs is an ordinal and a
	// place to wait its turn.
	//
	// It also removes a whole class of ordering bug by construction. While
	// each iteration owned a channel, a delivery could arrive before that
	// channel existed (a restored fan-out is rebuilt by the very action being
	// applied to it) or after it was released, and either way it was silently
	// dropped: the work was marked performed and the activity waited forever
	// for an approval nobody could give again. A queue owned by the decorator
	// has no such window — it exists for as long as the activity does.
	pending []iterationDelivery

	// mu guards subs. The HUB's goroutine reads through ProcessEvent while
	// the decorator's own goroutine arms and disarms.
	mu sync.Mutex

	// declared last, with mu, so the struct's pointer-bearing fields stay
	// contiguous (govet fieldalignment).
	inFlight int
}

// newEventSubs builds the subscription set for one iterated activity.
func newEventSubs(instanceID, nodeID string) eventSubs {
	return eventSubs{
		id:        instanceID + "/" + nodeID,
		subs:      map[string]*iterSubscription{},
		capParked: map[int]bool{},
		taskIDs:   map[int]string{},
		ready:     make(chan struct{}, 1),
	}
}

// subsIDFor derives a decorator's subscriber identity from what builds it.
//
// Tolerant of a track that carries no instance, and of no track at all: the
// package's tests build bare decorators to ask them a question that has
// nothing to do with subscriptions — what kind they iterate, what their live
// iteration awaits — and a constructor that panics on those would make the
// event work reach into every unrelated fixture. Such a decorator gets an
// identity naming only its node, which is exactly as much as it can know and
// is never registered with anything.
func subsIDFor(t *track, node flow.Node) eventSubs {
	instID := ""
	if t != nil && t.instance != nil {
		instID = t.instance.ID()
	}

	nodeID := ""
	if node != nil {
		nodeID = node.ID()
	}

	return newEventSubs(instID, nodeID)
}

// ID implements foundation.Identifyer, the half of eventproc.EventProcessor
// the hub uses to tell one subscriber from another.
func (es *eventSubs) ID() string {
	return es.id
}

// awaiting records that iteration ord is now parked on def, and reports
// whether this is the FIRST iteration to wait on it — which is when the
// decorator must register with the hub (SRD-090.B FR-2).
func (es *eventSubs) awaiting(def flow.EventDefinition, ord int) bool {
	es.mu.Lock()
	defer es.mu.Unlock()

	s, ok := es.subs[def.ID()]
	if !ok {
		es.subs[def.ID()] = &iterSubscription{def: def, waiting: []int{ord}}

		return true
	}

	if idx := sort.SearchInts(s.waiting, ord); idx == len(s.waiting) ||
		s.waiting[idx] != ord {
		s.waiting = append(s.waiting, 0)
		copy(s.waiting[idx+1:], s.waiting[idx:])
		s.waiting[idx] = ord
	}

	return false
}

// parking records that iteration ord is parked on a CAPABILITY — a human task
// awaiting a completion, an external-worker task awaiting a report — and opens
// its delivery box.
//
// There is nothing to register with the hub: such a wait is addressed by a
// task identity rather than an event definition, so the decorator only needs
// to know the instance is waiting, and to have somewhere to deliver its
// outcome (ADR-025 §2.15a).
func (es *eventSubs) parking(ord int) {
	es.mu.Lock()
	defer es.mu.Unlock()

	es.capParked[ord] = true
}

// deliver queues iteration ord's completion for the decorator to apply, and
// closes out that iteration's capability wait.
//
// It never blocks and never drops. The LOOP is the caller, so blocking it on
// an activity's goroutine would stall every other track in the instance; and
// a delivery that arrives before the decorator is applying — the ordinary case
// for a restored fan-out, which is rebuilt by the action being applied to it —
// simply waits its turn in the queue.
func (es *eventSubs) deliver(ord int, def flow.EventDefinition) {
	es.queue(iterationDelivery{ord: ord, def: def})
}

// queue appends a delivery and rings the doorbell.
func (es *eventSubs) queue(d iterationDelivery) {
	es.mu.Lock()
	delete(es.capParked, d.ord)
	es.pending = append(es.pending, d)
	es.mu.Unlock()

	select {
	case es.ready <- struct{}{}:
	default: // already rung; the decorator drains everything it finds
	}
}

// takeDeliveries hands the decorator everything queued since it last looked.
func (es *eventSubs) takeDeliveries() []iterationDelivery {
	es.mu.Lock()
	defer es.mu.Unlock()

	held := es.pending
	es.pending = nil

	return held
}

// deliveries is the doorbell the decorator selects on.
func (es *eventSubs) deliveries() <-chan struct{} { return es.ready }

// taskIDFor returns iteration ord's parked-work identity, minting one on first
// ask and returning the same value while the instance stays parked.
//
// Stability is the requirement: the id is a reference a human or a UI holds,
// so re-minting it mid-wait would invalidate the very thing they are about to
// act on. A restored iteration is given its recorded id through adoptTaskID
// before anything asks, for the same reason (SRD-071 FR-8).
func (es *eventSubs) taskIDFor(ord int) string {
	es.mu.Lock()
	defer es.mu.Unlock()

	if id, ok := es.taskIDs[ord]; ok {
		return id
	}

	id := foundation.GenerateID()
	es.taskIDs[ord] = id

	return id
}

// adoptTaskID gives iteration ord the identity a checkpoint recorded, so a
// rehydrated task keeps the id its inbox entry already carries.
func (es *eventSubs) adoptTaskID(ord int, id string) {
	if id == "" {
		return
	}

	es.mu.Lock()
	defer es.mu.Unlock()

	es.taskIDs[ord] = id
}

// delivering records that a completion is on its way to iteration ord. Called
// by the LOOP, before the send.
func (es *eventSubs) delivering() {
	es.mu.Lock()
	defer es.mu.Unlock()

	es.inFlight++
}

// delivered records that iteration ord has taken its envelope and is executing
// again. Called by the INSTANCE, once awaitTrigger has returned it.
func (es *eventSubs) delivered() {
	es.mu.Lock()
	defer es.mu.Unlock()

	if es.inFlight > 0 {
		es.inFlight--
	}
}

// busy reports whether any iteration of this activity is executing rather than
// parked — either mid-handoff, or already awake and running its node.
func (es *eventSubs) busy() bool {
	es.mu.Lock()
	defer es.mu.Unlock()

	return es.inFlight > 0 || len(es.pending) > 0
}

// taskIDSnapshot copies the live parked-work identities, keyed by ordinal, for
// the checkpoint to record (ADR-020 §2.12).
//
// A restore MUST give each iteration back the id it was announced under: the
// task outlives the instance's residency in the distributor's inbox, so a
// fresh mint would invalidate every reference a person or a UI is holding.
// The track's single recorded id can carry one of N, which is why this is per
// ordinal.
func (es *eventSubs) taskIDSnapshot() map[int]string {
	es.mu.Lock()
	defer es.mu.Unlock()

	if len(es.taskIDs) == 0 {
		return nil
	}

	out := make(map[int]string, len(es.taskIDs))
	for k, v := range es.taskIDs {
		out[k] = v
	}

	return out
}

// dropTaskID forgets iteration ord's identity once its work is done, so a later
// pass of the same activity mints its own rather than reusing a handle that
// now names nothing (ADR-020 §2.12).
func (es *eventSubs) dropTaskID(ord int) {
	es.mu.Lock()
	defer es.mu.Unlock()

	delete(es.taskIDs, ord)
}

// stopped records that iteration ord is no longer parked on def, and reports
// whether NONE now is — which is when the decorator unregisters. An unknown
// definition or ordinal is not an error: an instance that faulted before it
// parked has nothing to withdraw.
func (es *eventSubs) stopped(def flow.EventDefinition, ord int) bool {
	es.mu.Lock()
	defer es.mu.Unlock()

	s, ok := es.subs[def.ID()]
	if !ok {
		return false
	}

	if idx := sort.SearchInts(s.waiting, ord); idx < len(s.waiting) &&
		s.waiting[idx] == ord {
		s.waiting = append(s.waiting[:idx], s.waiting[idx+1:]...)
	}

	if len(s.waiting) > 0 {
		return false
	}

	delete(es.subs, def.ID())

	return true
}

// waitingFor reports whether iteration ord holds a wait — on a definition or a
// capability. An instance that holds none has nothing to be delivered and is
// simply run.
func (es *eventSubs) waitingFor(ord int) bool {
	es.mu.Lock()
	defer es.mu.Unlock()

	return es.waitsAnywhereLocked(ord)
}

// waitsAnywhereLocked reports whether ord still holds a wait of any kind — a
// capability, or any definition it subscribed to. Caller holds mu.
func (es *eventSubs) waitsAnywhereLocked(ord int) bool {
	if es.capParked[ord] {
		return true
	}

	for _, s := range es.subs {
		if idx := sort.SearchInts(s.waiting, ord); idx < len(s.waiting) &&
			s.waiting[idx] == ord {
			return true
		}
	}

	return false
}

// deliverTo hands eDef to iteration ord, reporting whether it landed. False
// means the instance is no longer waiting — a losing arm, or a sibling that
// completed while the delivery was in flight — which is a drop, not an error
// (SRD-027 FR-4's rule at iteration granularity).
func (es *eventSubs) deliverTo(ord int, eDef flow.EventDefinition) bool {
	es.mu.Lock()
	_, waiting := es.capParked[ord]

	if !waiting {
		for _, sub := range es.subs {
			if idx := sort.SearchInts(sub.waiting, ord); idx < len(sub.waiting) &&
				sub.waiting[idx] == ord {
				waiting = true

				break
			}
		}
	}
	es.mu.Unlock()

	if !waiting {
		return false
	}

	es.queue(iterationDelivery{ord: ord, def: eDef})

	return true
}

// anyWaiting reports whether ANY iteration of this activity is parked on any
// of its definitions — which is what decides the engine hold's lifetime
// (SRD-090.B FR-2).
//
// A hold is keyed (instanceID, trackID) and `ReleaseWaits` withdraws every
// hold taken for a track, so an activity whose instances share a host track
// must not release while a sibling still waits: the sibling would be left
// with nothing able to wake a released instance.
func (es *eventSubs) anyWaiting() bool {
	es.mu.Lock()
	defer es.mu.Unlock()

	return len(es.subs) > 0 || len(es.capParked) > 0
}

// waitingOn returns the ordinals parked on def, ascending — the dispatch
// order FR-3 specifies. A copy, because the caller reads it off the hub's
// goroutine while the decorator may still be arming.
//
// Ascending ORDINAL order rather than arrival order is the point: when
// nothing distinguishes the instances, nothing but their ordinals can decide
// which receives an indistinguishable envelope, and two runs of one model
// must not disagree.
func (es *eventSubs) waitingOn(defID string) []int {
	es.mu.Lock()
	defer es.mu.Unlock()

	s, ok := es.subs[defID]
	if !ok {
		return nil
	}

	return append([]int(nil), s.waiting...)
}

// activitySubscriber is what an executor answers with when it, rather than
// the track, owns the activity's waits (SRD-090.B FR-1).
//
// An EventProcessor the hub can register, plus the two bookkeeping questions
// only the owner can answer: is this the FIRST iteration to wait on the
// definition — so the hub registration is due — and was that the LAST — so
// the withdrawal is. That is FR-2's lifetime, expressed as the two moments it
// turns on and off.
type activitySubscriber interface {
	eventproc.EventProcessor

	awaiting(def flow.EventDefinition, ord int) bool
	stopped(def flow.EventDefinition, ord int) bool

	// anyWaiting gates the engine HOLD, whose lifetime is the whole
	// activity's rather than one instance's.
	anyWaiting() bool

	// parking records a CAPABILITY wait — one addressed by a task identity
	// rather than an event definition. It is stated separately from
	// awaiting/stopped because such a wait has no definition to register.
	parking(ord int)

	// completeIteration hands one instance its completion. The DECORATOR
	// decides where it goes: a fan-out queues it for serial application, and
	// every other shape runs one pass at a time, which is parked on the
	// track's own channel.
	completeIteration(ord int, def flow.EventDefinition, owner string)

	// delivering and delivered bracket the handoff, so the loop can tell a
	// fully parked activity from one with work in flight.
	delivering()
	delivered()
	busy() bool

	// taskIDFor, adoptTaskID and dropTaskID own the parked-work identity of
	// one instance (ADR-020 §2.12); taskIDSnapshot hands the set to the
	// checkpoint so a restore returns each iteration the id it was announced
	// under.
	taskIDFor(ord int) string
	adoptTaskID(ord int, id string)
	dropTaskID(ord int)
	taskIDSnapshot() map[int]string
}

// ProcessEvent is the hub's doorbell (ADR-006 §2.9.5): it runs on the HUB's
// goroutine and does not deliver anything itself — it emits to the loop,
// which owns routing and decides that exactly one runner receives a given
// occurrence. Same contract as track.ProcessEvent, which is the point: the
// decorator substitutes for the track in a chain that already exists, rather
// than introducing one.
//
// The fan-out to the instances waiting on the definition is the LOOP's, from
// waitingOn — dispatch belongs where the single writer is (SRD-090.B FR-3,
// landing with M4).
func (d *iterDecorator) ProcessEvent(
	_ context.Context, eDef flow.EventDefinition,
) error {
	d.t.instance.emit(trackEvent{
		kind: evDeliver, track: d.t, eDef: eDef, iterProc: d,
	})

	return nil
}

// ProcessEvent — see iterDecorator.ProcessEvent. A Standard Loop holds one
// pass at a time, so its waiting set never exceeds one; the shape is shared
// so the loop routes both kinds through one path.
func (d *loopDecorator) ProcessEvent(
	_ context.Context, eDef flow.EventDefinition,
) error {
	d.t.instance.emit(trackEvent{
		kind: evDeliver, track: d.t, eDef: eDef, iterProc: d,
	})

	return nil
}
