// Package waiters provides event waiter implementations for different event types.
// Waiters monitor for specific conditions and notify processors when events should occur.
package waiters

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/renv"
)

// TimerWaiterError is the error class for timer waiter errors.
const TimerWaiterError = "TIMER_WAITER_ERROR"

// timeWaiter defines details of timer event described by
// eDef.
type timeWaiter struct {
	next       time.Time
	eDef       flow.EventDefinition
	hub        eventproc.EventHub
	rt         renv.EngineRuntime
	stopCh     chan struct{}
	done       chan struct{}
	id         string
	processors []eventproc.EventProcessor
	state      eventproc.EventWaiterState
	cyclesLeft int
	duration   time.Duration
	m          sync.Mutex
}

// NewTimeWaiter creates a new timer event defined by eDef. rt is the engine
// runtime the waiter evaluates timer expressions and sources time through.
func NewTimeWaiter(
	eh eventproc.EventHub,
	ep eventproc.EventProcessor,
	eDefI flow.EventDefinition,
	id string,
	rt renv.EngineRuntime,
) (eventproc.EventWaiter, error) {
	if ep == nil || eDefI == nil || eh == nil || rt == nil {
		return nil,
			errs.New(
				errs.M("couldn't create a Waiter with empty EventProcessor, EventDefinition, EventHub or EngineRuntime"),
				errs.C(TimerWaiterError, errs.InvalidParameter, errs.EmptyNotAllowed))
	}

	eDef, ok := eDefI.(*events.TimerEventDefinition)
	if !ok {
		return nil,
			errs.New(
				errs.M("not an TimerEventDefinition"),
				errs.C(TimerWaiterError, errs.TypeCastingError),
				errs.D("event_definition_type", reflect.TypeOf(eDefI)))
	}

	id = strings.TrimSpace(id)
	if id == "" {
		id = foundation.GenerateID()
	}

	tw := timeWaiter{
		id:         id,
		eDef:       eDef,
		hub:        eh,
		rt:         rt,
		processors: []eventproc.EventProcessor{ep},
	}

	if err := tw.parseEDef(eDef, ep); err != nil {
		return nil,
			errs.New(
				errs.M("TimerEventDefinition parsing failed"),
				errs.C(TimerWaiterError, errs.OperationFailed),
				errs.E(err))
	}

	tw.state = eventproc.WSReady

	return &tw, nil
}

// parseEDef parsing TimerEventDefinition and fills timeWaiter structure
// with appropriate values.
func (tw *timeWaiter) parseEDef(
	eDef *events.TimerEventDefinition,
	ep eventproc.EventProcessor,
) error {
	ds, _ := ep.(data.Source)

	expressions := map[string]data.FormalExpression{
		"Time":     eDef.Time(),
		"Cycle":    eDef.Cycle(),
		"Duration": eDef.Duration(),
	}

	initialized := false
	for name, fe := range expressions {
		if fe == nil {
			continue
		}

		initialized = true
		if err := tw.processExpression(name, fe, ds); err != nil {
			return err
		}
	}

	if !initialized {
		return errs.New(
			errs.M("Timer should have Time, Cycle or Duration expresssion"))
	}

	return nil
}

// processExpression processes a single formal expression for the timer
func (tw *timeWaiter) processExpression(name string, fe data.FormalExpression, ds data.Source) error {
	tm, err := tw.rt.ExpressionEngine().Evaluate(context.Background(), fe, ds)
	if err != nil {
		return errs.New(
			errs.M(fmt.Sprintf("couldn't evaluate TimerEventDefintion %s value", name)),
			errs.E(err))
	}

	ctx := context.Background()
	var ok bool

	switch name {
	case "Time":
		tw.next, ok = tm.Get(ctx).(time.Time)
		if ok && tw.next.Before(tw.rt.Clock().Now()) {
			return errs.New(errs.M("couldn't use past time as a timer"))
		}

	case "Cycle":
		tw.cyclesLeft, ok = tm.Get(ctx).(int)
		if ok && tw.cyclesLeft <= 0 {
			return errs.New(errs.M("cycle isn't defined"))
		}

	case "Duration":
		tw.duration, ok = tm.Get(ctx).(time.Duration)
		if ok && tw.duration <= 0 {
			return errs.New(errs.M("duration isn't defined"))
		}
	}

	if !ok {
		return errs.New(
			errs.M(fmt.Sprintf("%s property casting error", name)))
	}

	return nil
}

// -------------------------- foundation.Identifyer interface -----------------

func (tw *timeWaiter) ID() string {
	return tw.id
}

// -------------------------- eventproc.EventWaiter interface -----------------
// EventDefinition returns underlaying event definition.
func (tw *timeWaiter) EventDefinition() flow.EventDefinition {
	return tw.eDef
}

// AddEventProcessor adds single EventProcessor into waiter's list of
// EventProcessors, waiting for the EventDefinition.
// If the EventProcessor already exists in waiters queue, no errors returned.
func (tw *timeWaiter) AddEventProcessor(ep eventproc.EventProcessor) error {
	if ep == nil {
		return errs.New(
			errs.M("empty EventProcessor isn't allowed"),
			errs.C(TimerWaiterError, errs.EmptyNotAllowed))
	}

	tw.m.Lock()
	defer tw.m.Unlock()

	if idx := slices.Index(tw.processors, ep); idx == -1 {
		tw.processors = append(tw.processors, ep)
	}

	return nil
}

// RemoveEventProcessor removes the ep EventProcessor from the waiter's
// processors list — the mirror of AddEventProcessor (same value comparison).
// It returns ObjectNotFound if ep was never registered, so the EventHub can
// tell an idempotent "already gone" from a real failure (FIX-003 B); the hub
// stops and drops the waiter once its last processor leaves.
func (tw *timeWaiter) RemoveEventProcessor(ep eventproc.EventProcessor) error {
	if ep == nil {
		return errs.New(
			errs.M("empty EventProcessor isn't allowed"),
			errs.C(TimerWaiterError, errs.EmptyNotAllowed))
	}

	tw.m.Lock()
	defer tw.m.Unlock()

	idx := slices.Index(tw.processors, ep)
	if idx == -1 {
		return errs.New(
			errs.M("event processor isn't registered with the waiter"),
			errs.C(TimerWaiterError, errs.ObjectNotFound),
			errs.D("waiter_id", tw.id),
			errs.D("event_processor_id", ep.ID()))
	}

	tw.processors = slices.Delete(tw.processors, idx, idx+1)

	return nil
}

// EventProcessor returns the EventProcessor expecting the registered
// EventDefinition.
func (tw *timeWaiter) EventProcessors() []eventproc.EventProcessor {
	tw.m.Lock()
	defer tw.m.Unlock()

	return tw.processors
}

// Process processed single event given by EventHub through EventPropagate
// call.
func (tw *timeWaiter) Process(eDef flow.EventDefinition) error {
	return fmt.Errorf(
		"timeWaiter doesn't process any EventDefinition (got EventDefinition #%s of type %s)",
		eDef.ID(), eDef.Type())
}

// Service runs the waiting/handling routine of registered event defined.
func (tw *timeWaiter) Service(ctx context.Context) error {
	if tw.state != eventproc.WSReady {
		return errs.New(
			errs.M("waiter isn't ready to start"),
			errs.C(TimerWaiterError, errs.InvalidState),
			errs.D("current_state", tw.state),
			errs.D("expected_state", eventproc.WSReady))
	}

	tw.state = eventproc.WSRunned

	if !tw.next.IsZero() {
		// Measure the absolute-timer delay against the injected Clock (the same
		// source the validation at parseEDef used), not the wall clock, so a
		// substituted clock governs the wait — see runTimerService.
		tw.duration = tw.next.Sub(tw.rt.Clock().Now())
		tw.cyclesLeft = 0
	}

	if tw.duration <= 0 {
		return errs.New(
			errs.M("waiter duration is not positive"),
			errs.C(TimerWaiterError, errs.InvalidState),
			errs.D("waiter_id", tw.ID()),
			errs.D("next_time", tw.next),
			errs.D("duration", tw.duration),
			errs.D("cycles", tw.cyclesLeft))
	}

	tw.stopCh = make(chan struct{})
	tw.done = make(chan struct{})

	tw.rt.Logger().Debug("timer waiter serviced",
		"waiter_id", tw.id, "duration", tw.duration)

	go tw.runTimerService(ctx)

	return nil
}

// runTimerService runs the timer waiter service in a background goroutine.
//
// tw.stopCh has exactly ONE closing owner — Stop() — whose close is atomic
// with the state check under tw.m. The ctx.Done branch here does NOT close
// the channel: this goroutine is its only reader and it returns immediately,
// so the close would signal nothing while racing Stop()'s close
// (panic: close of closed channel — audit 1.3 / FIX-003 A).
//
// The wait is driven by tw.rt.Clock().After, NOT time.NewTicker / time.After,
// and the channel is re-armed at the top of every loop iteration. This exists
// for test determinism (FIX-012): the runtime Clock is an injectable
// abstraction (ADR-004 v.1 — "tests inject fake"), and routing the wait
// through it lets a test substitute a clocktest.Clock and drive the timer with
// Advance() instead of really sleeping for tw.duration. With the default syscl
// Clock, After(d) is time.After(d), so production wall-clock behavior is
// identical to the former ticker — the change costs nothing in production and
// unlocks deterministic timer tests (and any future simulation/replay clock).
// Re-arming per iteration covers both shapes uniformly: a one-shot timer
// (cyclesLeft == 0) exits after the first fire because processTimerEvent
// returns a completion error, while a cyclic timer re-arms the next interval.
//
// Do NOT "simplify" this back to time.NewTicker / time.After: it silently
// re-breaks every fake-clock timer test (the goroutine would sleep on real
// time while validation honors the injected clock — the exact split this FIX
// removed).
func (tw *timeWaiter) runTimerService(ctx context.Context) {
	defer close(tw.done) // signal goroutine exit for EventHub.Shutdown drain

	for {
		fire := tw.rt.Clock().After(tw.duration)

		select {
		case <-ctx.Done():
			tw.m.Lock()
			tw.state = eventproc.WSStopped
			tw.m.Unlock()

			return

		case <-tw.stopCh:
			tw.rt.Logger().Debug("timer waiter stopping",
				"waiter_id", tw.id)

			return

		case <-fire:
			if err := tw.processTimerEvent(ctx); err != nil {
				return
			}
		}
	}
}

// processTimerEvent handles timer event processing.
//
// External interface calls (ep.ProcessEvent, tw.hub.WaiterFired) are
// made WITHOUT holding tw.m. Holding a mutex across an interface call
// risks a callback re-entering the waiter and deadlocking, and makes
// the receiver's mutex memory observable to third-party reflect-based
// inspection (e.g. testify/mock's Arguments.Diff) racing with
// concurrent acquirers.
//
// The shape used here: snapshot tw.processors / tw.eDef under the
// lock; release; call external interfaces with the snapshot;
// re-acquire only to mutate state.
func (tw *timeWaiter) processTimerEvent(ctx context.Context) error {
	tw.m.Lock()
	processors := append([]eventproc.EventProcessor(nil), tw.processors...)
	eDef := tw.eDef
	tw.m.Unlock()

	tw.rt.Logger().Debug("timer waiter delivering",
		"waiter_id", tw.id, "processors", len(processors))

	for _, ep := range processors {
		if err := ep.ProcessEvent(ctx, eDef); err != nil {
			tw.m.Lock()
			tw.state = eventproc.WSFailed
			tw.m.Unlock()

			return err
		}
	}

	// Decrement first, THEN test the terminal condition (FIX-012): a Cycle of N
	// must deliver exactly N events. Testing before the decrement spent one
	// extra cycle (N+1 deliveries). A one-shot timer enters with cyclesLeft == 0
	// and ends here on its first fire (0 -> -1, which is <= 0).
	tw.m.Lock()
	tw.cyclesLeft--
	if tw.cyclesLeft <= 0 {
		tw.processors = []eventproc.EventProcessor{}
		tw.state = eventproc.WSEnded
		tw.m.Unlock()

		// Terminal: report the fire; the EventHub (sole remover, ADR-006 v.2
		// §2.5) removes the waiter. The timer no longer removes itself.
		_ = tw.hub.WaiterFired(eDef.ID()) // ignore error during cleanup

		return errs.New(errs.M("timer completed")) // signal completion
	}

	tw.m.Unlock()

	return nil
}

// Stop terminates waiting cycle of the waiter.
//
// The state check is performed under the mutex together with the write,
// so the read-then-write sequence is atomic with respect to concurrent
// writers (runTimerService / processTimerEvent). Previously the check
// ran outside the lock, which both raced with those writers and made
// the check-then-write a TOCTOU window.
func (tw *timeWaiter) Stop() error {
	tw.m.Lock()
	defer tw.m.Unlock()

	if tw.state != eventproc.WSRunned {
		return errs.New(
			errs.M("couldn't stop not runned waiter"),
			errs.C(TimerWaiterError, errs.InvalidState),
			errs.D("current_state", tw.state))
	}

	tw.state = eventproc.WSStopped

	close(tw.stopCh)

	return nil
}

// State returns current state of the EventWaiter.
//
// The read takes the mutex because writers (runTimerService,
// processTimerEvent, Stop) all mutate tw.state under it. Without the
// lock here, concurrent reads race with those writes — the race
// detector flags it under stress.
func (tw *timeWaiter) State() eventproc.EventWaiterState {
	tw.m.Lock()
	defer tw.m.Unlock()

	return tw.state
}

// Done returns a channel closed when the service goroutine has exited; nil until
// Service starts it (a registered waiter is always serviced first). EventHub.
// Shutdown waits on it to drain goroutines (ADR-006 v.1 §2.5).
func (tw *timeWaiter) Done() <-chan struct{} {
	return tw.done
}

var _ eventproc.EventWaiter = (*timeWaiter)(nil)
