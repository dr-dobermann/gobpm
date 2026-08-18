package instance

import (
	"context"
	"sort"
	"sync"

	"github.com/dr-dobermann/gobpm/pkg/eventproc"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
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

	// id is "<instance>/<node>": stable across the activity's passes and
	// across a restore, distinct per process instance.
	id string

	// mu guards subs. The HUB's goroutine reads through ProcessEvent while
	// the decorator's own goroutine arms and disarms.
	mu sync.Mutex
}

// newEventSubs builds the subscription set for one iterated activity.
func newEventSubs(instanceID, nodeID string) eventSubs {
	return eventSubs{
		id:   instanceID + "/" + nodeID,
		subs: map[string]*iterSubscription{},
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

	if len(s.waiting) > 0 {
		return false
	}

	delete(es.subs, def.ID())

	return true
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
