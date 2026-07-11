/*
Package eventhub provides event hub implementation for BPMN processes.

This package is part of GoBPM - Business Process Management Engine for Go.
See LICENSE file for license information.

Author: dr-dobermann (rgabitov@gmail.com)
Repository: https://github.com/dr-dobermann/gobpm
*/
package eventhub

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/internal/eventproc/eventhub/waiters"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/renv"
)

const errorClass = "EVENT_HUB_ERRORS"

// hubState is the EventHub lifecycle, a single source of truth (one field can't
// hold the invalid started-and-stopped combination two booleans allowed).
type hubState uint8

const (
	// hubNotStarted is a freshly created hub, before Start.
	hubNotStarted hubState = iota
	// hubStarted is a started hub accepting registration and propagation.
	hubStarted
	// hubStopped is a shut-down hub: it drained its waiters and rejects further
	// registration (terminal).
	hubStopped
)

type (
	// EventHub processes all registration requests from EventProcessors
	// for specific eventDefinition.
	// On every pair EventProcessor - eventDefinition EventHub creates
	// personal eventWaiter and runs its Service in separate go-routine.
	EventHub struct {
		ctx     context.Context
		rt      renv.EngineRuntime
		waiters map[string]eventproc.EventWaiter
		// signalIdx groups the registered signal waiters by signal name, so a thrown
		// signal reaches its catchers by an O(1) name lookup instead of scanning every
		// waiter (SRD-027 FR-6). One entry per waiter (per catch eDef.ID()), not per
		// processor; maintained under m alongside waiters at the register/remove sites.
		signalIdx map[string][]eventproc.EventWaiter
		events    []flow.EventDefinition
		m         sync.RWMutex
		// state is read lock-free by Run/PropagateEvent and written by
		// Start/Shutdown, so it lives in an atomic to stay race-free across those
		// unsynchronized readers (registration/shutdown still serialize the map
		// under m; the atomic just removes the state data race).
		state atomic.Uint32
	}
)

// New creates a new EventHub. rt is the engine's resolved runtime, passed to
// every waiter the hub builds so timer/expression waiters reach Clock /
// ExpressionEngine (ADR-002 §4.3).
func New(rt renv.EngineRuntime) (*EventHub, error) {
	if rt == nil {
		return nil, errs.New(
			errs.M("empty engine runtime"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	return &EventHub{
			rt:        rt,
			waiters:   map[string]eventproc.EventWaiter{},
			signalIdx: map[string][]eventproc.EventWaiter{},
			events:    []flow.EventDefinition{},
		},
		nil
}

// getState reads the lock-free hub lifecycle state.
func (eh *EventHub) getState() hubState {
	// hubState is a 0..2 enum stored in the atomic; the narrowing never
	// overflows.
	//nolint:gosec // bounded enum, no overflow
	return hubState(eh.state.Load())
}

// setState writes the lock-free hub lifecycle state.
func (eh *EventHub) setState(s hubState) {
	eh.state.Store(uint32(s))
}

// Start performs synchronous initialization of the EventHub: records the
// context that subsequent Run / RegisterEvent / UnregisterEvent /
// PropagateEvent calls will observe, and flips the started flag.
//
// Start MUST be called exactly once before Run. Returning from Start
// establishes a happens-before edge — any caller that observes the
// successful return is guaranteed to see the hub in the started state and the
// stored ctx, without needing additional synchronization. This is the
// motivation for splitting Start from Run; see FIX-001.
func (eh *EventHub) Start(ctx context.Context) error {
	if eh.getState() != hubNotStarted {
		return errs.New(
			errs.M("eventHub is already started or stopped"),
			errs.C(errorClass, errs.InvalidState))
	}

	eh.setState(hubStarted)
	eh.ctx = ctx

	return nil
}

// Run is the blocking event-processing loop. It MUST be invoked after
// Start has returned successfully; calling Run on a non-started hub
// returns an error.
//
// Run blocks until its context is canceled and then returns ctx.Err().
func (eh *EventHub) Run(ctx context.Context) error {
	if eh.getState() != hubStarted {
		return errs.New(
			errs.M("eventHub isn't started"),
			errs.C(errorClass, errs.InvalidState))
	}

	<-ctx.Done()

	return ctx.Err()
}

// --------------------------- eventproc.EventProducer ------------------------

// RegisterEvent registers the EventDefinitions from the single EventProcessor.
func (eh *EventHub) RegisterEvent(
	ep eventproc.EventProcessor,
	eDef flow.EventDefinition,
) error {
	if eh.getState() != hubStarted {
		return errs.New(
			errs.M("eventHub isn't started"),
			errs.C(errorClass, errs.InvalidState))
	}

	if ep == nil {
		return errs.New(
			errs.M("empty event processor isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if eDef == nil {
		return errs.New(
			errs.M("EventHub.RegisterEvent: a nil EventDefinition isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	return eh.registerWaiter(ep, eDef, waiters.CreateWaiter)
}

// RegisterPersistentEvent registers a persistent instance-starter subscription
// (SRD-015): the waiter built by waiters.CreatePersistentWaiter fires for every
// matching message and is retained until UnregisterEvent/Stop, unlike the
// single-shot in-instance receiver RegisterEvent builds. Only message triggers
// are accepted (CreatePersistentWaiter enforces it).
func (eh *EventHub) RegisterPersistentEvent(
	ep eventproc.EventProcessor,
	eDef flow.EventDefinition,
) error {
	if eh.getState() != hubStarted {
		return errs.New(
			errs.M("eventHub isn't started"),
			errs.C(errorClass, errs.InvalidState))
	}

	if ep == nil {
		return errs.New(
			errs.M("empty event processor isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if eDef == nil {
		return errs.New(
			errs.M("EventHub.RegisterPersistentEvent: a nil EventDefinition "+
				"isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	return eh.registerWaiter(ep, eDef, waiters.CreatePersistentWaiter)
}

// waiterBuilder builds the waiter a registration installs — either the
// single-shot waiters.CreateWaiter or the persistent
// waiters.CreatePersistentWaiter. Extracting it lets RegisterEvent and
// RegisterPersistentEvent share the one critical section below.
type waiterBuilder func(
	eventproc.EventHub,
	eventproc.EventProcessor,
	flow.EventDefinition,
	renv.EngineRuntime,
) (eventproc.EventWaiter, error)

// registerWaiter is the shared lookup→build→start→insert path for both
// RegisterEvent and RegisterPersistentEvent.
//
// The lookup, create, start and insert run under ONE critical section so
// two concurrent registrations of the same eDef.ID() can't both miss the
// existence check and both create a waiter — the second insert would
// orphan the first waiter and its serving goroutine (audit 1.5 /
// FIX-003 C). The build func and AddEventProcessor never re-enter eh.m, and
// Service() only spawns a detached goroutine that touches eh.m no sooner
// than its first fire, so holding eh.m across them is safe.
func (eh *EventHub) registerWaiter(
	ep eventproc.EventProcessor,
	eDef flow.EventDefinition,
	build waiterBuilder,
) error {
	eh.m.Lock()
	defer eh.m.Unlock()

	if eh.getState() == hubStopped {
		return errs.New(
			errs.M("event hub is shut down; registration rejected"),
			errs.C(errorClass, errs.InvalidState),
			errs.D("event_definition_id", eDef.ID()))
	}

	if w, ok := eh.waiters[eDef.ID()]; ok {
		if err := w.AddEventProcessor(ep); err != nil {
			return errs.New(
				errs.M("couldn't add event processor to waiter"),
				errs.C(errorClass, errs.OperationFailed),
				errs.D("waiter_id", w.ID()),
				errs.D("event_definition_id", eDef.ID()),
				errs.D("event_definition_type", string(eDef.Type())),
				errs.D("event_processor_id", ep.ID()))
		}

		eh.rt.Logger().Debug("event registered (added to existing waiter)",
			"event_processor_id", ep.ID(),
			"event_definition_id", eDef.ID(),
			"event_definition_type", string(eDef.Type()),
			"waiter_id", w.ID())

		return nil
	}

	w, err := build(eh, ep, eDef, eh.rt)
	if err != nil {
		return errs.New(
			errs.M("eventWaiter building failed"),
			errs.C(errorClass, errs.BulidingFailed),
			errs.D("event_processor_id", ep.ID()),
			errs.D("event_definition_id", eDef.ID()),
			errs.E(err))
	}

	// Start the waiter BEFORE inserting it: a failed start never leaves a
	// dead, non-serving waiter in the map (no cleanup branch needed).
	if err := w.Service(eh.ctx); err != nil {
		return errs.New(
			errs.M("failed to start waiter service"),
			errs.C(errorClass, errs.OperationFailed),
			errs.D("waiter_id", w.ID()),
			errs.E(err))
	}

	eh.waiters[eDef.ID()] = w

	// A new signal waiter joins the name index (SRD-027 FR-6). The "added to existing
	// waiter" branch above creates no waiter, so it leaves the index untouched.
	if name, ok := signalName(eDef); ok {
		eh.signalIdx[name] = append(eh.signalIdx[name], w)
	}

	eh.rt.Logger().Debug("event registered (new waiter)",
		"event_processor_id", ep.ID(),
		"event_definition_id", eDef.ID(),
		"event_definition_type", string(eDef.Type()),
		"waiter_id", w.ID())

	return nil
}

// Shutdown stops every registered waiter and waits — bounded by ctx — for their
// service goroutines to exit, so none outlives the hub (ADR-006 v.1 §2.5,
// SRD-019). It marks the hub stopped (further registration is rejected) and
// removes every waiter from the registry even if its Stop returns an error, so a
// failed Stop never leaks the registry entry. Idempotent.
func (eh *EventHub) Shutdown(ctx context.Context) error {
	eh.m.Lock()
	if eh.getState() == hubStopped {
		eh.m.Unlock()

		return nil
	}

	eh.setState(hubStopped)

	ws := make([]eventproc.EventWaiter, 0, len(eh.waiters))
	for _, w := range eh.waiters {
		ws = append(ws, w)
	}
	// Remove all up front: the registry is clean regardless of any Stop error.
	eh.waiters = map[string]eventproc.EventWaiter{}
	eh.signalIdx = map[string][]eventproc.EventWaiter{}
	eh.m.Unlock()

	// Stop each waiter (logging — never aborting on — a failed Stop) and wait for
	// its service goroutine to exit via its Done channel, off the lock.
	var wg sync.WaitGroup

	for _, w := range ws {
		if err := w.Stop(); err != nil {
			eh.rt.Logger().Warn("event waiter Stop failed during shutdown",
				"waiter_id", w.ID(), "error", err.Error())
		}

		done := w.Done()
		if done == nil {
			continue // never serviced — no goroutine to drain
		}

		wg.Add(1)

		go func(d <-chan struct{}) {
			defer wg.Done()
			<-d
		}(done)
	}

	drained := make(chan struct{})

	go func() {
		wg.Wait()
		close(drained)
	}()

	select {
	case <-drained:
		return nil

	case <-ctx.Done():
		return errs.New(
			errs.M("event hub shutdown timed out before all waiters drained"),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(ctx.Err()))
	}
}

// UnregisterEvent removes the registered eventDefintions for single
// EventProcessor.
func (eh *EventHub) UnregisterEvent(
	ep eventproc.EventProcessor,
	eDefID string,
) error {
	if eh.getState() != hubStarted {
		return errs.New(
			errs.M("eventHub isn't started"),
			errs.C(errorClass, errs.InvalidState))
	}

	if ep == nil {
		return errs.New(
			errs.M("empty event processor isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	eh.m.RLock()
	w, ok := eh.waiters[eDefID]
	eh.m.RUnlock()

	if !ok {
		// ObjectNotFound (not InvalidParameter): a missing waiter is an
		// "already gone" condition the instance treats as idempotent —
		// the fired-timer path self-removes the waiter before the track
		// unregisters (FIX-003 B).
		return errs.New(
			errs.M("couldn't find waiter for the event definition"),
			errs.C(errorClass, errs.ObjectNotFound),
			errs.D("event_definition_id", eDefID))
	}

	if err := w.RemoveEventProcessor(ep); err != nil {
		return errs.New(
			errs.M("couldn't remove event processor from waiter"),
			errs.C(errorClass, errs.ObjectNotFound),
			errs.D("event_waiter_id", w.ID()),
			errs.D("event_processor_id", ep.ID()),
			errs.D("event_definition_id", eDefID),
			errs.E(err))
	}

	if len(w.EventProcessors()) == 0 {
		if w.State() == eventproc.WSRunned {
			if err := w.Stop(); err != nil {
				return errs.New(
					errs.M("waiter stop failed"),
					errs.C(errorClass, errs.OperationFailed),
					errs.D("waiter_id", w.ID()),
					errs.D("event_definition_idf", w.EventDefinition().ID()),
					errs.D("event_definition_type", string(w.EventDefinition().Type())))
			}
		}

		eh.rt.Logger().Debug("event unregistered (waiter stopped)",
			"event_processor_id", ep.ID(),
			"event_definition_id", eDefID,
			"waiter_id", w.ID())

		return eh.RemoveWaiter(w.EventDefinition().ID())
	}

	eh.rt.Logger().Debug("event unregistered (processor removed, waiter kept)",
		"event_processor_id", ep.ID(),
		"event_definition_id", eDefID,
		"waiter_id", w.ID())

	return nil
}

// PropagateEvent sends a fired throw event's eventDefinition
// up to chain of EventProducers.
//
// Since the eventHub is the last event producer in the chain
// it puts the event into event queue for further processing by
// the appropriate waiter.
func (eh *EventHub) PropagateEvent(
	_ context.Context,
	eDef flow.EventDefinition,
) error {
	if eh.getState() != hubStarted {
		return errs.New(
			errs.M("eventHub isn't started"),
			errs.C(errorClass, errs.InvalidState))
	}

	if eDef == nil {
		return errs.New(
			errs.M("EventHub.PropagateEvent: a nil EventDefinition isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	// A signal is a broadcast publication matched by NAME, not by the throw's
	// eDef.ID() (throw and catch are distinct nodes — ADR-006 v.1 §2.1, SRD-020):
	// fan out to every catcher of the same signal name.
	if eDef.Type() == flow.TriggerSignal {
		return eh.broadcastSignal(eDef)
	}

	eh.m.RLock()
	w, ok := eh.waiters[eDef.ID()]
	eh.m.RUnlock()

	if !ok {
		// ADR-006 §2.4: propagating to no registered waiter is a logged no-op,
		// not an error. A signal thrown with no live catcher is simply not
		// caught (BPMN §10.5.1); the hub is a live dispatcher, not a store, so
		// there is nothing to buffer and nothing to fail.
		eh.rt.Logger().Debug(
			"event propagated with no registered waiter; ignored (no-op)",
			"event_definition_id", eDef.ID(),
			"event_definition_type", string(eDef.Type()))

		return nil
	}

	if err := w.Process(eDef); err != nil {
		return errs.New(
			errs.M("event definition processing failed"),
			errs.C(errorClass, errs.OperationFailed),
			errs.D("waiter_id", w.ID()),
			errs.D("event_definition_id", eDef.ID()),
			errs.D("event_definition_type", string(eDef.Type())),
			errs.E(err))
	}

	if len(w.EventProcessors()) == 0 {
		return eh.RemoveWaiter(eDef.ID())
	}

	return nil
}

// broadcastSignal delivers a thrown signal to every registered catcher of the
// same signal name — the BPMN broadcast publication strategy (ADR-006 v.1 §2.1,
// §10.5.1). It matches by name (not eDef.ID(): throw and catch are distinct
// nodes) via the O(1) signal-name index (SRD-027 FR-6) instead of scanning the
// waiter registry. No catcher in reach is a logged no-op, not an error (§2.4).
// Each fired catcher self-unregisters as it resumes (track.ProcessEvent), so the
// hub removes the emptied waiters (and their index entries).
func (eh *EventHub) broadcastSignal(eDef flow.EventDefinition) error {
	name, ok := signalName(eDef)
	if !ok {
		return errs.New(
			errs.M("not a signal event definition"),
			errs.C(errorClass, errs.TypeCastingError),
			errs.D("event_definition_id", eDef.ID()))
	}

	eh.m.RLock()
	targets := append([]eventproc.EventWaiter(nil), eh.signalIdx[name]...)
	eh.m.RUnlock()

	if len(targets) == 0 {
		eh.rt.Logger().Debug(
			"signal thrown with no catcher in reach; ignored (no-op)",
			"signal", name)

		return nil
	}

	eh.rt.Logger().Debug("signal broadcast",
		"signal", name,
		"catchers", len(targets))

	// Fan out off the lock: each delivery resumes a catcher's track, which
	// self-unregisters (track.ProcessEvent) — that removal needs eh.m. Process
	// is best-effort and logs per catcher, so it returns nil today; the
	// defensive branch keeps a would-be future error visible instead of
	// silent, without stopping the broadcast (it must reach every catcher —
	// FIX-007). ADR-022 v.1 §2.3(2).
	for _, w := range targets {
		if err := w.Process(eDef); err != nil {
			eh.rt.Logger().Debug("signal waiter delivery returned an error",
				"signal", name, "waiter_id", w.ID(), "error", err.Error())
		}
	}

	return nil
}

// signalName returns the signal name of a SignalEventDefinition, or ("", false)
// for any other event definition.
func signalName(eDef flow.EventDefinition) (string, bool) {
	sig, ok := eDef.(*events.SignalEventDefinition)
	if !ok || sig.Signal() == nil {
		return "", false
	}

	return sig.Signal().Name(), true
}

// RemoveWaiter removes the waiter registered for eDefID from the
// EventHub waiter's list.
func (eh *EventHub) RemoveWaiter(eDefID string) error {
	eDefID = strings.TrimSpace(eDefID)
	if eDefID == "" {
		return errs.New(
			errs.M("event definition id is empty"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	eh.m.Lock()
	defer eh.m.Unlock()

	w, ok := eh.waiters[eDefID]
	if !ok {
		return errs.New(
			errs.M("waiter isn't found"),
			errs.C(errorClass, errs.ObjectNotFound),
			errs.D("event_definition_id", eDefID))
	}

	eh.removeWaiterFromIndex(w)
	delete(eh.waiters, eDefID)

	return nil
}

// removeWaiterFromIndex drops a signal waiter from signalIdx in step with its removal from the
// registry (SRD-027 FR-6); a non-signal waiter is a no-op. Called under eh.m by every path that
// deletes from eh.waiters, so the name index never outlives the registry.
func (eh *EventHub) removeWaiterFromIndex(w eventproc.EventWaiter) {
	if name, ok := signalName(w.EventDefinition()); ok {
		eh.signalIdxRemove(name, w)
	}
}

// signalIdxRemove drops waiter w from signalIdx[name], removing the name key entirely when
// its last waiter goes so no empty slice lingers (SRD-027 FR-6). Called under eh.m.
func (eh *EventHub) signalIdxRemove(name string, w eventproc.EventWaiter) {
	ws := eh.signalIdx[name]

	for i, x := range ws {
		if x == w {
			ws = append(ws[:i], ws[i+1:]...)

			break
		}
	}

	if len(ws) == 0 {
		delete(eh.signalIdx, name)

		return
	}

	eh.signalIdx[name] = ws
}

// SignalCatchers reports how many catch processors are currently subscribed
// for the signal name — a waiter carrying several processors (a second
// instance of the same shared-id catch joins the existing waiter rather than
// creating one) contributes each of them. It is a readiness probe for tests
// (FIX-021): a catcher's token parks BEFORE its hub registration runs, so an
// observed parked token alone does not mean a thrown signal already has a
// catcher. Deliberately NOT part of the eventproc.EventHub contract — callers
// reach it via a type assertion on the concrete hub.
func (eh *EventHub) SignalCatchers(name string) int {
	eh.m.RLock()
	defer eh.m.RUnlock()

	total := 0

	for _, w := range eh.signalIdx[name] {
		if pc, ok := w.(interface{ ProcessorCount() int }); ok {
			total += pc.ProcessorCount()

			continue
		}

		total++
	}

	return total
}

// WaiterFired reports that the waiter for eDefID has fired. The EventHub is the
// sole owner of waiter removal (ADR-006 v.1 §2.5): it removes the waiter iff it
// has reached a terminal state (Ended/Failed) and keeps a still-running one (a
// persistent message waiter, or a timer mid-cycle). A waiter never removes
// itself — it sets its own state and reports here.
func (eh *EventHub) WaiterFired(eDefID string) error {
	eDefID = strings.TrimSpace(eDefID)
	if eDefID == "" {
		return errs.New(
			errs.M("event definition id is empty"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	eh.m.Lock()
	defer eh.m.Unlock()

	w, ok := eh.waiters[eDefID]
	if !ok {
		return errs.New(
			errs.M("waiter isn't found"),
			errs.C(errorClass, errs.ObjectNotFound),
			errs.D("event_definition_id", eDefID))
	}

	switch w.State() {
	case eventproc.WSEnded, eventproc.WSFailed:
		eh.removeWaiterFromIndex(w)
		delete(eh.waiters, eDefID)
	}

	return nil
}

// AddEventKey extends the broker subscription of the waiter registered for
// eDefID with correlation key (SRD-017 §4.5 lazy association): a parked
// in-instance message receiver becomes reachable by a key its instance learned
// after it parked. A missing waiter (the receiver isn't parked) and a
// non-keyable waiter (a timer, with no keyed subscription) are benign no-ops.
func (eh *EventHub) AddEventKey(eDefID, key string) error {
	eDefID = strings.TrimSpace(eDefID)
	if eDefID == "" {
		return errs.New(
			errs.M("event definition id is empty"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	eh.m.RLock()
	w, ok := eh.waiters[eDefID]
	eh.m.RUnlock()

	if !ok {
		return nil
	}

	ka, ok := w.(interface {
		AddKey(string) error
	})
	if !ok {
		return nil
	}

	return ka.AddKey(key)
}

// ----------------------------------------------------------------------------

// interfaces check
var _ eventproc.EventProducer = (*EventHub)(nil)
