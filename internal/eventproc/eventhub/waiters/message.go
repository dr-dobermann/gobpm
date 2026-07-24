package waiters

import (
	"context"
	"errors"
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
	state      eventproc.EventWaiterState
	m          sync.Mutex
}

// AddKey extends the waiter's broker subscription with key (SRD-017 §4.5 lazy
// association). It is safe before Service has subscribed (a nil subscription is
// a no-op) — the receiver then picks the key up from its instance's grown
// key-set when it does subscribe.
func (mw *messageWaiter) AddKey(key string) error {
	if mw.sub == nil {
		return nil
	}

	return mw.sub.AddKey(key)
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
				errs.D("event_definition_type", string(eDefI.Type())))
	}

	msg := eDef.Message()
	if msg == nil {
		return nil,
			errs.New(
				errs.M("MessageEventDefinition has no message"),
				errs.C(MessageWaiterError, errs.EmptyNotAllowed),
				errs.D("event_definition_id", eDef.ID()))
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
			errs.D("waiter_id", mw.id),
			errs.D("event_processor_id", ep.ID()))
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
		errs.D("event_definition_id", eDef.ID()),
		errs.D("event_definition_type", string(eDef.Type())))
}

// Service subscribes the broker for the waiter's message name and starts the
// delivery goroutine. The subscription is registered synchronously, so a
// message published after Service returns is delivered (subscribe-before-
// publish, ADR-006 v.1 §2.4).
// subscriptionKeys gathers the correlation keys the waiter's processors declare
// for their subscription (SRD-017 §4.3 declared-filter): a processor that
// implements CorrelationKeys (the in-instance receiver track) contributes its
// instance's conversation key values, so the message routes to that instance; a
// processor that declares none (the instance-starter) contributes nothing,
// leaving a wildcard subscription.
func (mw *messageWaiter) subscriptionKeys() []string {
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

func (mw *messageWaiter) Service(ctx context.Context) error {
	if mw.state != eventproc.WSReady {
		return errs.New(
			errs.M("waiter isn't ready to start"),
			errs.C(MessageWaiterError, errs.InvalidState),
			errs.D("current_state", mw.state.String()))
	}

	sub, err := mw.rt.MessageBroker().Subscribe(ctx, mw.name, mw.subscriptionKeys()...)
	if err != nil {
		mw.state = eventproc.WSFailed

		return errs.New(
			errs.M("couldn't subscribe to the message broker"),
			errs.C(MessageWaiterError, errs.OperationFailed),
			errs.D("message_name", mw.name),
			errs.E(err))
	}

	mw.sub = sub
	mw.state = eventproc.WSRunned
	mw.stopCh = make(chan struct{})
	mw.done = make(chan struct{})

	mw.rt.Logger().Debug("message waiter serviced",
		"waiter_id", mw.id, "message_name", mw.name)

	go mw.runMessageService(ctx, sub)

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
				"waiter_id", mw.id, "error", err.Error())
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
			mw.rt.Logger().Debug("message waiter stopping", "waiter_id", mw.id)

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
					"waiter_id", mw.id, "message_name", mw.name,
					"error", err.Error())

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
	return mw.hub.WaiterFired(mw.eDef.ID())
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
		"waiter_id", mw.id, "message_name", mw.name,
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
	datum, err := payloadDatum(mw.eDef.Message().Item().ID(), env.Payload)
	if err != nil {
		return nil, errs.New(
			errs.M("couldn't build payload datum"),
			errs.C(MessageWaiterError, errs.OperationFailed),
			errs.E(err),
			errs.D("message_name", mw.eDef.Message().Name()))
	}

	return mw.eDef.CloneEventDefinition([]data.Data{datum})
}

// payloadDatum wraps a broker payload as a Ready datum for the message item
// addressed by itemID, through the error-returning constructors (FIX-026 —
// a bad id fails the delivery, never panics the hub).
func payloadDatum(itemID string, payload any) (data.Data, error) {
	item, err := data.NewItemDefinition(
		values.NewVariable(payload),
		foundation.WithID(itemID))
	if err != nil {
		return nil, err
	}

	iae, err := data.NewItemAwareElement(item, data.ReadyDataState)
	if err != nil {
		return nil, err
	}

	return data.NewParameter(itemID, iae)
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
				"waiter_id", mw.id, "error", err.Error())
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
