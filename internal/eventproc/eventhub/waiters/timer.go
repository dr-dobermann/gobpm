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
	"github.com/dr-dobermann/gobpm/pkg/monitor"
)

// TimerWatierError is the error class for timer waiter errors.
const TimerWatierError = "TIMER_WAITER_ERRROR"

// timeWaiter defines details of timer event described by
// eDef.
type timeWaiter struct {
	id string

	// base event definition
	eDef flow.EventDefinition

	// hub which owns the Waiter.
	hub eventproc.EventHub

	// event processors expecting defined events
	processors []eventproc.EventProcessor

	// state of the waiter
	state eventproc.EventWaiterState

	// time of the next event fairing
	next time.Time

	// cycles left defined by eventDefinition and updated by every
	// event fired.
	// if the number of cycles isn't defined and timer should fire until the
	// endTime cyclesLeft sets as -1
	cyclesLeft int

	// time duration between events firirng
	duration time.Duration

	m sync.Mutex

	stopCh chan struct{}
}

// NewTimeWaiter creates a new timer event defined by eDef.
func NewTimeWaiter(
	eh eventproc.EventHub,
	ep eventproc.EventProcessor,
	eDefI flow.EventDefinition,
	id string,
) (eventproc.EventWaiter, error) {
	if ep == nil || eDefI == nil || eh == nil {
		return nil,
			errs.New(
				errs.M("couldn't create a Waiter with empty EventProcessor, EventDefinition or EventHub"),
				errs.C(TimerWatierError, errs.InvalidParameter, errs.EmptyNotAllowed))
	}

	eDef, ok := eDefI.(*events.TimerEventDefinition)
	if !ok {
		return nil,
			errs.New(
				errs.M("not an TimerEventDefinition"),
				errs.C(TimerWatierError, errs.TypeCastingError),
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
		processors: []eventproc.EventProcessor{ep},
	}

	if err := tw.parseEDef(eDef, ep); err != nil {
		return nil,
			errs.New(
				errs.M("TimerEventDefinition parsing failed"),
				errs.C(TimerWatierError, errs.OperationFailed),
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
	tm, err := fe.Evaluate(context.Background(), ds)
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
		if ok && tw.next.Before(time.Now()) {
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
		return fmt.Errorf("empty EventProcessor")
	}

	tw.m.Lock()
	defer tw.m.Unlock()

	if idx := slices.Index(tw.processors, ep); idx == -1 {
		tw.processors = append(tw.processors, ep)
	}

	return nil
}

// RemoveEventProcessor removes the ep EventProcessor from waiter's event
// processors list.
func (tw *timeWaiter) RemoveEventProcessor(_ eventproc.EventProcessor) error {
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
			errs.C(TimerWatierError, errs.InvalidState),
			errs.D("current_state", eventproc.WSReady))
	}

	tw.state = eventproc.WSRunned

	if !tw.next.IsZero() {
		tw.duration = time.Until(tw.next)
		tw.cyclesLeft = 0
	}

	if tw.duration <= 0 {
		return errs.New(
			errs.M("waiter duration is not positive"),
			errs.C(TimerWatierError, errs.InvalidState),
			errs.D("waiter_id", tw.ID()),
			errs.D("next_time", tw.next),
			errs.D("duration", tw.duration),
			errs.D("cycles", tw.cyclesLeft))
	}

	tw.stopCh = make(chan struct{})

	go tw.runTimerService(ctx)

	return nil
}

// runTimerService runs the timer waiter service in a background goroutine
func (tw *timeWaiter) runTimerService(ctx context.Context) {
		var m monitor.Writer

		if mv := ctx.Value(monitor.Key); mv != nil {
			m = mv.(monitor.Writer)
		}

		tckr := time.NewTicker(tw.duration)

		for {
			select {
			case <-ctx.Done():
				monitor.Save(m, "timeWaiter", "Waiter stopped by context",
					monitor.D("waiter_id", tw.id))

				close(tw.stopCh)

				tckr.Stop()

				tw.m.Lock()
				tw.state = eventproc.WSStopped
				tw.m.Unlock()

				return

			case <-tw.stopCh:
				fmt.Println("stopping waiter ", tw.id, "...")
				monitor.Save(m, "timeWaiter", "Waiter stopped",
					monitor.D("waiter_id", tw.id))

				tckr.Stop()

				return

			case t := <-tckr.C:
				monitor.Save(m, "timeWaiter", "Waiter catch an event",
					monitor.D("waiter_id", tw.id),
					monitor.D("event_time", t),
					monitor.D("cycles_left", tw.cyclesLeft))

				if err := tw.processTimerEvent(ctx, m); err != nil {
					return
				}
			}
		}
}

// processTimerEvent handles timer event processing with proper locking
func (tw *timeWaiter) processTimerEvent(ctx context.Context, m monitor.Writer) error {
	tw.m.Lock()
	defer tw.m.Unlock()

	for _, ep := range tw.processors {
		if err := ep.ProcessEvent(ctx, tw.eDef); err != nil {
			monitor.Save(m, "timeWaiter", "Event processing failed",
				monitor.D("waiter_id", tw.id),
				monitor.D("error", err))

			tw.state = eventproc.WSFailed
			return err
		}
	}

	if tw.cyclesLeft == 0 {
		// clear processors list on exit
		tw.processors = []eventproc.EventProcessor{}
		tw.state = eventproc.WSEnded
		_ = tw.hub.RemoveWaiter(tw) // ignore error during cleanup
		
		return errs.New(errs.M("timer completed")) // signal completion
	}

	tw.cyclesLeft--
	
	return nil
}

// Stop terminates waiting cycle of the waiter.
func (tw *timeWaiter) Stop() error {
	if tw.state != eventproc.WSRunned {
		return errs.New(
			errs.M("couldn't stop not runned waiter"),
			errs.C(TimerWatierError, errs.InvalidState),
			errs.D("current_state", tw.state))
	}

	tw.m.Lock()
	defer tw.m.Unlock()

	tw.state = eventproc.WSStopped

	close(tw.stopCh)

	return nil
}

// State returns current state of the EventWaiter.
func (tw *timeWaiter) State() eventproc.EventWaiterState {
	return tw.state
}

var _ eventproc.EventWaiter = (*timeWaiter)(nil)
