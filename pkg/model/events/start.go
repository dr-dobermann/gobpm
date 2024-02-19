package events

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/set"
)

var startTriggers = set.New[Trigger](
	TriggerMessage,
	TriggerTimer,
	TriggerConditional,
	TriggerSignal,
)

type StartEvent struct {
	catchEvent

	// This attribute only applies to Start Events of Event Sub-Processes; it is
	// ignored for other Start Events. This attribute denotes whether the
	// Sub-Process encompassing the Event Sub-Process should be canceled or not,
	// If the encompassing Sub-Process is not canceled, multiple instances of
	// the Event Sub-Process can run concurrently. This attribute cannot be
	// applied to Error Events (where it’s always true), or Compensation Events
	// (where it doesn’t apply).
	interrrupting bool
}

// NewStartEvent creates a new StartEvent and returns its pointer on success
// or error if any.
func NewStartEvent(
	id, name string,
	props []data.Property,
	defRef []Definition,
	defs []Definition,
	parallel bool,
	interrupting bool,
	docs ...*foundation.Documentation,
) (*StartEvent, error) {
	// check event definitions to comply with StartEvent triggers.
	for _, d := range defRef {
		if !startTriggers.Has(d.Type()) {
			return nil,
				&errs.ApplicationError{
					Err:     nil,
					Message: "invalid StartEvent trigger in definition_refs",
					Classes: []string{
						eventErrorClass,
						errs.InvalidParameter},
					Details: map[string]string{
						"definition_id": d.Id(),
						"trigger":       string(d.Type()),
					},
				}
		}
	}

	for _, d := range defs {
		if !startTriggers.Has(d.Type()) {
			return nil,
				&errs.ApplicationError{
					Err:     nil,
					Message: "invalid StartEvent trigger in defintions",
					Classes: []string{
						eventErrorClass,
						errs.InvalidParameter},
					Details: map[string]string{
						"definition_id": d.Id(),
						"trigger":       string(d.Type()),
					},
				}
		}
	}

	ce, err := newCatchEvent(id, name, props, defRef, defs, docs...)
	if err != nil {
		return nil, err
	}

	se := StartEvent{
		catchEvent:    *ce,
		interrrupting: interrupting,
	}

	if se.triggers.Count() > 1 && parallel {
		se.parallelMultiple = true
	}

	return &se, nil
}
