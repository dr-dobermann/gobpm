package events

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// *****************************************************************************
type TimerEventDefinition struct {
	definition

	// If the trigger is a Timer, then a timeDate MAY be entered. Timer
	// attributes are mutually exclusive and if any of the other Timer
	// attributes is set, timeDate MUST NOT be set (if the isExecutable
	// attribute of the Process is set to true).
	// The return type of the attribute timeDate MUST conform to the ISO-8601
	// format for date and time representations.
	timeDate data.FormalExpression

	// If the trigger is a Timer, then a timeCycle MAY be entered. Timer
	// attributes are mutually exclusive and if any of the other Timer
	// attributes is set, timeCycle MUST NOT be set (if the isExecutable
	// attribute of the Process is set to true).
	// The return type of the attribute timeCycle MUST conform to the ISO-8601
	// format for recurring time interval representations.
	timeCycle data.FormalExpression

	// If the trigger is a Timer, then a timeDuration MAY be entered. Timer
	// attributes are mutually exclusive and if any of the other Timer
	// attributes is set, timeDuration MUST NOT be set (if the isExecutable
	// attribute of the Process is set to true).
	// The return type of the attribute timeDuration MUST conform to the
	// ISO-8601 format for time interval representations.
	timeDuration data.FormalExpression
}

// Type implements Definition interface for TimerEventDefinition.
func (*TimerEventDefinition) Type() flow.EventTrigger {
	return flow.TriggerTimer
}

// NewTimerEventDefinition creates a new TimerEventDefinition and returns its
// pointer if there are no questions to timer parameters.
// If parameters arent' consistent then error returned.
func NewTimerEventDefinition(
	tDate, tCycle, tDuration data.FormalExpression,
	baseOpts ...options.Option,
) (*TimerEventDefinition, error) {
	if tDate == nil && tCycle == nil && tDuration == nil {
		return nil,
			errs.New(
				errs.M("all timer expression couldn't be empty"),
				errs.C(errorClass, errs.InvalidParameter))
	}

	if (tDate != nil && (tCycle != nil || tDuration != nil)) ||
		(tCycle != nil && tDuration != nil) {

		return nil,
			errs.New(
				errs.M("doesn't allow to define Timer Data or Cycle or Duration simultaneously"),
				errs.C(errorClass, errs.InvalidParameter))
	}

	for _, t := range []struct {
		fe   data.FormalExpression
		name string
	}{
		{tDate, "date"},
		{tCycle, "cycle"},
		{tDuration, "duration"},
	} {
		if t.fe != nil && !isTimeType(t.fe) {
			return nil,
				errs.New(
					errs.M("expression result isn't time.Time type"),
					errs.C(errorClass, errs.InvalidObject),
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

// Date return the Timer's date.
func (ted *TimerEventDefinition) Date() data.FormalExpression {
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

func isTimeType(fe data.FormalExpression) bool {
	if fe != nil && fe.ResultType() == "Time" {
		return true
	}

	return false
}
