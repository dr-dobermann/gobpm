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
	"reflect"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/internal/eventproc/eventhub/waiters"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/observability"
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

// reportEventFlow announces an event-definition's flow through the hub
// (SRD-041 §3.4 EventFlow): registration, delivery, drop, or unregistration.
// The event_definition_id (and waiter/signal identity, where the caller has it)
// travels in details; the hub's failure logs stay Logger() diagnostics.
func (eh *EventHub) reportEventFlow(
	phase observability.Phase,
	details map[string]string,
) {
	eh.rt.Reporter().Report(observability.Fact{
		Kind:    observability.KindEventFlow,
		Phase:   phase,
		Details: details,
	})
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

	eh.rt.Reporter().Report(observability.Fact{
		Kind:  observability.KindHubState,
		Phase: observability.PhaseStarted,
	})

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

	if err := checkProcessor(ep, "RegisterEvent"); err != nil {
		return err
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

	if err := checkProcessor(ep, "RegisterPersistentEvent"); err != nil {
		return err
	}

	if eDef == nil {
		return errs.New(
			errs.M("EventHub.RegisterPersistentEvent: a nil EventDefinition "+
				"isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	return eh.registerWaiter(ep, eDef, waiters.CreatePersistentWaiter)
}

// checkProcessor validates the processor a registration offers, at the one
// boundary a host reaches: EventProcessor is a PUBLIC contract (pkg/eventproc),
// so anything can implement it.
//
// Beyond the nil check it requires the processor's dynamic type to be
// COMPARABLE. A waiter identifies its processors by value — slices.Index over
// the interface — which is what makes a repeated registration idempotent and
// lets one processor unregister without disturbing another. Identity by ID()
// could not replace it: a snapshot clone preserves element ids, so two
// instances of one process registering the same catch node present two
// distinct processors carrying the SAME id, and matching on it would let one
// instance unregister the other's wait.
//
// Go does not report false when two interface values of one uncomparable
// dynamic type meet — it panics. A host implementing EventProcessor on a
// struct with a slice or map field would therefore crash the hub on its SECOND
// registration for a definition, inside a waiter, far from the call that
// caused it. Refusing it here names the type and says what to do instead.
func checkProcessor(ep eventproc.EventProcessor, method string) error {
	if ep == nil {
		return errs.New(
			errs.M("empty event processor isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if t := reflect.TypeOf(ep); !t.Comparable() {
		return errs.New(
			errs.M("EventHub.%s: an EventProcessor of the uncomparable type "+
				"%s isn't allowed — a waiter identifies its processors by "+
				"value; register a pointer to it instead", method, t),
			errs.C(errorClass, errs.InvalidParameter))
	}

	return nil
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
// The lookup and the insert run under ONE critical section so two concurrent
// registrations of the same eDef.ID() can't both miss the existence check and
// both create a waiter — the second insert would orphan the first waiter and
// its serving goroutine (audit 1.5 / FIX-003 C).
//
// What may run under eh.m is decided by ONE property: whether the call is
// FOREIGN — whether it can reach the host (a broker, a processor, a logger the
// host supplied). Foreign work does not run under the engine's one lock, no
// matter how briefly it usually takes, because "usually" is a property of the
// host's network and not of this engine (FIX-038 §1.1).
//
// Re-entrancy is a WEAKER test and answering it is not enough. AddEventProcessor
// still never re-enters eh.m — that is why the comment this replaces read as
// true for as long as it did — but it was for a while allowed to call the host's
// broker, and holding eh.m across that stalled every registration, lookup and
// unregistration in the engine behind one host call (FIX-041 §1.1). So: before
// adding a call inside this lock, ask what it can reach, not what it can
// re-enter.
//
// Under the lock: the registry map, the signal index, AddEventProcessor.
// Outside it: build, Service, Stop, ApplyProcessorKeys.
func (eh *EventHub) registerWaiter(
	ep eventproc.EventProcessor,
	eDef flow.EventDefinition,
	build waiterBuilder,
) error {
	// FAST PATH — an existing waiter just gains a processor. The registry half
	// runs under the lock; the joining processor's correlation keys reach the
	// broker after it is released.
	joined, w, err := eh.joinExistingWaiter(ep, eDef)
	if err != nil {
		return err
	}

	if joined {
		if aerr := eh.applyProcessorKeys(w, ep); aerr != nil {
			return aerr
		}

		eh.reportEventFlow(observability.PhaseRegistered, map[string]string{
			observability.AttrEventDefinitionID: eDef.ID(),
			observability.AttrWaiterID:          w.ID(),
		})

		return nil
	}

	// SLOW PATH — building and starting a waiter is FOREIGN work: a message
	// waiter's Service subscribes to the host's broker, which may be remote and
	// may block. Holding the hub's one lock across it stalled every
	// registration, unregistration and lookup in the engine behind one host
	// call (FIX-038 §1.1). It happens here, unlocked.
	w, err = build(eh, ep, eDef, eh.rt)
	if err != nil {
		return errs.New(
			errs.M("eventWaiter building failed"),
			errs.C(errorClass, errs.BulidingFailed),
			errs.D(observability.AttrEventProcessorID, ep.ID()),
			errs.D(observability.AttrEventDefinitionID, eDef.ID()),
			errs.E(err))
	}

	// Start BEFORE inserting: a failed start never leaves a dead, non-serving
	// waiter in the map (no cleanup branch needed).
	if err := w.Service(eh.ctx); err != nil {
		return errs.New(
			errs.M("failed to start waiter service"),
			errs.C(errorClass, errs.OperationFailed),
			errs.D(observability.AttrWaiterID, w.ID()),
			errs.E(err))
	}

	return eh.publishWaiter(ep, eDef, w)
}

// applyProcessorKeys is the foreign half of a join: it hands the joining
// processor's correlation keys to the waiter's broker subscription, with eh.m
// RELEASED. A waiter with no keyed subscription (signal, timer) does not
// implement KeyedWaiter and is a no-op.
//
// It runs AFTER the registration, and that order is deliberate. The window it
// leaves — processor listed, key not yet subscribed — drops nothing: the broker
// routes no envelope for a key it has not been given, and an unmatched envelope
// waits in its inbox until a subscription wants it (ADR-006 §2.4). It also
// means a concurrent UnregisterEvent SEES the processor, so it cannot tear the
// waiter down while this registration is still attaching to it — the stranding
// FIX-038 §1.3 fixed, which applying keys before registering would reopen.
//
// A failure undoes the registration: the caller reports a failed registration,
// and a processor listed against a waiter nobody registered it with would
// receive events the caller believes it is not subscribed to.
func (eh *EventHub) applyProcessorKeys(
	w eventproc.EventWaiter, ep eventproc.EventProcessor,
) error {
	kw, ok := w.(eventproc.KeyedWaiter)
	if !ok {
		return nil
	}

	err := kw.ApplyProcessorKeys(ep)
	if err == nil {
		return nil
	}

	eh.undoFailedJoin(w, ep)

	return errs.New(
		errs.M("couldn't apply the correlation keys of a joining processor"),
		errs.C(errorClass, errs.OperationFailed),
		errs.D(observability.AttrWaiterID, w.ID()),
		errs.D(observability.AttrEventProcessorID, ep.ID()),
		errs.E(err))
}

// undoFailedJoin unregisters ep, and buries the waiter only if the failed key
// actually killed it.
//
// Both outcomes are real. A key refused after an earlier one landed leaves a
// key-set the port cannot repair — there is no RemoveKey — so the waiter sheds
// the whole subscription and is DEAD (FIX-041 §3.1 D2). Left mapped it would be
// joined by every later registration for the same definition and deliver to none
// of them, which is FIX-038 §1.3's stranding arrived at from the other
// direction; the processors already parked on it lose their registration, which
// is the cost §3.2 B3 accepts and the reason this logs at Error.
//
// But a key refused as the FIRST of the join's set changes nothing: the
// subscription is exactly as it was, the waiter keeps serving everyone parked
// on it, and only this join is turned away. Unmapping it there would tear down
// a healthy waiter — and every other processor's subscription with it — to
// punish a registration that failed harmlessly.
//
// So the waiter's own state decides, not the fact that an error came back.
func (eh *EventHub) undoFailedJoin(
	w eventproc.EventWaiter, ep eventproc.EventProcessor,
) {
	if rerr := w.RemoveEventProcessor(ep); rerr != nil {
		eh.rt.Logger().Debug("couldn't unregister a processor whose keys failed",
			observability.AttrWaiterID, w.ID(),
			observability.AttrEventProcessorID, ep.ID(),
			observability.AttrError, rerr.Error())
	}

	if w.State() != eventproc.WSFailed {
		return // the join failed; the waiter did not
	}

	eDefID := w.EventDefinition().ID()

	eh.m.Lock()

	// Only if it is still THIS waiter under that id: a concurrent
	// registration may already have replaced it, and unmapping the
	// replacement would strand a live one to bury a dead one.
	if mapped, ok := eh.waiters[eDefID]; ok && mapped == w {
		eh.dropWaiterLocked(eDefID, w)
	}

	eh.m.Unlock()

	eh.rt.Logger().Error("a refused correlation key failed a message waiter",
		observability.AttrWaiterID, w.ID(),
		observability.AttrEventDefinitionID, eDefID,
		observability.AttrEventProcessorID, ep.ID(),
		"orphaned_processors", len(w.EventProcessors()))
}

// joinExistingWaiter adds ep to the waiter already serving eDef, if there is
// one. Registry work only, so it runs under eh.m.
func (eh *EventHub) joinExistingWaiter(
	ep eventproc.EventProcessor, eDef flow.EventDefinition,
) (joined bool, w eventproc.EventWaiter, err error) {
	eh.m.Lock()
	defer eh.m.Unlock()

	if eh.getState() == hubStopped {
		return false, nil, errs.New(
			errs.M("event hub is shut down; registration rejected"),
			errs.C(errorClass, errs.InvalidState),
			errs.D(observability.AttrEventDefinitionID, eDef.ID()))
	}

	w, ok := eh.waiters[eDef.ID()]
	if !ok {
		return false, nil, nil
	}

	if err := w.AddEventProcessor(ep); err != nil {
		return false, nil, errs.New(
			errs.M("couldn't add event processor to waiter"),
			errs.C(errorClass, errs.OperationFailed),
			errs.D(observability.AttrWaiterID, w.ID()),
			errs.D(observability.AttrEventDefinitionID, eDef.ID()),
			errs.D(observability.AttrEventDefinitionType, string(eDef.Type())),
			errs.D(observability.AttrEventProcessorID, ep.ID()))
	}

	return true, w, nil
}

// publishWaiter installs a started waiter, or — if another registration won the
// race while this one was building outside the lock — joins that winner and
// stops the loser. Stopping happens AFTER the lock is released: a message
// waiter's Stop unsubscribes from the host broker, which is the same foreign
// call the build path must not hold the lock across.
func (eh *EventHub) publishWaiter(
	ep eventproc.EventProcessor, eDef flow.EventDefinition, w eventproc.EventWaiter,
) error {
	eh.m.Lock()

	stopped := eh.getState() == hubStopped
	winner, lost := eh.waiters[eDef.ID()]

	var addErr error

	switch {
	case stopped:
		// nothing to install into

	case lost:
		addErr = winner.AddEventProcessor(ep)

	default:
		eh.waiters[eDef.ID()] = w

		// A new signal waiter joins the name index (SRD-027 FR-6); the join
		// paths create no waiter and leave the index untouched.
		if name, ok := signalName(eDef); ok {
			eh.signalIdx[name] = append(eh.signalIdx[name], w)
		}
	}

	eh.m.Unlock()

	if stopped || lost {
		eh.stopUnusedWaiter(w)
	}

	switch {
	case stopped:
		return errs.New(
			errs.M("event hub is shut down; registration rejected"),
			errs.C(errorClass, errs.InvalidState),
			errs.D(observability.AttrEventDefinitionID, eDef.ID()))

	case addErr != nil:
		return errs.New(
			errs.M("couldn't add event processor to waiter"),
			errs.C(errorClass, errs.OperationFailed),
			errs.D(observability.AttrEventDefinitionID, eDef.ID()),
			errs.D(observability.AttrEventProcessorID, ep.ID()),
			errs.E(addErr))
	}

	// The losing path joined the winner above, under the lock. Its foreign half
	// belongs here, for the same reason as the fast path's.
	if lost {
		if err := eh.applyProcessorKeys(winner, ep); err != nil {
			return err
		}
	}

	// Name the waiter the processor actually landed on. On the losing path
	// that is the WINNER: w was stopped and discarded a few lines up, so
	// reporting it points an operator at a waiter that no longer exists.
	served := w
	if lost {
		served = winner
	}

	eh.reportEventFlow(observability.PhaseRegistered, map[string]string{
		observability.AttrEventDefinitionID: eDef.ID(),
		observability.AttrWaiterID:          served.ID(),
	})

	return nil
}

// stopUnusedWaiter tears down a waiter this registration built but did not
// install. A failure is a Debug fact: nothing references it, so there is
// nothing a caller could do.
func (eh *EventHub) stopUnusedWaiter(w eventproc.EventWaiter) {
	if err := w.Stop(); err != nil {
		eh.rt.Logger().Debug("stopping an uninstalled waiter",
			observability.AttrWaiterID, w.ID(),
			observability.AttrError, err.Error())
	}
}

// Shutdown stops every registered waiter and waits — bounded by ctx — for their
// service goroutines to exit, so none outlives the hub (ADR-006 §2.5,
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

	// Report AFTER the unlock: Reporter() is the engine's producer, which fans
	// the fact out to HOST observers and through the host's log redactor. Under
	// the hub lock it was the same shape §1.1 removes from the registration
	// path — an embedder's code deciding how long the hub stays locked.
	eh.rt.Reporter().Report(observability.Fact{
		Kind:  observability.KindHubState,
		Phase: observability.PhaseStopped,
	})

	// Stop each waiter (logging — never aborting on — a failed Stop) and wait for
	// its service goroutine to exit via its Done channel, off the lock.
	var wg sync.WaitGroup

	for _, w := range ws {
		if err := w.Stop(); err != nil {
			eh.rt.Logger().Warn("event waiter Stop failed during shutdown",
				observability.AttrWaiterID, w.ID(), observability.AttrError, err.Error())
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

	// The emptiness check and the removal must be ONE critical section. Read
	// under RLock, release, then check-and-remove let a concurrent
	// registerWaiter find this waiter still mapped and attach a processor to
	// it — after which this call stopped and unmapped it, and that
	// registration's events never arrived, with no error anywhere
	// (FIX-038 §1.3).
	eh.m.Lock()

	w, ok := eh.waiters[eDefID]
	if !ok {
		eh.m.Unlock()

		// ObjectNotFound (not InvalidParameter): a missing waiter is an
		// "already gone" condition the instance treats as idempotent —
		// the fired-timer path self-removes the waiter before the track
		// unregisters (FIX-003 B).
		return errs.New(
			errs.M("couldn't find waiter for the event definition"),
			errs.C(errorClass, errs.ObjectNotFound),
			errs.D(observability.AttrEventDefinitionID, eDefID))
	}

	if err := w.RemoveEventProcessor(ep); err != nil {
		eh.m.Unlock()

		return errs.New(
			errs.M("couldn't remove event processor from waiter"),
			errs.C(errorClass, errs.ObjectNotFound),
			errs.D(observability.AttrWaiterID, w.ID()),
			errs.D(observability.AttrEventProcessorID, ep.ID()),
			errs.D(observability.AttrEventDefinitionID, eDefID),
			errs.E(err))
	}

	last := len(w.EventProcessors()) == 0
	if last {
		// Remove it here, under the same lock that observed it empty.
		eh.dropWaiterLocked(eDefID, w)
	}

	eh.m.Unlock()

	if !last {
		return nil
	}

	// Stop OUTSIDE the lock: a message waiter's Stop unsubscribes from the
	// host broker, which must never run under the hub lock (FIX-038 §1.1).
	if w.State() == eventproc.WSRunned {
		if err := w.Stop(); err != nil {
			// A waiter that will not stop is still serving its subscription,
			// so leaving it unmapped strands it: the next registration for
			// this definition builds a SECOND waiter and subscribes again.
			// Put it back — only if the key is still free, since a
			// registration may have installed its own by now — which makes
			// the failed unregistration atomic: nothing happened.
			eh.remapUnstopped(eDefID, w)

			return errs.New(
				errs.M("waiter stop failed"),
				errs.C(errorClass, errs.OperationFailed),
				errs.D(observability.AttrWaiterID, w.ID()),
				errs.D(observability.AttrEventDefinitionID, w.EventDefinition().ID()),
				errs.D(observability.AttrEventDefinitionType, string(w.EventDefinition().Type())))
		}
	}

	eh.reportEventFlow(observability.PhaseUnregistered, map[string]string{
		observability.AttrEventDefinitionID: eDefID,
		observability.AttrWaiterID:          w.ID(),
	})

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
	// eDef.ID() (throw and catch are distinct nodes — ADR-006 §2.1, SRD-020):
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
		eh.reportEventFlow(observability.PhaseDropped, map[string]string{
			observability.AttrEventDefinitionID: eDef.ID(),
		})

		return nil
	}

	if err := w.Process(eDef); err != nil {
		return errs.New(
			errs.M("event definition processing failed"),
			errs.C(errorClass, errs.OperationFailed),
			errs.D(observability.AttrWaiterID, w.ID()),
			errs.D(observability.AttrEventDefinitionID, eDef.ID()),
			errs.D(observability.AttrEventDefinitionType, string(eDef.Type())),
			errs.E(err))
	}

	// A message routed to its waiter is observed downstream as the receiving
	// instance's Correlation fact (match/mismatch) — not as a hub EventFlow, so
	// one delivery is not double-recorded. EventFlow/Delivered is reserved for a
	// signal broadcast (below), which has no single instance to correlate.

	if len(w.EventProcessors()) == 0 {
		return eh.RemoveWaiter(eDef.ID())
	}

	return nil
}

// broadcastSignal delivers a thrown signal to every registered catcher of the
// same signal name — the BPMN broadcast publication strategy (ADR-006 §2.1,
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
			errs.D(observability.AttrEventDefinitionID, eDef.ID()))
	}

	eh.m.RLock()
	targets := append([]eventproc.EventWaiter(nil), eh.signalIdx[name]...)
	eh.m.RUnlock()

	if len(targets) == 0 {
		eh.reportEventFlow(observability.PhaseDropped, map[string]string{
			observability.AttrSignal: name,
		})

		return nil
	}

	eh.reportEventFlow(observability.PhaseDelivered, map[string]string{
		observability.AttrSignal: name,
	})

	// Fan out off the lock: each delivery resumes a catcher's track, which
	// self-unregisters (track.ProcessEvent) — that removal needs eh.m. Process
	// is best-effort and logs per catcher, so it returns nil today; the
	// defensive branch keeps a would-be future error visible instead of
	// silent, without stopping the broadcast (it must reach every catcher —
	// FIX-007). ADR-022 §2.3(2).
	for _, w := range targets {
		if err := w.Process(eDef); err != nil {
			eh.rt.Logger().Debug("signal waiter delivery returned an error",
				observability.AttrSignal, name, observability.AttrWaiterID, w.ID(), observability.AttrError, err.Error())
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
			errs.D(observability.AttrEventDefinitionID, eDefID))
	}

	eh.dropWaiterLocked(eDefID, w)

	return nil
}

// dropWaiterLocked takes a waiter the caller has ALREADY found under eh.m out
// of the registry and its name index. It cannot fail, which is the point:
// UnregisterEvent's emptiness check and this removal must happen under ONE
// acquisition, or a concurrent registration slips between them and attaches a
// processor to a waiter this call is about to stop and unmap (FIX-038 §1.3).
// Caller holds eh.m.
func (eh *EventHub) dropWaiterLocked(eDefID string, w eventproc.EventWaiter) {
	eh.removeWaiterFromIndex(w)
	delete(eh.waiters, eDefID)
}

// remapUnstopped puts back a waiter whose Stop failed, so a failed
// unregistration leaves the registry as it found it rather than stranding a
// live subscription outside the map. It restores nothing if the key is taken:
// a registration that installed its own waiter in the meantime owns the
// definition now, and overwriting it would strand THAT one instead.
func (eh *EventHub) remapUnstopped(eDefID string, w eventproc.EventWaiter) {
	eh.m.Lock()
	defer eh.m.Unlock()

	if _, taken := eh.waiters[eDefID]; taken {
		return
	}

	eh.waiters[eDefID] = w

	if name, ok := signalName(w.EventDefinition()); ok {
		eh.signalIdx[name] = append(eh.signalIdx[name], w)
	}
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
// sole owner of waiter removal (ADR-006 §2.5): it removes the waiter iff it
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
			errs.D(observability.AttrEventDefinitionID, eDefID))
	}

	eh.reportEventFlow(observability.PhaseFired, map[string]string{
		observability.AttrEventDefinitionID: eDefID,
		observability.AttrWaiterID:          w.ID(),
	})

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

	kw, ok := w.(eventproc.KeyedWaiter)
	if !ok {
		return nil
	}

	// Outside the lock, released above: AddKey reaches the host's broker.
	return kw.AddKey(key)
}

// ----------------------------------------------------------------------------

// interfaces check
var _ eventproc.EventProducer = (*EventHub)(nil)
