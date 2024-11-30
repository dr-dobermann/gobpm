package waiters

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

const TimerWatierError = "TIMER_WAITER_ERRROR"

// timeWaiter defines details of timer event described by
// eDef.
type timeWaiter struct {
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
}

// NewTimeWaiter creates a new timer event defined by eDef.
func NewTimeWaiter(
	ep eventproc.EventProcessor,
	eDefI flow.EventDefinition,
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

	tw := timeWaiter{
		eDef:      eDef,
		processor: ep,
		state:     eventproc.WSReady,
	}

	err := parseEDef(eDef, &tw)
	if err != nil {
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
	var ok bool

	ds, ok := tw.processor.(data.Source)
	if !ok {
		return fmt.Errorf(
			"EventProcessor #%s doesn't implement data.Source interface",
			tw.processor.Id())
	}

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

		case "Cycle":
			tw.cyclesLeft, ok = tm.Get().(int)

		case "Duration":
			tw.duration, ok = tm.Get().(time.Duration)
		}
		if !ok {
			return fmt.Errorf(
				"%s property of TimerEventDefintion #%s casting error",
				name, eDef.Id())
		}
	}

	return nil
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
	return fmt.Errorf("not implemented yet")
}

// Stop terminates waiting cycle of the waiter.
func (tw *timeWaiter) Stop() error {
	return fmt.Errorf("not implemented yet")
}

// State returns current state of the EventWaiter.
func (tw *timeWaiter) State() eventproc.EventWaiterState {
	return tw.state
}

// ----------------------------------------------------------------------------
// interfaces check

var _ eventproc.EventWaiter = (*timeWaiter)(nil)
