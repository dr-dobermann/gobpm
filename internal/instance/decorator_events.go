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
// instance would need an ordinal to tell the entries apart, and an ordinal in
// the hub's registry is iteration vocabulary in a driver (ADR-025 §2.13a).
type iterSubscription struct {
	def flow.EventDefinition

	// waiting is the ordinals parked on this definition, ascending. Empty
	// means the subscription is due to be unregistered: its lifetime is
	// "while any instance awaits", not "while the activity runs"
	// (SRD-090.B FR-2).
	waiting []int
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

	// taskIDs is each capability-parked instance's own parked-work identity,
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
	// instance now being handed its completion". Between the send and the
	// instance waking, the activity looks fully parked from the loop — and a
	// dehydration there takes the track away mid-delivery: the instance wakes
	// on dehydrateCh instead of its box, its completion is lost, and the
	// person who did the work is asked to do it again.
	//
	// Raised by the loop before the send, dropped by the instance once it has
	// the envelope, after which its own `parked` flag carries the fact.
	//
	// Declared after the maps so the struct's pointer fields stay contiguous
	// (govet fieldalignment).
	// staged holds a completion that arrived BEFORE its instance had a box to
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
	// Held here rather than retried because the loop must not block: the
	// instance is moments away from parking, and parking is what hands it
	// over.
	staged map[int]flow.EventDefinition

	// boxes is each waiting instance's own delivery channel, keyed by
	// ordinal (SRD-090.B M5b).
	//
	// Per INSTANCE rather than per definition: an instance waits on one node
	// at a time, however many arms that node carries. And per instance
	// rather than per track, for the reason M3b gave the composite drain —
	// "the host track's evtCh could only ever serve one waiter, so a second
	// instance of the same activity would have consumed the first one's".
	// A broadcast has to reach N of them, and an ordinal-ordered dispatch
	// has to choose WHICH, neither of which one shared channel can express.
	boxes map[int]chan flow.EventDefinition

	// id is "<instance>/<node>": stable across the activity's passes and
	// across a restore, distinct per process instance.
	id string

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
		boxes:     map[int]chan flow.EventDefinition{},
		capParked: map[int]bool{},
		staged:    map[int]flow.EventDefinition{},
		taskIDs:   map[int]string{},
	}
}

// subsIDFor derives a decorator's subscriber identity from what builds it.
//
// Tolerant of a track that carries no instance, and of no track at all: the
// package's tests build bare decorators to ask them a question that has
// nothing to do with subscriptions — what kind they iterate, what their live
// instance awaits — and a constructor that panics on those would make the
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

// awaiting records that instance ord is now parked on def, and reports
// whether this is the FIRST instance to wait on it — which is when the
// decorator must register with the hub (SRD-090.B FR-2).
func (es *eventSubs) awaiting(def flow.EventDefinition, ord int) bool {
	es.mu.Lock()
	defer es.mu.Unlock()

	es.openBoxLocked(ord)

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

// openBoxLocked gives instance ord its delivery channel if it has none. The
// single place a box is created, so every way of parking gets one alike.
// Caller holds mu.
func (es *eventSubs) openBoxLocked(ord int) {
	if _, open := es.boxes[ord]; !open {
		es.boxes[ord] = make(chan flow.EventDefinition, eventBufferDepth)
	}
}

// parking records that instance ord is parked on a CAPABILITY — a human task
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

	es.openBoxLocked(ord)
	es.capParked[ord] = true

	// a completion that arrived before this box existed is handed over now,
	// which is the moment there is somewhere to put it. The box is buffered,
	// and this instance is the only reader, so the send cannot block.

	if def, waiting := es.staged[ord]; waiting {
		delete(es.staged, ord)
		es.boxes[ord] <- def
	}
}

// deliver hands instance ord its completion and closes out its capability
// wait, in ONE step under the lock.
//
// The two cannot be separate calls. Dropping the wait releases the instance's
// delivery box, and the loop's release raced the instance's park: the box was
// created and the envelope pushed into it, and the release then deleted the
// box WITH the envelope inside. The instance fell back to the track's channel
// and waited forever for a completion someone had already performed.
//
// The box is deliberately not dropped here — the envelope is in it and the
// instance is about to read it. It goes when the decorator does, which is the
// end of the activity's run either way.
func (es *eventSubs) deliver(ord int, def flow.EventDefinition) {
	es.mu.Lock()
	defer es.mu.Unlock()

	delete(es.capParked, ord)

	// NOT PARKED YET: a restored fan-out is rebuilt by the very action being
	// applied to it, so a completion can arrive before its instance has
	// anywhere to receive it. Held until parking hands it over.
	box, open := es.boxes[ord]
	if !open {
		es.staged[ord] = def

		return
	}

	box <- def
}

// taskIDFor returns instance ord's parked-work identity, minting one on first
// ask and returning the same value while the instance stays parked.
//
// Stability is the requirement: the id is a reference a human or a UI holds,
// so re-minting it mid-wait would invalidate the very thing they are about to
// act on. A restored instance is given its recorded id through adoptTaskID
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

// adoptTaskID gives instance ord the identity a checkpoint recorded, so a
// rehydrated task keeps the id its inbox entry already carries.
func (es *eventSubs) adoptTaskID(ord int, id string) {
	if id == "" {
		return
	}

	es.mu.Lock()
	defer es.mu.Unlock()

	es.taskIDs[ord] = id
}

// delivering records that a completion is on its way to instance ord. Called
// by the LOOP, before the send.
func (es *eventSubs) delivering() {
	es.mu.Lock()
	defer es.mu.Unlock()

	es.inFlight++
}

// delivered records that instance ord has taken its envelope and is executing
// again. Called by the INSTANCE, once awaitTrigger has returned it.
func (es *eventSubs) delivered() {
	es.mu.Lock()
	defer es.mu.Unlock()

	if es.inFlight > 0 {
		es.inFlight--
	}
}

// busy reports whether any instance of this activity is executing rather than
// parked — either mid-handoff, or already awake and running its node.
func (es *eventSubs) busy() bool {
	es.mu.Lock()
	defer es.mu.Unlock()

	return es.inFlight > 0
}

// taskIDSnapshot copies the live parked-work identities, keyed by ordinal, for
// the checkpoint to record (ADR-020 §2.12).
//
// A restore MUST give each instance back the id it was announced under: the
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

// dropTaskID forgets instance ord's identity once its work is done, so a later
// pass of the same activity mints its own rather than reusing a handle that
// now names nothing (ADR-020 §2.12).
func (es *eventSubs) dropTaskID(ord int) {
	es.mu.Lock()
	defer es.mu.Unlock()

	delete(es.taskIDs, ord)
}

// stopped records that instance ord is no longer parked on def, and reports
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

	if !es.waitsAnywhereLocked(ord) {
		delete(es.boxes, ord)
	}

	if len(s.waiting) > 0 {
		return false
	}

	delete(es.subs, def.ID())

	return true
}

// waitsAnywhereLocked reports whether ord is still parked on ANY definition —
// the condition for keeping its delivery box open. Caller holds mu.
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

// boxFor returns instance ord's delivery channel — what its unit blocks on
// instead of the track's shared one.
func (es *eventSubs) boxFor(ord int) chan flow.EventDefinition {
	es.mu.Lock()
	defer es.mu.Unlock()

	return es.boxes[ord]
}

// deliverTo hands eDef to instance ord, reporting whether it landed. False
// means the instance is no longer waiting — a losing arm, or a sibling that
// completed while the delivery was in flight — which is a drop, not an error
// (SRD-027 FR-4's rule at iteration granularity).
//
// Non-blocking: the box is buffered exactly as the track's channel is, and
// the LOOP is the sender, so a full box must never stall the single writer.
func (es *eventSubs) deliverTo(ord int, eDef flow.EventDefinition) bool {
	es.mu.Lock()
	box, ok := es.boxes[ord]
	es.mu.Unlock()

	if !ok {
		return false
	}

	select {
	case box <- eDef:
		return true
	default:
		return false
	}
}

// anyWaiting reports whether ANY instance of this activity is parked on any
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
// only the owner can answer: is this the FIRST instance to wait on the
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

	// boxFor is the channel instance ord's unit blocks on for its own
	// delivery.
	boxFor(ord int) chan flow.EventDefinition

	// parking and deliver bracket a CAPABILITY wait — one addressed by a
	// task identity rather than an event definition. They exist separately
	// from awaiting/stopped because such a wait has no definition to hang a
	// delivery box off, and a box opened only as a side effect of
	// subscribing is a box a human task never gets.
	parking(ord int)

	// delivering and delivered bracket the handoff of a completion, so the
	// loop can tell a fully parked activity from one with work in flight.
	delivering()
	delivered()
	busy() bool

	// deliver hands one instance its completion and ends its capability wait.
	deliver(ord int, def flow.EventDefinition)

	// taskIDFor, adoptTaskID and dropTaskID own the parked-work identity of
	// one instance (ADR-020 §2.12); taskIDSnapshot hands the set to the
	// checkpoint so a restore returns each instance the id it was announced
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
