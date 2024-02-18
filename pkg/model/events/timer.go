package events

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
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
	timeDate *data.Expression

	// If the trigger is a Timer, then a timeCycle MAY be entered. Timer
	// attributes are mutually exclusive and if any of the other Timer
	// attributes is set, timeCycle MUST NOT be set (if the isExecutable
	// attribute of the Process is set to true).
	// The return type of the attribute timeCycle MUST conform to the ISO-8601
	// format for recurring time interval representations.
	timeCycle *data.Expression

	// If the trigger is a Timer, then a timeDuration MAY be entered. Timer
	// attributes are mutually exclusive and if any of the other Timer
	// attributes is set, timeDuration MUST NOT be set (if the isExecutable
	// attribute of the Process is set to true).
	// The return type of the attribute timeDuration MUST conform to the
	// ISO-8601 format for time interval representations.
	timeDuration *data.Expression
}

// Type implements Definition interface for TimerEventDefinition.
func (*TimerEventDefinition) Type() Trigger {

	return TriggerTimer
}

// NewTimerEventDefinition creates a new TimerEventDefinition and returns its
// pointer if there are no questions to timer parameters.
// If parameters arent' consistent then error returned.
func NewTimerEventDefinition(
	id string,
	tDate, tCycle, tDuration *data.Expression,
	docs ...*foundation.Documentation,
) (*TimerEventDefinition, error) {
	if (tDate != nil && (tCycle != nil || tDuration != nil)) ||
		(tCycle != nil && tDuration != nil) {

		return nil,
			&errs.ApplicationError{
				Message: "doesn't allow to define Timer Data or Cycle or Duration simultaneously",
				Classes: []string{
					eventErrorClass,
					errs.InvalidParameter,
				},
				Details: map[string]string{
					"timer_event_definition_id": id,
				},
			}
	}

	return &TimerEventDefinition{
		definition:   *newDefinition(id, docs...),
		timeDate:     tDate,
		timeCycle:    tCycle,
		timeDuration: tDuration,
	}, nil
}

// MustTimerEventDefinition tries to create a new TimerEventDefinition.
// If error occurs, then panic fired.
func MustTimerEventDefinition(
	id string,
	tDate, tCycle, tDuration *data.Expression,
	docs ...*foundation.Documentation,
) *TimerEventDefinition {
	ted, err := NewTimerEventDefinition(id, tDate, tCycle, tDuration, docs...)
	if err != nil {
		panic(err.Error())
	}

	return ted
}
