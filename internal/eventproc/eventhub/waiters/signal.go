package waiters

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"

	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/renv"
)

// SignalWaiterError classifies signalWaiter failures.
const SignalWaiterError = "SIGNAL_WAITER_ERROR"

// signalWaiter catches a BPMN signal (ADR-006 v.1 §2.1, SRD-020). A signal is a
// broadcast publication with no correlation: a throw of signal name X fires
// EVERY catcher of X. The waiter is **passive** — unlike the message (broker)
// and timer (ticker) waiters it has no external source, so it spawns no service
// goroutine; it is fired only by an in-process throw through EventHub.Process.
// Broadcast across instances falls out of identity sharing: signal event
// definitions have no CloneForInstance, so every instance catching the same
// modeled node shares one eDef.ID() and so one waiter, with one processor per
// catching track. It never removes itself — the hub is the sole owner
// (ADR-006 v.1 §2.5).
type signalWaiter struct {
	hub        eventproc.EventHub
	rt         renv.EngineRuntime
	eDef       *events.SignalEventDefinition
	ctx        context.Context
	done       chan struct{}
	name       string
	id         string
	processors []eventproc.EventProcessor
	state      eventproc.EventWaiterState
	m          sync.Mutex
}

// NewSignalWaiter builds a signalWaiter for a SignalEventDefinition. It rejects
// empty dependencies and a non-signal definition.
func NewSignalWaiter(
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
				errs.C(SignalWaiterError,
					errs.InvalidParameter, errs.EmptyNotAllowed))
	}

	eDef, ok := eDefI.(*events.SignalEventDefinition)
	if !ok {
		return nil,
			errs.New(
				errs.M("not a SignalEventDefinition"),
				errs.C(SignalWaiterError, errs.TypeCastingError),
				errs.D("event_definition_type", string(eDefI.Type())))
	}

	sig := eDef.Signal()
	if sig == nil {
		return nil,
			errs.New(
				errs.M("SignalEventDefinition has no signal"),
				errs.C(SignalWaiterError, errs.EmptyNotAllowed),
				errs.D("event_definition_id", eDef.ID()))
	}

	id = strings.TrimSpace(id)
	if id == "" {
		id = foundation.GenerateID()
	}

	// done is closed at construction: a passive waiter has no goroutine to wait
	// for, so EventHub.Shutdown's drain on Done() returns immediately.
	done := make(chan struct{})
	close(done)

	return &signalWaiter{
		id:         id,
		name:       sig.Name(),
		eDef:       eDef,
		hub:        eh,
		rt:         rt,
		done:       done,
		processors: []eventproc.EventProcessor{ep},
		state:      eventproc.WSReady,
	}, nil
}

// ID returns the waiter id.
func (sw *signalWaiter) ID() string {
	return sw.id
}

// EventDefinition returns the signal event definition the waiter waits for.
func (sw *signalWaiter) EventDefinition() flow.EventDefinition {
	return sw.eDef
}

// AddEventProcessor adds ep to the waiter's processor list (idempotent). A
// second instance catching the same shared-id signal lands here, joining the
// broadcast set.
func (sw *signalWaiter) AddEventProcessor(ep eventproc.EventProcessor) error {
	if ep == nil {
		return errs.New(
			errs.M("empty EventProcessor isn't allowed"),
			errs.C(SignalWaiterError, errs.EmptyNotAllowed))
	}

	sw.m.Lock()
	defer sw.m.Unlock()

	if idx := slices.Index(sw.processors, ep); idx == -1 {
		sw.processors = append(sw.processors, ep)
	}

	return nil
}

// RemoveEventProcessor removes ep from the waiter's processor list (a catcher
// that fired and consumed the signal, or a canceled track).
func (sw *signalWaiter) RemoveEventProcessor(ep eventproc.EventProcessor) error {
	if ep == nil {
		return errs.New(
			errs.M("empty EventProcessor isn't allowed"),
			errs.C(SignalWaiterError, errs.EmptyNotAllowed))
	}

	sw.m.Lock()
	defer sw.m.Unlock()

	idx := slices.Index(sw.processors, ep)
	if idx == -1 {
		return errs.New(
			errs.M("event processor isn't registered with the waiter"),
			errs.C(SignalWaiterError, errs.ObjectNotFound),
			errs.D("waiter_id", sw.id),
			errs.D("event_processor_id", ep.ID()))
	}

	sw.processors = slices.Delete(sw.processors, idx, idx+1)

	return nil
}

// EventProcessors returns the waiter's registered processors.
func (sw *signalWaiter) EventProcessors() []eventproc.EventProcessor {
	sw.m.Lock()
	defer sw.m.Unlock()

	return sw.processors
}

// Service records the engine context the catchers resume under and marks the
// waiter running. It spawns NO goroutine: a signal has no external source.
func (sw *signalWaiter) Service(ctx context.Context) error {
	sw.m.Lock()
	defer sw.m.Unlock()

	if sw.state != eventproc.WSReady {
		return errs.New(
			errs.M("waiter isn't ready to start"),
			errs.C(SignalWaiterError, errs.InvalidState),
			errs.D("current_state", sw.state.String()))
	}

	sw.ctx = ctx
	sw.state = eventproc.WSRunned

	sw.rt.Logger().Debug("signal waiter serviced",
		"waiter_id", sw.id, "signal", sw.name)

	return nil
}

// Process delivers a thrown signal to EVERY registered catcher — the broadcast
// (ADR-006 v.1 §2.1, BPMN §10.5.1). It has no correlation filter. Delivery is
// best-effort: one catcher's failure is logged and does not stop the signal
// reaching the others. Each catching track self-unregisters as it resumes
// (track.ProcessEvent), so the hub removes the emptied waiter afterwards.
func (sw *signalWaiter) Process(eDef flow.EventDefinition) error {
	sw.m.Lock()
	ctx := sw.ctx
	// Iterate a copy: each delivery makes its track self-unregister, mutating
	// sw.processors under sw.m while we fan out.
	processors := append([]eventproc.EventProcessor(nil), sw.processors...)
	sw.m.Unlock()

	sw.rt.Logger().Debug("signal waiter delivering",
		"waiter_id", sw.id, "signal", sw.name,
		"processors", len(processors))

	for _, ep := range processors {
		err := ep.ProcessEvent(ctx, eDef)
		if err == nil {
			continue
		}

		if errors.Is(err, eventproc.ErrRejected) {
			// the catcher is no longer waiting (an already-fired / deferred-choice
			// loser, or a correlation mismatch): a benign drop, not a delivery
			// failure — broadcast reaches every catcher in range (FIX-007).
			sw.rt.Logger().Debug("signal delivery skipped: catcher not waiting",
				"waiter_id", sw.id, "signal", sw.name,
				"event_processor_id", ep.ID())

			continue
		}

		sw.rt.Logger().Warn("signal delivery to a catcher failed",
			"waiter_id", sw.id,
			"signal", sw.name,
			"event_processor_id", ep.ID(),
			"error", err.Error())
	}

	return nil
}

// Stop marks the waiter stopped. There is no goroutine to signal — the passive
// waiter has none.
func (sw *signalWaiter) Stop() error {
	sw.m.Lock()
	defer sw.m.Unlock()

	sw.state = eventproc.WSStopped

	return nil
}

// State returns the current waiter state.
func (sw *signalWaiter) State() eventproc.EventWaiterState {
	sw.m.Lock()
	defer sw.m.Unlock()

	return sw.state
}

// Done returns an already-closed channel: a passive waiter has no service
// goroutine, so EventHub.Shutdown's drain completes immediately (ADR-006 §2.5).
func (sw *signalWaiter) Done() <-chan struct{} {
	return sw.done
}

var _ eventproc.EventWaiter = (*signalWaiter)(nil)
