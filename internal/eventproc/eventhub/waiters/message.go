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

// messageWaiter bridges the MessageBroker to the EventHub (ADR-014): it
// subscribes the broker for its message name and, on a matching envelope, fires
// the event (carrying the payload) to the registered processors and reports the
// fire to the hub. It never removes itself — the EventHub is the sole remover
// (ADR-006 §2.5). The waiter is a pure forwarder: it hands every
// coarse-matched (broker keyed-routed) envelope to its processors and never
// self-terminates on a fire. An in-instance receiver's waiter is removed when
// its track consumes the event and unregisters (the hub drops the emptied
// waiter); the instance-starter's processor (SRD-015) never unregisters, so its
// waiter keeps firing for every matching message until Stop. Fine correlation is
// the receiving instance loop's job, not the waiter's (ADR-017 §2): the loop
// runs the correlation gate and drops a mismatch, keeping its track parked.
type messageWaiter struct {
	hub    eventproc.EventHub
	rt     renv.EngineRuntime
	eDef   *events.MessageEventDefinition
	stopCh chan struct{}
	done   chan struct{}
	sub    messaging.Subscription
	name   string
	id     string
	// sent is every correlation key the broker has already been given — the
	// ones Subscribe was created with, plus everything applied since. A key is
	// sent once: membroker collapses a repeat (its key-set is a map), but the
	// port is host-implementable and an adapter that turns each key into a
	// queue-level binding makes a second one.
	sent       map[string]struct{}
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
	sub, fresh, err := mw.acceptKeys(key)
	if err != nil || sub == nil || len(fresh) == 0 {
		// refused, buffered for Service to pick up, or already at the broker
		return err
	}

	// outside the lock: the subscription belongs to the host's broker, and
	// the engine does not hold its own lock across a host call.
	if err := sub.AddKey(key); err != nil {
		mw.unrecordKeys(fresh) // it never arrived; let a retry re-issue it

		return err
	}

	return nil
}

// terminalStates are the waiter states from which no key can reach a broker
// again: the subscription is gone (or about to be) and no goroutine will serve
// one. Declared as data because the question "is this waiter finished?" is a
// lookup, and a switch invites the next state to be added to one of its
// readers and not the others.
var terminalStates = map[eventproc.EventWaiterState]struct{}{
	eventproc.WSEnded:   {},
	eventproc.WSStopped: {},
	eventproc.WSFailed:  {},
}

// acceptKeys stages keys on the waiter and returns the live subscription to
// apply them to — or nil when it buffered them instead, for Service to pick up
// when it subscribes.
//
// It exists so that `mw.sub == nil` is interpreted in exactly ONE place. That
// test means "not subscribed YET" before Service, and "not subscribed ANY
// MORE" after discardSubscription hands the subscription back — and reading
// the second as the first buffers a key into a waiter the hub has unmapped and
// nothing will ever service, returning nil to a caller that believes the key
// was accepted. That is #320's silence, restored inside the fix for #320: the
// key is taken, never routed, and no error is reported anywhere.
//
// The two readers were fifteen lines apart and only one of them checked the
// state, which is why the check now lives with the field rather than with each
// reader.
func (mw *messageWaiter) acceptKeys(
	keys ...string,
) (messaging.Subscription, []string, error) {
	mw.m.Lock()
	defer mw.m.Unlock()

	if _, done := terminalStates[mw.state]; done {
		return nil, nil, errs.New(
			errs.M("couldn't add a correlation key to a finished waiter"),
			errs.C(MessageWaiterError, errs.InvalidState),
			errs.D("current_state", mw.state.String()),
			errs.D(observability.AttrWaiterID, mw.id),
			errs.D(observability.AttrMessageName, mw.name))
	}

	if mw.sub == nil {
		// Buffered whole, and deliberately NOT recorded as sent: Service
		// de-duplicates the buffer against the same set when it subscribes.
		mw.pendingKeys = append(mw.pendingKeys, keys...)

		return nil, nil, nil
	}

	return mw.sub, mw.freshLocked(keys), nil
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
		sent:       map[string]struct{}{},
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

	// IndexFunc, not Index: an EventProcessor is an interface, and comparing
	// one whose dynamic type is uncomparable — a struct holding a slice or a
	// map, which a processor carrying correlation keys naturally is — PANICS.
	// slices.Index does exactly that comparison (master's sameProcessor, merged
	// from the parallel #320 fix).
	if idx := slices.IndexFunc(mw.processors, sameProcessor(ep)); idx == -1 {
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
// envelope waits in its inbox until a subscription wants it (ADR-006 §2.4).
// The inverse window (key subscribed, processor not listed) consumes and
// discards.
//
// Guarded by TestJoinHalvesAreSeparable and
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

	// acceptKeys BUFFERS when there is no subscription yet — it does not skip.
	// It is tempting to reason that a registered processor is in Service's
	// snapshot and will have its keys read there, but that is false for exactly
	// the window #320 was lost in: a processor joining after Service snapshots
	// its processors and before it publishes mw.sub is in neither, and its key
	// would vanish as before.
	// `fresh` is only what the broker does not already have. A processor may
	// answer to a key another one already brought, may repeat one of its own,
	// and — since AddEventProcessor is idempotent while this step is not — may
	// be joined twice, which used to re-send its whole key-set. membroker
	// collapses the repeat (its key-set is a map); a host adapter that turns
	// each key into a queue-level binding makes a second binding.
	sub, fresh, err := mw.acceptKeys(keys...)
	if err != nil || sub == nil || len(fresh) == 0 {
		return err
	}

	for i, k := range fresh {
		if err := sub.AddKey(k); err != nil {
			// the failed key and everything after it never reached the broker
			mw.unrecordKeys(fresh[i:])

			// Discard ONLY once something has actually landed. A key-set the
			// broker took part of cannot be repaired — the port has no
			// RemoveKey — so it can only be thrown away whole. But a failure on
			// the FIRST key leaves the subscription exactly as it was, and
			// tearing down a healthy subscription would strand every processor
			// already parked on it for nothing.
			if i == 0 {
				return errs.New(
					errs.M("couldn't extend the subscription for a joining "+
						"processor"),
					errs.C(MessageWaiterError, errs.OperationFailed),
					errs.D(observability.AttrMessageName, mw.name),
					errs.D(observability.AttrWaiterID, mw.id),
					errs.E(err))
			}

			return mw.discardSubscription(sub, err,
				"couldn't extend the subscription for a joining processor")
		}
	}

	return nil
}

// unsentKeys returns the subset of keys the broker has not been given yet, and
// records them as sent in the same critical section.
//
// Recorded on the DECISION to send rather than on a successful send: two
// concurrent joins carrying the same key would otherwise both find it unsent
// and both send it, which is the duplicate this set exists to prevent. A key
// whose AddKey then fails costs the waiter its subscription (or, on the first
// key, costs the join), so there is nothing left to un-record.
func (mw *messageWaiter) unsentKeys(keys []string) []string {
	mw.m.Lock()
	defer mw.m.Unlock()

	return mw.freshLocked(keys)
}

// unrecordKeys puts keys back in the unsent set after they failed to reach the
// broker, so a retry re-issues them.
//
// Without this a retry is WORSE than a no-op: unsentKeys records a key when it
// decides to send it, so a second attempt would find it already recorded, skip
// it, and return success for a key the broker never received. That is #320's
// silence — a key accepted, never routed, nothing reported — reached through
// the very set that exists to prevent duplicates.
//
// Un-recording can let two concurrent joins both send one key if one of them
// fails; that costs a duplicate, and a duplicate is the failure this set exists
// to avoid, while the alternative is a lost key. Cheaper is not the same as
// safer, so the loss loses.
func (mw *messageWaiter) unrecordKeys(keys []string) {
	mw.m.Lock()
	defer mw.m.Unlock()

	for _, k := range keys {
		delete(mw.sent, k)
	}
}

// freshLocked is unsentKeys' body for a caller that already holds mw.m.
func (mw *messageWaiter) freshLocked(keys []string) []string {
	fresh := make([]string, 0, len(keys))

	for _, k := range keys {
		if _, ok := mw.sent[k]; ok {
			continue
		}

		// guards a repeat within this very call, too
		mw.sent[k] = struct{}{}
		fresh = append(fresh, k)
	}

	return fresh
}

// discardSubscription drops the whole broker subscription after a key could not
// be applied, marks the waiter failed and wraps err.
//
// Discarding is the only repair available: messaging.Subscription can grow a
// key-set but not shrink one, so a partly-applied set cannot be undone in place
// (FIX-041 §3.1 D2). It is also a safe one — the messages the discarded keys
// would have matched go back to waiting in the broker's inbox for a
// subscription that wants them (ADR-006 §2.4), whereas an orphan key left
// on a live subscription eats them permanently, which is the #320 failure mode
// made durable.
//
// This is not a waiter removing itself (ADR-006 §2.5): it drops its own
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

	idx := slices.IndexFunc(mw.processors, sameProcessor(ep))
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
// publish, ADR-006 §2.4).
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
	// arrived before Service reaches both, so the raw set holds duplicates
	// (FIX-041 §1.5). unsentKeys removes them AND records what the broker is
	// about to be given, so nothing sends any of them a second time later.
	keys = mw.unsentKeys(keys)

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

	// Unlike ApplyProcessorKeys, this loop discards on ANY failure, including
	// the first key. There the subscription survives a first-key failure
	// because only the joining processor is turned away and the waiter keeps
	// serving everyone else; here the waiter itself is being abandoned — the
	// hub never publishes a waiter whose Service returned an error — so a
	// subscription left behind is one nobody will ever unsubscribe (§1.4).
	for _, k := range mw.unsentKeys(late) {
		if aerr := sub.AddKey(k); aerr != nil {
			return mw.discardSubscription(sub, aerr,
				"couldn't apply a correlation key learned during subscribe")
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

// runMessageService waits for matching envelopes (or a stop/cancel) and
// forwards each one to the waiting node. The waiter never self-terminates on a
// fire (ADR-017 §2): it loops, forwarding every coarse-matched message,
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
				// (ADR-022 §2.3/§2.4).
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
// EventHub is the sole remover (ADR-006 §2.5). A processor's ProcessEvent is
// fire-and-forget (ADR-017 §2): the receiver's loop runs the correlation
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
		// (ADR-022 §2.2). runMessageService logs the joined error and stops.
		return errors.Join(err, mw.hub.WaiterFired(mw.eDef.ID()))
	}

	// Success: report the fire so the hub removes the waiter iff terminal.
	// WaiterFired errors only on an invariant violation — this waiter absent
	// from the registry it registered into — i.e. hub-state divergence;
	// propagate it (fail-fast, ADR-022 §2.3) so runMessageService stops the
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
// delivery error. A processor's ProcessEvent is fire-and-forget (ADR-017
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
// item (ADR-014 §2.6) and woven into a cloned definition.
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

	if mw.state != eventproc.WSRunned {
		state := mw.state
		mw.m.Unlock()

		return errs.New(
			errs.M("couldn't stop a not-runned waiter"),
			errs.C(MessageWaiterError, errs.InvalidState),
			errs.D("current_state", state.String()))
	}

	mw.state = eventproc.WSStopped

	close(mw.stopCh)

	sub := mw.sub

	mw.m.Unlock()

	// Unsubscribe synchronously so the broker has dropped this subscription by the
	// time Stop returns: EventHub.UnregisterEvent may immediately register a
	// replacement waiter on the same message name (a superseding process version),
	// and a subsequent publish must not be claimed into this now-dead, buffered
	// channel (SRD-031.A FR-7). The service goroutine's deferred Unsubscribe (which
	// covers the ctx-cancel / channel-closed exit paths that never call Stop) is
	// idempotent, so the double call is harmless.
	//
	// Synchronous, but NOT under mw.m: it is the host's broker, and holding the
	// waiter's lock across it blocks State, EventProcessors and every delivery
	// snapshot behind a call this engine cannot hurry (FIX-038 §1.1). The state
	// is already WSStopped and stopCh already closed, so nothing observes a
	// half-stopped waiter through the gap.
	if sub != nil {
		if err := sub.Unsubscribe(); err != nil {
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
// Shutdown waits on it to drain goroutines (ADR-006 §2.5).
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
