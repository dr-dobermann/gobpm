package events

import "github.com/dr-dobermann/gobpm/pkg/model/data"

type TimerEventDefinition struct {
	Definition

	// If the trigger is a Timer, then a timeDate MAY be entered. Timer
	// attributes are mutually exclusive and if any of the other Timer
	// attributes is set, timeDate MUST NOT be set (if the isExecutable
	// attribute of the Process is set to true).
	// The return type of the attribute timeDate MUST conform to the ISO-8601
	// format for date and time representations.
	TimeDate *data.Expression

	// If the trigger is a Timer, then a timeCycle MAY be entered. Timer
	// attributes are mutually exclusive and if any of the other Timer
	// attributes is set, timeCycle MUST NOT be set (if the isExecutable
	// attribute of the Process is set to true).
	// The return type of the attribute timeCycle MUST conform to the ISO-8601
	// format for recurring time interval representations.
	TimeCycle *data.Expression

	// If the trigger is a Timer, then a timeDuration MAY be entered. Timer
	// attributes are mutually exclusive and if any of the other Timer
	// attributes is set, timeDuration MUST NOT be set (if the isExecutable
	// attribute of the Process is set to true).
	// The return type of the attribute timeDuration MUST conform to the
	// ISO-8601 format for time interval representations.
	TimeDuration *data.Expression
}
