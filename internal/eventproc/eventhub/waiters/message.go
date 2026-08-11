package waiters

import (
	"context"
	"errors"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"slices"
	"strings"
	"sync"

	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/messaging"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/renv"
)

// MessageWaiterError classifies messageWaiter failures.
const MessageWaiterError = "MESSAGE_WAITER_ERROR"

// messageWaiter bridges the MessageBroker to the EventHub (ADR-014 v.1): it
// subscribes the broker for its message name and, on a matching envelope, fires
// the event (carrying the payload) to the registered processors and reports the
// fire to the hub. It never removes itself — the EventHub is the sole remover
// (ADR-006 v.1 §2.5). The waiter is a pure forwarder: it hands every
// coarse-matched (broker keyed-routed) envelope to its processors and never
// self-terminates on a fire. An in-instance receiver's waiter is removed when
// its track consumes the event and unregisters (the hub drops the emptied
// waiter); the instance-starter's processor (SRD-015) never unregisters, so its
// waiter keeps firing for every matching message until Stop. Fine correlation is
// the receiving instance loop's job, not the waiter's (ADR-017 v.1 §2): the loop
// runs the correlation gate and drops a mismatch, keeping its track parked.
type messageWaiter struct {
	hub        eventproc.EventHub
	rt         renv.EngineRuntime
	eDef       *events.MessageEventDefinition
	stopCh     chan struct{}
	done       chan struct{}
	sub        messaging.Subscription
	name       string
	id         string
	processors []eventproc.EventProcessor
	// pendingKeys holds correlation keys learned BEFORE the broker
	// subscription exists. They used to be dropped, which lost a
	// Multi-Instance iteration's key whenever it was derived while a
	// sibling's registration was still inside Subscribe — the envelope
	// then matched no subscription and waited in the broker's inbox
	// forever (#320).
	pendingKeys []string
	state       eventproc.EventWaiterState
	m           sync.Mutex
}

// AddKey extends the waiter's broker subscription with key (SRD-017 §4.5 lazy
// association).
//
// Before Service has subscribed the key is BUFFERED, not discarded. v.1
// returned nil there, reasoning that the receiver would pick the key up from
// its instance's grown key-set when it did subscribe — true only when the key
// reaches the instance before Service reads it. A Multi-Instance iteration
// that derived its key while a sibling's registration was inside Subscribe
// lost it silently, and its envelope then matched no subscription and waited
// in the broker's inbox forever (#320).
func (mw *messageWaiter) AddKey(key string) error {
	mw.m.Lock()

	if mw.sub == nil {
		mw.pendingKeys = append(mw.pendingKeys, key)
		mw.m.Unlock()

		return nil
	}

	sub := mw.sub
	mw.m.Unlock()

	// outside the lock: the subscription belongs to the host's broker, and
	// the engine does not hold its own lock across a host call.
	return sub.AddKey(key)
}

// NewMessageWaiter builds a messageWaiter for a MessageEventDefinition. It
// rejects empty dependencies and a non-message event definition. The waiter
// forwards every matching message to its processors; its lifecycle is
// processor-driven (removed when its only processor — an in-instance track —
// unregisters on consume; kept alive while a non-unregistering instance-starter
// processor stays subscribed), not selected by a flag.
func NewMessageWaiter(
	eh eventproc.EventHub,
	ep eventproc.EventProcessor,
	eDefI flow.EventDefinition,
	id string,
	rt renv.EngineRuntime,
) (eventproc.EventWaiter, error) {
	if ep == nil || eDefI == nil || eh == nil || rt == nil {
		return nil,
			errs.New(
				errs.M("couldn't create a Waiter with empty EventProcessor, "+
					"EventDefinition, EventHub or EngineRuntime"),
				errs.C(MessageWaiterError,
					errs.InvalidParameter, errs.EmptyNotAllowed))
	}

	eDef, ok := eDefI.(*events.MessageEventDefinition)
	if !ok {
		return nil,
			errs.New(
				errs.M("not a MessageEventDefinition"),
				errs.C(MessageWaiterError, errs.TypeCastingError),
				errs.D(observability.AttrEventDefinitionType, string(eDefI.Type())))
	}

	msg := eDef.Message()
	if msg == nil {
		return nil,
			errs.New(
				errs.M("MessageEventDefinition has no message"),
				errs.C(MessageWaiterError, errs.EmptyNotAllowed),
				errs.D(observability.AttrEventDefinitionID, eDef.ID()))
	}

	id = strings.TrimSpace(id)
	if id == "" {
		id = foundation.GenerateID()
	}

	return &messageWaiter{
		id:         id,
		name:       msg.Name(),
		eDef:       eDef,
		hub:        eh,
		rt:         rt,
		processors: []eventproc.EventProcessor{ep},
		state:      eventproc.WSReady,
	}, nil
}

// ID returns the waiter id.
func (mw *messageWaiter) ID() string {
	return mw.id
}

// EventDefinition returns the message event definition the waiter waits for.
func (mw *messageWaiter) EventDefinition() flow.EventDefinition {
	return mw.eDef
}

// AddEventProcessor adds ep to the waiter's processor list (idempotent).
//
// Registry work ONLY — it reaches neither the processor nor the broker, so the
// EventHub may (and does) call it under its own lock. The joining processor's
// correlation keys are applied by ApplyProcessorKeys once that lock is
// released: v.1 of the #320 fix applied them here, which put a host-broker call
// inside the engine-wide hub lock and stalled every registration, lookup and
// unregistration behind it — FIX-038 §1.1's defect, reintroduced (FIX-041 §1.1).
//
// Guarded by TestJoinDoesNotHoldTheHubLock.
func (mw *messageWaiter) AddEventProcessor(ep eventproc.EventProcessor) error {
	if ep == nil {
		return errs.New(
			errs.M("empty EventProcessor isn't allowed"),
			errs.C(MessageWaiterError, errs.EmptyNotAllowed))
	}

	mw.m.Lock()
	defer mw.m.Unlock()

	if idx := slices.Index(mw.processors, ep); idx == -1 {
		mw.processors = append(mw.processors, ep)
	}

	return nil
}

// ApplyProcessorKeys applies ep's correlation keys to the waiter's broker
// subscription — the foreign half of a join, run by the EventHub after it has
// released its own lock.
//
// A JOINING processor brings its own keys, and the subscription was created
// from the keys of whoever registered first. Without this step a Multi-Instance
// iteration that joins an existing waiter is unreachable by its own key: its
// envelope matches no subscription and waits in the broker's inbox (#320). The
// lazy AddEventKey path cannot be relied on for it — that one silently no-ops
// while the waiter is not yet in the hub's map.
//
// The order the hub uses — register ep, THEN apply its keys — is the safe one.
// Its window (processor listed, key not yet subscribed) drops nothing: the
// broker routes no envelope for a key it has not been given, and an unmatched
// envelope waits in its inbox until a subscription wants it (ADR-006 v.5 §2.4).
// The inverse window (key subscribed, processor not listed) consumes and
// discards.
//
// Guarded by TestJoinAppliesKeysAfterRegistering and
// TestPartialKeyFailureDiscardsTheSubscription.
func (mw *messageWaiter) ApplyProcessorKeys(ep eventproc.EventProcessor) error {
	if ep == nil {
		return errs.New(
			errs.M("empty EventProcessor isn't allowed"),
			errs.C(MessageWaiterError, errs.EmptyNotAllowed))
	}

	// Outside the lock: CorrelationKeys is a call into a processor the host
	// may have supplied, and the engine does not hold its own lock across
	// another component's call (FIX-038 §1.1).
	keys := processorKeys(ep)
	if len(keys) == 0 {
		return nil
	}

	mw.m.Lock()

	// No subscription yet — BUFFER, do not skip. It is tempting to reason
	// that a registered processor is in Service's snapshot and will have its
	// keys read there, but that is false for exactly the window #320 was lost
	// in: a processor joining after Service snapshots its processors and
	// before it publishes mw.sub is in neither, and its key would vanish as
	// before. The duplicate this can produce is Service's to remove.
	if mw.sub == nil {
		mw.pendingKeys = append(mw.pendingKeys, keys...)
		mw.m.Unlock()

		return nil
	}

	sub := mw.sub
	mw.m.Unlock()

	for _, k := range keys {
		if err := sub.AddKey(k); err != nil {
			return mw.discardSubscription(sub, err,
				"couldn't extend the subscription for a joining processor")
		}
	}

	return nil
}

// discardSubscription drops the whole broker subscription after a key could not
// be applied, marks the waiter failed and wraps err.
//
// Discarding is the only repair available: messaging.Subscription can grow a
// key-set but not shrink one, so a partly-applied set cannot be undone in place
// (FIX-041 §3.1 D2). It is also a safe one — the messages the discarded keys
// would have matched go back to waiting in the broker's inbox for a
// subscription that wants them (ADR-006 v.5 §2.4), whereas an orphan key left
// on a live subscription eats them permanently, which is the #320 failure mode
// made durable.
//
// This is not a waiter removing itself (ADR-006 v.5 §2.5): it drops its own
// subscription and marks its own state, exactly as Stop does. Removal from the
// registry stays the hub's, on the failed registration it is already returning.
func (mw *messageWaiter) discardSubscription(
	sub messaging.Subscription,
	cause error,
	msg string,
) error {
	// Fail the waiter BEFORE unsubscribing, and unsubscribe outside the lock
	// because it is a host call. The order is what makes the failure stick:
	// unsubscribing closes the channel the service goroutine is selecting on,
	// so it wakes immediately and relabels the waiter — and setStateUnlessFailed
	// can only refuse that relabel if WSFailed is already written. Reversed,
	// the final state is a coin toss between Failed and Stopped.
	mw.m.Lock()
	mw.sub = nil
	mw.state = eventproc.WSFailed
	mw.m.Unlock()

	uerr := sub.Unsubscribe()

	e := errs.New(
		errs.M(msg),
		errs.C(MessageWaiterError, errs.OperationFailed),
		errs.D(observability.AttrMessageName, mw.name),
		errs.D(observability.AttrWaiterID, mw.id),
		errs.E(cause))

	if uerr != nil {
		mw.rt.Logger().Error("couldn't unsubscribe a failed message waiter",
			observability.AttrWaiterID, mw.id,
			observability.AttrMessageName, mw.name,
			observability.AttrError, uerr.Error())
	}

	return e
}

// processorKeys reads the correlation keys a processor answers to, if it
// answers to any: a plain track has none, an instance carrying conversation
// or iteration keys has several.
func processorKeys(ep eventproc.EventProcessor) []string {
	kp, ok := ep.(eventproc.KeyedProcessor)
	if !ok {
		return nil
	}

	return kp.CorrelationKeys()
}

// RemoveEventProcessor removes ep from the waiter's processor list.
func (mw *messageWaiter) RemoveEventProcessor(ep eventproc.EventProcessor) error {
	if ep == nil {
		return errs.New(
			errs.M("empty EventProcessor isn't allowed"),
			errs.C(MessageWaiterError, errs.EmptyNotAllowed))
	}

	mw.m.Lock()
	defer mw.m.Unlock()

	idx := slices.Index(mw.processors, ep)
	if idx == -1 {
		return errs.New(
			errs.M("event processor isn't registered with the waiter"),
			errs.C(MessageWaiterError, errs.ObjectNotFound),
			errs.D(observability.AttrWaiterID, mw.id),
			errs.D(observability.AttrEventProcessorID, ep.ID()))
	}

	mw.processors = slices.Delete(mw.processors, idx, idx+1)

	return nil
}

// EventProcessors returns the waiter's registered processors.
func (mw *messageWaiter) EventProcessors() []eventproc.EventProcessor {
	mw.m.Lock()
	defer mw.m.Unlock()

	return mw.processors
}

// Process isn't used by the messageWaiter: messages arrive through the broker
// subscription, not through the EventHub propagation path.
func (mw *messageWaiter) Process(eDef flow.EventDefinition) error {
	return errs.New(
		errs.M("messageWaiter doesn't process propagated EventDefinitions"),
		errs.C(MessageWaiterError, errs.InvalidState),
		errs.D(observability.AttrEventDefinitionID, eDef.ID()),
		errs.D(observability.AttrEventDefinitionType, string(eDef.Type())))
}

// Service subscribes the broker for the waiter's message name and starts the
// delivery goroutine. The subscription is registered synchronously, so a
// message published after Service returns is delivered (subscribe-before-
// publish, ADR-006 v.1 §2.4).
//
// Every state / stopCh / done access below takes mw.m, like State, setState and
// Stop do. None of the writes is a demonstrated race — they all complete before
// the hub publishes the waiter — but a field guarded on three paths and bare on
// a fourth makes the next reader's reasoning wrong (FIX-041 §1.6).
func (mw *messageWaiter) Service(ctx context.Context) error {
	// Snapshot the keys known now — the processors' own, plus anything
	// buffered by AddKey or ApplyProcessorKeys before this point.
	mw.m.Lock()

	if mw.state != eventproc.WSReady {
		state := mw.state
		mw.m.Unlock()

		return errs.New(
			errs.M("waiter isn't ready to start"),
			errs.C(MessageWaiterError, errs.InvalidState),
			errs.D("current_state", state.String()))
	}

	consumed := len(mw.pendingKeys)
	procs := append([]eventproc.EventProcessor(nil), mw.processors...)
	keys := append([]string(nil), mw.pendingKeys...)
	mw.m.Unlock()

	// outside the lock, for the same reason as ApplyProcessorKeys
	for _, p := range procs {
		keys = append(keys, processorKeys(p)...)
	}

	// A joining processor's keys have two homes — the processor itself and the
	// buffer that carries them across the subscribe window — and a key that
	// arrived before Service reaches both. membroker collapses the duplicate
	// (its key-set is a map), but the port is host-implementable and an adapter
	// that turns each key into a queue-level registration would make two
	// (FIX-041 §1.5).
	keys = uniqueKeys(keys)

	sub, err := mw.rt.MessageBroker().Subscribe(ctx, mw.name, keys...)
	if err != nil {
		mw.setState(eventproc.WSFailed)

		return errs.New(
			errs.M("couldn't subscribe to the message broker"),
			errs.C(MessageWaiterError, errs.OperationFailed),
			errs.D(observability.AttrMessageName, mw.name),
			errs.E(err))
	}

	// Publishing the subscription and collecting whatever AddKey buffered
	// WHILE Subscribe was running is one step: after this, AddKey applies
	// directly and nothing more accumulates.
	mw.m.Lock()
	mw.sub = sub
	late := append([]string(nil), mw.pendingKeys[consumed:]...)
	mw.pendingKeys = nil
	mw.m.Unlock()

	for _, k := range late {
		if aerr := sub.AddKey(k); aerr != nil {
			// The subscription goes with it: some of the late keys are on it
			// and the port cannot take them off again (FIX-041 §3.1 D2).
			return mw.discardSubscription(sub, aerr,
				"couldn't apply a key learned during subscribe")
		}
	}

	mw.m.Lock()
	mw.state = eventproc.WSRunned
	mw.stopCh = make(chan struct{})
	mw.done = make(chan struct{})
	mw.m.Unlock()

	mw.rt.Logger().Debug("message waiter serviced",
		observability.AttrWaiterID, mw.id, observability.AttrMessageName, mw.name)

	go mw.runMessageService(ctx, sub)

	return nil
}

// uniqueKeys returns keys with duplicates removed, keeping first-seen order so
// a broker adapter that logs or indexes by position sees a stable list.
func uniqueKeys(keys []string) []string {
	if len(keys) < 2 {
		return keys
	}

	seen := make(map[string]struct{}, len(keys))
	unique := make([]string, 0, len(keys))

	for _, k := range keys {
		if _, ok := seen[k]; ok {
			continue
		}

		seen[k] = struct{}{}
		unique = append(unique, k)
	}

	return unique
}

// runMessageService waits for matching envelopes (or a stop/cancel) and
// forwards each one to the waiting node. The waiter never self-terminates on a
// fire (ADR-017 v.1 §2): it loops, forwarding every coarse-matched message,
// until the context is canceled, it is stopped, or the broker closes the
// subscription channel. An in-instance receiver's waiter is torn down by the
// hub when its track consumes the event and unregisters.
func (mw *messageWaiter) runMessageService(
	ctx context.Context,
	sub messaging.Subscription,
) {
	// Every exit path tears the broker subscription down: a stopped waiter that
	// stayed subscribed would keep claiming published messages into its abandoned
	// (buffered) channel, swallowing them away from a live waiter on the same
	// message name — e.g. a superseding process version (SRD-031.A FR-7).
	defer func() {
		if err := sub.Unsubscribe(); err != nil {
			mw.rt.Logger().Warn("message waiter unsubscribe failed",
				observability.AttrWaiterID, mw.id, observability.AttrError, err.Error())
		}

		close(mw.done) // signal goroutine exit for EventHub.Shutdown drain
	}()

	ch := sub.C()

	for {
		select {
		case <-ctx.Done():
			mw.setStateUnlessFailed(eventproc.WSStopped)

			return

		case <-mw.stopCh:
			mw.rt.Logger().Debug("message waiter stopping", observability.AttrWaiterID, mw.id)

			return

		case env, ok := <-ch:
			if !ok {
				mw.setStateUnlessFailed(eventproc.WSStopped)

				return
			}

			if err := mw.processMessageEvent(ctx, env); err != nil {
				// a fire-definition / processor failure is terminal for this
				// waiter (it already set WSFailed and reported the fire); log it
				// at the goroutine top — nothing above can act on it — and stop
				// (ADR-022 v.1 §2.3/§2.4).
				mw.rt.Logger().Error("message waiter terminally failed",
					observability.AttrWaiterID, mw.id, observability.AttrMessageName, mw.name,
					observability.AttrError, err.Error())

				return
			}
		}
	}
}

// processMessageEvent forwards the payload-carrying event to every registered
// processor, then reports the fire to the hub. It never removes itself — the
// EventHub is the sole remover (ADR-006 v.1 §2.5). A processor's ProcessEvent is
// fire-and-forget (ADR-017 v.1 §2): the receiver's loop runs the correlation
// gate and drops a mismatch on its side, so the waiter forwards unconditionally
// and a non-nil return is a real delivery failure, not a correlation mismatch.
func (mw *messageWaiter) processMessageEvent(
	ctx context.Context,
	env messaging.Envelope,
) error {
	eDef, err := mw.fireDefinition(env)
	if err == nil {
		err = mw.deliver(ctx, eDef)
	}

	if err != nil {
		mw.setState(eventproc.WSFailed)
		// A build (fireDefinition) or delivery failure is terminal for the
		// waiter; report the fire so the hub removes it, and join that report so
		// a hub-side failure surfaces too rather than being swallowed
		// (ADR-022 v.1 §2.2). runMessageService logs the joined error and stops.
		return errors.Join(err, mw.hub.WaiterFired(mw.eDef.ID()))
	}

	// Success: report the fire so the hub removes the waiter iff terminal.
	// WaiterFired errors only on an invariant violation — this waiter absent
	// from the registry it registered into — i.e. hub-state divergence;
	// propagate it (fail-fast, ADR-022 v.1 §2.3) so runMessageService stops the
	// now-orphaned waiter. The normal nil lets the serve-loop continue.
	err = mw.hub.WaiterFired(mw.eDef.ID())

	// …unless the DELIVERY ITSELF stopped this waiter, in which case being
	// absent is orderly, not divergent. A SRD-071 wait-holder wakes its
	// instance synchronously inside ProcessEvent, and the wake releases the
	// holder's registrations — which unregisters and Stops this very waiter
	// while it is still inside the call it is about to report. The wait it
	// served is over and its goroutine should exit; it just must not exit
	// claiming a failure. Reporting nil returns to the serve loop, whose
	// closed stopCh ends it on the ordinary stopping path — the same exit it
	// would have taken had that branch won the select.
	if err != nil && mw.State() == eventproc.WSStopped {
		return nil
	}

	return err
}

// deliver forwards eDef to every registered processor, returning the first
// delivery error. A processor's ProcessEvent is fire-and-forget (ADR-017 v.1
// §2): the receiver's loop runs the correlation gate and drops a mismatch on
// its side, so a non-nil return is a real delivery failure, not a mismatch.
func (mw *messageWaiter) deliver(
	ctx context.Context,
	eDef flow.EventDefinition,
) error {
	mw.m.Lock()
	processors := append([]eventproc.EventProcessor(nil), mw.processors...)
	mw.m.Unlock()

	mw.rt.Logger().Debug("message waiter delivering",
		observability.AttrWaiterID, mw.id, observability.AttrMessageName, mw.name,
		"processors", len(processors))

	for _, ep := range processors {
		if err := ep.ProcessEvent(ctx, eDef); err != nil {
			return err
		}
	}

	return nil
}

// fireDefinition builds the event definition delivered to the processors: the
// broker payload is reconstructed as a typed, Ready datum for the message's
// item (ADR-014 v.1 §2.6) and woven into a cloned definition.
func (mw *messageWaiter) fireDefinition(
	env messaging.Envelope,
) (flow.EventDefinition, error) {
	itemID := mw.eDef.Message().Item().ID()

	datum, err := data.ReadyValueParameter(itemID,
		values.NewVariable(env.Payload), foundation.WithID(itemID))
	if err != nil {
		return nil, payloadErr(mw.eDef.Message().Name(), err)
	}

	return mw.eDef.CloneEventDefinition([]data.Data{datum})
}

// payloadErr classifies a payload datum build failure (FIX-026 — a bad
// message item fails the delivery, never panics the hub).
func payloadErr(msgName string, err error) error {
	return errs.New(
		errs.M("couldn't build payload datum"),
		errs.C(MessageWaiterError, errs.OperationFailed),
		errs.E(err),
		errs.D(observability.AttrMessageName, msgName))
}

// Stop terminates the delivery goroutine of a running waiter.
func (mw *messageWaiter) Stop() error {
	mw.m.Lock()
	defer mw.m.Unlock()

	if mw.state != eventproc.WSRunned {
		return errs.New(
			errs.M("couldn't stop a not-runned waiter"),
			errs.C(MessageWaiterError, errs.InvalidState),
			errs.D("current_state", mw.state.String()))
	}

	mw.state = eventproc.WSStopped

	close(mw.stopCh)

	// Unsubscribe synchronously so the broker has dropped this subscription by the
	// time Stop returns: EventHub.UnregisterEvent may immediately register a
	// replacement waiter on the same message name (a superseding process version),
	// and a subsequent publish must not be claimed into this now-dead, buffered
	// channel (SRD-031.A FR-7). The service goroutine's deferred Unsubscribe (which
	// covers the ctx-cancel / channel-closed exit paths that never call Stop) is
	// idempotent, so the double call is harmless.
	if mw.sub != nil {
		if err := mw.sub.Unsubscribe(); err != nil {
			mw.rt.Logger().Warn("message waiter unsubscribe failed on stop",
				observability.AttrWaiterID, mw.id, observability.AttrError, err.Error())
		}
	}

	return nil
}

// State returns the current waiter state.
func (mw *messageWaiter) State() eventproc.EventWaiterState {
	mw.m.Lock()
	defer mw.m.Unlock()

	return mw.state
}

// setState updates the waiter state under the lock.
func (mw *messageWaiter) setState(s eventproc.EventWaiterState) {
	mw.m.Lock()
	mw.state = s
	mw.m.Unlock()
}

// setStateUnlessFailed updates the state unless the waiter has already failed.
//
// WSFailed is terminal and carries the reason a caller was handed. When
// discardSubscription fails a serviced waiter it closes the very channel the
// service goroutine is selecting on, so that goroutine wakes immediately and
// would otherwise relabel the waiter WSStopped — reporting an orderly shutdown
// for a waiter that broke, and making the failure racy to observe.
func (mw *messageWaiter) setStateUnlessFailed(s eventproc.EventWaiterState) {
	mw.m.Lock()
	defer mw.m.Unlock()

	if mw.state == eventproc.WSFailed {
		return
	}

	mw.state = s
}

// Done returns a channel closed when the service goroutine has exited; nil until
// Service starts it (a registered waiter is always serviced first). EventHub.
// Shutdown waits on it to drain goroutines (ADR-006 v.1 §2.5).
//
// Under mw.m for the same reason Service now writes it under mw.m: the field is
// guarded everywhere else, and a lone bare read is what the next change trips
// over (FIX-041 §1.6).
func (mw *messageWaiter) Done() <-chan struct{} {
	mw.m.Lock()
	defer mw.m.Unlock()

	return mw.done
}

var (
	_ eventproc.EventWaiter = (*messageWaiter)(nil)
	_ eventproc.KeyedWaiter = (*messageWaiter)(nil)
)
