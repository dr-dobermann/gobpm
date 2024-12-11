package waiters

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/monitor"
)

const TimerWatierError = "TIMER_WAITER_ERRROR"

// timeWaiter defines details of timer event described by
// eDef.
type timeWaiter struct {
	id string

	// base event definition
	eDef flow.EventDefinition

	// event processor expecting defined events
	processor eventproc.EventProcessor

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

	stopCh chan struct{}
}

// NewTimeWaiter creates a new timer event defined by eDef.
func NewTimeWaiter(
	ep eventproc.EventProcessor,
	eDefI flow.EventDefinition,
	id string,
) (eventproc.EventWaiter, error) {
	if ep == nil || eDefI == nil {
		return nil,
			errs.New(
				errs.M("EventProcessor or EventDefinition is empty"),
				errs.C(TimerWatierError, errs.InvalidParameter, errs.EmptyNotAllowed))
	}

	eDef, ok := eDefI.(*events.TimerEventDefinition)
	if !ok {
		return nil,
			errs.New(
				errs.M("not an TimerEventDefinition"),
				errs.C(TimerWatierError, errs.TypeCastingError),
				errs.D("event_defintion_type", reflect.TypeOf(eDefI)))
	}

	id = strings.TrimSpace(id)
	if id == "" {
		id = foundation.GenerateId()
	}

	tw := timeWaiter{
		id:        id,
		eDef:      eDef,
		processor: ep,
		state:     eventproc.WSReady,
	}

	if err := parseEDef(eDef, &tw); err != nil {
		return nil,
			errs.New(
				errs.M("TimerEventDefinition parsing failed"),
				errs.C(TimerWatierError, errs.OperationFailed),
				errs.E(err))
	}

	return &tw, nil
}

// parseEDef parsing TimerEventDefinition and fills timeWaiter structure
// with appropriate values.
func parseEDef(
	eDef *events.TimerEventDefinition,
	tw *timeWaiter,
) error {
	var (
		ok bool
		ds data.Source
	)

	ds, _ = tw.processor.(data.Source)

	for name, fe := range map[string]data.FormalExpression{
		"Time":     eDef.Time(),
		"Cycle":    eDef.Cycle(),
		"Duration": eDef.Duration(),
	} {
		if fe == nil {
			continue
		}

		tm, err := fe.Evaluate(context.Background(), ds)
		if err != nil {
			return fmt.Errorf(
				"couldn't evaluate TimerEventDefintion #%s %s value: %w",
				eDef.Id(), name, err)
		}

		switch name {
		case "Time":
			tw.next, ok = tm.Get().(time.Time)
			if ok && tw.next.Before(time.Now()) {
				return fmt.Errorf("couldn't use past time as a timer")
			}

		case "Cycle":
			tw.cyclesLeft, ok = tm.Get().(int)
			if ok && tw.cyclesLeft == 0 {
				return fmt.Errorf("cycle isn't defined")
			}

		case "Duration":
			tw.duration, ok = tm.Get().(time.Duration)
			if ok && tw.duration == 0 {
				return fmt.Errorf("duration isn't defined")
			}
		}

		if !ok {
			return fmt.Errorf(
				"%s property of TimerEventDefintion #%s casting error",
				name, eDef.Id())
		}
	}

	return nil
}

// -------------------------- foundation.Identifyer interface -----------------

func (tw *timeWaiter) Id() string {
	return tw.id
}

// -------------------------- eventproc.EventWaiter interface -----------------
// EventDefinition returns underlaying event definition.
func (tw *timeWaiter) EventDefinition() flow.EventDefinition {
	return tw.eDef
}

// EventProcessor returns the EventProcessor expecting the registered
// EventDefinition.
func (tw *timeWaiter) EventProcessor() eventproc.EventProcessor {
	return tw.processor
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
			errs.D("waiter_id", tw.Id()),
			errs.D("next_time", tw.next),
			errs.D("duration", tw.duration),
			errs.D("cycles", tw.cyclesLeft))
	}

	tw.stopCh = make(chan struct{})

	w := func() {
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

				tw.state = eventproc.WSEnded

				return

			case <-tw.stopCh:
				monitor.Save(m, "timeWaiter", "Waiter stopped",
					monitor.D("waiter_id", tw.id))

				tckr.Stop()

				tw.state = eventproc.WSEnded

				return

			case t := <-tckr.C:
				monitor.Save(m, "timeWaiter", "Waiter catch an event",
					monitor.D("waiter_id", tw.id),
					monitor.D("event_time", t),
					monitor.D("cycles_left", tw.cyclesLeft))

				if err := tw.processor.ProcessEvent(ctx, tw.eDef); err != nil {
					monitor.Save(m, "timeWaiter", "Event processing failed",
						monitor.D("waiter_id", tw.id),
						monitor.D("error", err))

					tw.state = eventproc.WSFailed

					return
				}

				if tw.cyclesLeft == 0 {
					tckr.Stop()

					tw.state = eventproc.WSEnded

					return
				}

				tw.cyclesLeft--
			}
		}
	}

	go w()

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

	close(tw.stopCh)

	return nil
}

// State returns current state of the EventWaiter.
func (tw *timeWaiter) State() eventproc.EventWaiterState {
	return tw.state
}
