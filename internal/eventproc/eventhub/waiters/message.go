package waiters

import (
	"context"
	"reflect"
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
// subscribes the broker for its message name and, on the first matching
// envelope, fires the event (carrying the payload) to the registered
// processors, then removes itself from the hub. It is one-shot — a single
// message resumes the waiting node.
type messageWaiter struct {
	hub        eventproc.EventHub
	rt         renv.EngineRuntime
	eDef       *events.MessageEventDefinition
	stopCh     chan struct{}
	name       string
	id         string
	processors []eventproc.EventProcessor
	state      eventproc.EventWaiterState
	m          sync.Mutex
}

// NewMessageWaiter builds a messageWaiter for a MessageEventDefinition. It
// rejects empty dependencies and a non-message event definition.
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
				errs.D("event_definition_type", reflect.TypeOf(eDefI)))
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
		errs.D("event_definition_type", eDef.Type()))
}

// Service subscribes the broker for the waiter's message name and starts the
// delivery goroutine. The subscription is registered synchronously, so a
// message published after Service returns is delivered (subscribe-before-
// publish, ADR-006 v.1 §2.4).
func (mw *messageWaiter) Service(ctx context.Context) error {
	if mw.state != eventproc.WSReady {
		return errs.New(
			errs.M("waiter isn't ready to start"),
			errs.C(MessageWaiterError, errs.InvalidState),
			errs.D("current_state", mw.state))
	}

	ch, err := mw.rt.MessageBroker().Subscribe(ctx, mw.name, "")
	if err != nil {
		mw.state = eventproc.WSFailed

		return errs.New(
			errs.M("couldn't subscribe to the message broker"),
			errs.C(MessageWaiterError, errs.OperationFailed),
			errs.D("message_name", mw.name),
			errs.E(err))
	}

	mw.state = eventproc.WSRunned
	mw.stopCh = make(chan struct{})

	go mw.runMessageService(ctx, ch)

	return nil
}

// runMessageService waits for the first matching envelope (or a stop/cancel)
// and resumes the waiting node. The waiter is one-shot: it returns after the
// first delivered message.
func (mw *messageWaiter) runMessageService(
	ctx context.Context,
	ch <-chan messaging.Envelope,
) {
	select {
	case <-ctx.Done():
		mw.setState(eventproc.WSStopped)

	case <-mw.stopCh:
		mw.rt.Logger().Debug("message waiter stopping", "waiter_id", mw.id)

	case env, ok := <-ch:
		if !ok {
			mw.setState(eventproc.WSStopped)

			return
		}

		_ = mw.processMessageEvent(ctx, env)
	}
}

// processMessageEvent fires the payload-carrying event to every registered
// processor, then removes the waiter from the hub.
func (mw *messageWaiter) processMessageEvent(
	ctx context.Context,
	env messaging.Envelope,
) error {
	eDef, err := mw.fireDefinition(env)
	if err != nil {
		mw.setState(eventproc.WSFailed)

		return err
	}

	mw.m.Lock()
	processors := append([]eventproc.EventProcessor(nil), mw.processors...)
	mw.m.Unlock()

	for _, ep := range processors {
		if err := ep.ProcessEvent(ctx, eDef); err != nil {
			mw.setState(eventproc.WSFailed)

			return err
		}
	}

	mw.m.Lock()
	mw.processors = []eventproc.EventProcessor{}
	mw.state = eventproc.WSEnded
	mw.m.Unlock()

	_ = mw.hub.RemoveWaiter(mw.eDef.ID()) // ignore error during cleanup

	return nil
}

// fireDefinition builds the event definition delivered to the processors: the
// broker payload is reconstructed as a typed, Ready datum for the message's
// item (ADR-014 v.1 §2.6) and woven into a cloned definition. Phase-1 messages
// always carry an item (bpmncommon.NewMessage invariant); the datum building
// uses the Must* constructors as ServiceTask.Exec does on its result path.
func (mw *messageWaiter) fireDefinition(
	env messaging.Envelope,
) (flow.EventDefinition, error) {
	item := mw.eDef.Message().Item()

	datum := data.MustParameter(item.ID(),
		data.MustItemAwareElement(
			data.MustItemDefinition(
				values.NewVariable(env.Payload),
				foundation.WithID(item.ID())),
			data.ReadyDataState))

	return mw.eDef.CloneEvent([]data.Data{datum})
}

// Stop terminates the delivery goroutine of a running waiter.
func (mw *messageWaiter) Stop() error {
	mw.m.Lock()
	defer mw.m.Unlock()

	if mw.state != eventproc.WSRunned {
		return errs.New(
			errs.M("couldn't stop a not-runned waiter"),
			errs.C(MessageWaiterError, errs.InvalidState),
			errs.D("current_state", mw.state))
	}

	mw.state = eventproc.WSStopped

	close(mw.stopCh)

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

var _ eventproc.EventWaiter = (*messageWaiter)(nil)
