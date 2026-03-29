package events

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
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
		(tDate == nil && (tCycle == nil || tDuration == nil)) {
		return nil,
			errs.New(
				errs.M("doesn't allow to define Timer Data or Cycle and Duration simultaneously"),
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
