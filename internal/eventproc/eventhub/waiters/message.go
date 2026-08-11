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
	// subscribedKeys is what mw.sub was created carrying — see pendingKeys.
	subscribedKeys []string
	hub            eventproc.EventHub
	rt             renv.EngineRuntime
	eDef           *events.MessageEventDefinition
	stopCh         chan struct{}
	done           chan struct{}
	sub            messaging.Subscription
	name           string
	id             string
	processors     []eventproc.EventProcessor
	state          eventproc.EventWaiterState
	m              sync.Mutex
}

// AddKey extends the waiter's broker subscription with key (SRD-017 §4.5 lazy
// association). It is safe before Service has subscribed (a nil subscription is
// a no-op) — the receiver then picks the key up from its instance's grown
// key-set when it does subscribe.
//
// The key is recorded in subscribedKeys on success, because that field is what
// a joining processor consults to decide whether its own key still needs
// subscribing. This path extends the SAME subscription (correlation.go's
// extendReceivers reaches it for every parked message catch, MI iterations
// included), so leaving it unrecorded would make the field understate what the
// subscription carries.
func (mw *messageWaiter) AddKey(key string) error {
	mw.m.Lock()
	sub := mw.sub
	mw.m.Unlock()

	if sub == nil {
		return nil
	}

	if err := sub.AddKey(key); err != nil {
		return err
	}

	mw.recordKey(key)

	return nil
}

// recordKey notes that the waiter's subscription now carries key, ignoring one
// it already lists — a retry re-issues AddKey deliberately (see
// AddEventProcessor), and without this the record would grow on every retry.
func (mw *messageWaiter) recordKey(key string) {
	mw.m.Lock()
	defer mw.m.Unlock()

	if !slices.Contains(mw.subscribedKeys, key) {
		mw.subscribedKeys = append(mw.subscribedKeys, key)
	}
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

// AddEventProcessor adds ep to the waiter's processor list (idempotent) and
// extends the live broker subscription with ep's correlation keys.
//
// The key half is not an optimization. subscriptionKeys() is read once, by
// Service, when the subscription is created — so a processor that JOINS an
// already-serving waiter contributes its keys to nothing. Its instance then
// sits parked on a subscription that does not carry its key, and an envelope
// addressed to it matches no subscription and is buffered forever: the
// message is not misrouted, it is silently never delivered.
//
// Parallel multi-instance is where this bites. Iterations of one shared catch
// register concurrently; whichever arrives first builds and services the
// waiter with only ITS key, and every later iteration joins here. Whether the
// bug appears depends purely on whether the second registration lands before
// or after Service, which is why it surfaced as an intermittent lost
// delivery rather than a reproducible failure.
func (mw *messageWaiter) AddEventProcessor(ep eventproc.EventProcessor) error {
	if ep == nil {
		return errs.New(
			errs.M("empty EventProcessor isn't allowed"),
			errs.C(MessageWaiterError, errs.EmptyNotAllowed))
	}

	sub := mw.addProcessor(ep)

	if sub == nil {
		// Not serving yet. Service reads the processor list when it
		// subscribes, and re-reads it once the subscription is published
		// (subscribeLateJoiners) — so a join landing on either side of that
		// read is picked up, and this return strands nothing.
		return nil
	}

	if len(mw.pendingKeys(ep)) == 0 {
		// Already carried — either Service included it, or a previous join
		// subscribed it. Nothing to do, and nothing silently skipped.
		return nil
	}

	// The keys are (re-)subscribed even when ep was ALREADY on the list.
	//
	// Returning early on "already present" would make a retry a no-op, and a
	// retry is exactly what a caller does after this method fails: the
	// processor is appended before the subscription is extended, so a failed
	// AddKey leaves ep registered with its key missing. Reporting success on
	// the second call would then strand it — registered, parked, and
	// unreachable — which is the very failure this method was changed to
	// prevent. AddKey is idempotent at the broker, so re-issuing costs
	// nothing and makes the retry mean something.
	return mw.subscribeKeysOf(ep, sub)
}

// addProcessor records ep (idempotently) and returns the live subscription,
// if the waiter is serving.
//
// It exists so the lock is released by DEFER rather than by hand. The first
// version of this unlocked explicitly before the foreign AddKey call below,
// and a panic in between then left the waiter's mutex held — turning a
// recoverable fault into a deadlocked Stop, which is how it presented.
func (mw *messageWaiter) addProcessor(
	ep eventproc.EventProcessor,
) messaging.Subscription {
	mw.m.Lock()
	defer mw.m.Unlock()

	if slices.IndexFunc(mw.processors, sameProcessor(ep)) == -1 {
		mw.processors = append(mw.processors, ep)
	}

	return mw.sub
}

// pendingKeys returns the keys ep declares that this waiter's subscription
// does not already carry.
//
// The decision is made against what the subscription ACTUALLY carries —
// recorded when it was created and kept current by every path that extends it
// — rather than against whether ep is on the processor list. A processor may
// be listed and its key unsubscribed (it joined while Service was blocked in
// Subscribe), and it may be listed twice over with its key already carried (a
// caller retrying a failed AddEventProcessor).
func (mw *messageWaiter) pendingKeys(ep eventproc.EventProcessor) []string {
	kp, ok := ep.(interface{ CorrelationKeys() []string })
	if !ok {
		return nil
	}

	mw.m.Lock()
	defer mw.m.Unlock()

	var missing []string

	for _, k := range kp.CorrelationKeys() {
		if k != "" && !slices.Contains(mw.subscribedKeys, k) {
			missing = append(missing, k)
		}
	}

	return missing
}

// subscribeKeysOf extends sub with the correlation keys ep declares.
//
// It runs OUTSIDE the waiter's lock: AddKey reaches the host's broker, which
// may be remote and may block, and holding a lock across a foreign call is
// what FIX-038 §1.1 removed from the hub's registration path.
func (mw *messageWaiter) subscribeKeysOf(
	ep eventproc.EventProcessor, sub messaging.Subscription,
) error {
	kp, ok := ep.(interface{ CorrelationKeys() []string })
	if !ok {
		// A processor declaring no keys wants the wildcard the subscription
		// already has — an instance-starter, for one.
		return nil
	}

	for _, k := range kp.CorrelationKeys() {
		if k == "" {
			continue
		}

		if err := sub.AddKey(k); err == nil {
			mw.recordKey(k)
		} else {
			return errs.New(
				errs.M("couldn't extend the subscription with a joining "+
					"processor's correlation key"),
				errs.C(MessageWaiterError, errs.OperationFailed),
				errs.D(observability.AttrMessageName, mw.name),
				errs.D(observability.AttrEventProcessorID, ep.ID()),
				errs.E(err))
		}
	}

	return nil
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

// subscriptionKeys gathers the correlation keys the waiter's processors declare
// for their subscription (SRD-017 §4.3 declared-filter): a processor that
// implements CorrelationKeys (the in-instance receiver track) contributes its
// instance's conversation key values, so the message routes to that instance; a
// processor that declares none (the instance-starter) contributes nothing,
// leaving a wildcard subscription.
func (mw *messageWaiter) subscriptionKeys() []string {
	mw.m.Lock()
	defer mw.m.Unlock()

	var keys []string

	for _, p := range mw.processors {
		if kp, ok := p.(interface {
			CorrelationKeys() []string
		}); ok {
			keys = append(keys, kp.CorrelationKeys()...)
		}
	}

	return keys
}

// Service subscribes the broker for the waiter's message name and starts the
// delivery goroutine. The subscription is registered synchronously, so a
// message published after Service returns is delivered (subscribe-before-
// publish, ADR-006 v.1 §2.4).
//
// The key list is read BEFORE the blocking Subscribe and mw.sub is published
// only after it, so a processor joining in between defers to a Service that
// has already read the list — the lost-delivery bug AddEventProcessor exists
// to prevent, one window narrower. Service therefore RE-READS the processors
// once the subscription is published and subscribes anything that appeared.
//
// The hub makes that window unreachable today: registerWaiter builds and
// services a waiter before publishing it, so no other goroutine holds a
// reference while this runs. That is an ordering in another package, and a
// waiter whose correctness rests on it would break silently the day the
// ordering changes.
func (mw *messageWaiter) Service(ctx context.Context) error {
	if mw.State() != eventproc.WSReady {
		return errs.New(
			errs.M("waiter isn't ready to start"),
			errs.C(MessageWaiterError, errs.InvalidState),
			errs.D("current_state", mw.State().String()))
	}

	keys := mw.subscriptionKeys()

	sub, err := mw.rt.MessageBroker().Subscribe(ctx, mw.name, keys...)
	if err != nil {
		mw.setState(eventproc.WSFailed)

		return errs.New(
			errs.M("couldn't subscribe to the message broker"),
			errs.C(MessageWaiterError, errs.OperationFailed),
			errs.D(observability.AttrMessageName, mw.name),
			errs.E(err))
	}

	// Published first, so a joiner arriving from here on takes the ordinary
	// AddEventProcessor path and subscribes its own key.
	mw.m.Lock()
	mw.sub = sub
	mw.subscribedKeys = keys
	mw.m.Unlock()

	if err := mw.subscribeLateJoiners(sub); err != nil {
		if uErr := sub.Unsubscribe(); uErr != nil {
			mw.rt.Logger().Warn("message waiter unsubscribe failed after a "+
				"failed start", observability.AttrWaiterID, mw.id,
				observability.AttrError, uErr.Error())
		}

		mw.m.Lock()
		mw.sub = nil
		mw.subscribedKeys = nil
		mw.state = eventproc.WSFailed
		mw.m.Unlock()

		return err
	}

	// Running state is published last and together: until the goroutine
	// exists there is nothing for Stop to stop, and stopCh/done must be in
	// place before it can be reached.
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

// subscribeLateJoiners extends sub with the keys of every processor whose keys
// the subscription does not already carry — those that joined while Subscribe
// was blocking, and whose AddEventProcessor deferred to this method.
func (mw *messageWaiter) subscribeLateJoiners(
	sub messaging.Subscription,
) error {
	mw.m.Lock()
	processors := append([]eventproc.EventProcessor(nil), mw.processors...)
	mw.m.Unlock()

	for _, ep := range processors {
		if len(mw.pendingKeys(ep)) == 0 {
			continue
		}

		if err := mw.subscribeKeysOf(ep, sub); err != nil {
			return err
		}
	}

	return nil
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
			mw.setState(eventproc.WSStopped)

			return

		case <-mw.stopCh:
			mw.rt.Logger().Debug("message waiter stopping", observability.AttrWaiterID, mw.id)

			return

		case env, ok := <-ch:
			if !ok {
				mw.setState(eventproc.WSStopped)

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

// Done returns a channel closed when the service goroutine has exited; nil until
// Service starts it (a registered waiter is always serviced first). EventHub.
// Shutdown waits on it to drain goroutines (ADR-006 v.1 §2.5).
func (mw *messageWaiter) Done() <-chan struct{} {
	return mw.done
}

var _ eventproc.EventWaiter = (*messageWaiter)(nil)
