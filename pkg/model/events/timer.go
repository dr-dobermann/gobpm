package events

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// TimerEventDefinition represents a timer event definition.
type TimerEventDefinition struct {
	timeDate     data.FormalExpression
	timeCycle    data.FormalExpression
	timeDuration data.FormalExpression
	definition
}

// Type implements Definition interface for TimerEventDefinition.
func (*TimerEventDefinition) Type() flow.EventTrigger {
	return flow.TriggerTimer
}

// NewTimerEventDefinition creates a TimerEventDefinition from the three timer
// attributes BPMN defines as mutually exclusive (§10.5.5, Table 10.101), and
// returns an error if the combination isn't one the engine can schedule.
//
// Exactly one of three forms is accepted:
//
//   - tDate alone — an absolute deadline: the timer fires once at that moment.
//   - tDuration alone — a relative deadline: the timer fires once that long
//     after it is armed.
//   - tCycle WITH tDuration — a recurrence: tCycle is the repetition count and
//     tDuration the interval between firings.
//
// The recurrence is where the model departs from the XML notation. BPMN packs
// both numbers into one ISO 8601 string on timeCycle (R3/PT10H); the engine
// carries them as two typed expressions instead of a parsed string. That is
// why tDuration is required alongside tCycle, and why tCycle alone is refused —
// a repetition count with no interval has nothing to schedule. Both spellings
// denote the same schedule.
//
// Each expression's evaluated result must match its attribute: time.Time for
// tDate, int for tCycle, time.Duration for tDuration.
func NewTimerEventDefinition(
	tDate, tCycle, tDuration data.FormalExpression,
	baseOpts ...options.Option,
) (*TimerEventDefinition, error) {
	if tDate == nil && tCycle == nil && tDuration == nil {
		return nil,
			errs.New(
				errs.M("NewTimerEventDefinition: a Timer needs timeDate, "+
					"timeDuration, or timeCycle with timeDuration"),
				errs.C(errorClass, errs.InvalidParameter))
	}

	if tDate != nil && (tCycle != nil || tDuration != nil) {
		return nil,
			errs.New(
				errs.M("NewTimerEventDefinition: timeDate is mutually "+
					"exclusive with timeCycle and timeDuration "+
					"(BPMN Table 10.101)"),
				errs.C(errorClass, errs.InvalidParameter))
	}

	if tDate == nil && tDuration == nil {
		return nil,
			errs.New(
				errs.M("NewTimerEventDefinition: timeCycle needs timeDuration "+
					"as its interval — a recurrence is carried as "+
					"(count, interval)"),
				errs.C(errorClass, errs.InvalidParameter))
	}

	for _, t := range []struct {
		fe          data.FormalExpression
		name, tName string
	}{
		{tDate, "date", "Time"},
		{tCycle, "cycle", "int"},
		{tDuration, "duration", "Duration"},
	} {
		if t.fe != nil && t.fe.ResultType() != t.tName {
			return nil,
				errs.New(
					errs.M("expression result isn't desired type"),
					errs.C(errorClass, errs.InvalidObject),
					errs.D("expected_type", t.tName),
					errs.D("expr_type", t.fe.ResultType()),
					errs.D("time_type", t.name))
		}
	}

	d, err := newDefinition(baseOpts...)
	if err != nil {
		return nil, err
	}

	return &TimerEventDefinition{
		definition:   *d,
		timeDate:     tDate,
		timeCycle:    tCycle,
		timeDuration: tDuration,
	}, nil
}

// MustTimerEventDefinition tries to create a new TimerEventDefinition.
// If error occurs, then panic fired.
func MustTimerEventDefinition(
	tDate, tCycle, tDuration data.FormalExpression,
	baseOpts ...options.Option,
) *TimerEventDefinition {
	ted, err := NewTimerEventDefinition(tDate, tCycle, tDuration, baseOpts...)
	if err != nil {
		errs.Panic(err.Error())
	}

	return ted
}

// CloneForInstance returns a per-instance copy of the TimerEventDefinition
// with a FRESH id, sharing the (immutable) timer expressions by reference.
// Node cloning (Event.clone) uses it so each process instance's timer catch
// registers a DISTINCT EventHub waiter (keyed by eDef id): without it
// concurrent instances waiting on the same timer would share one waiter and a
// single timer occurrence would resume them all (FIX-004; the timer analog of
// MessageEventDefinition.CloneForInstance). A timer carries no payload, so
// there is no fire-path CloneEventDefinition to keep the id stable — only the
// registration identity must be per-instance. Canary:
// TestTimerReceiverPerInstanceClone.
func (ted *TimerEventDefinition) CloneForInstance() flow.EventDefinition {
	return &TimerEventDefinition{
		definition:   definition{BaseElement: foundation.EmptyBaseElement()},
		timeDate:     ted.timeDate,
		timeCycle:    ted.timeCycle,
		timeDuration: ted.timeDuration,
	}
}

// Time return the Timer's time.
func (ted *TimerEventDefinition) Time() data.FormalExpression {
	return ted.timeDate
}

// Cycle return the Timer's cycle.
func (ted *TimerEventDefinition) Cycle() data.FormalExpression {
	return ted.timeCycle
}

// Duration return the Timer's duration.
func (ted *TimerEventDefinition) Duration() data.FormalExpression {
	return ted.timeDuration
}
