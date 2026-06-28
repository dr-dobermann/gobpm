package events

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/exec"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/renv"
	"github.com/dr-dobermann/gobpm/pkg/set"
)

var endTriggers = set.New(
	flow.TriggerCancel,
	flow.TriggerCompensation,
	flow.TriggerError,
	flow.TriggerEscalation,
	flow.TriggerMessage,
	flow.TriggerSignal,
	flow.TriggerTerminate,
)

// EndEvent represents a BPMN end event.
type EndEvent struct {
	throwEvent
}

// NewEndEvent creates a new EndEvent and returns its pointer on success
// or error on failure.
//
// Available options are:
//   - foundation.WithId
//   - foundation.WithDocs
//   - data.WithProperties
//   - events.WithTerminateTrigger
//   - events.WithCancelTrigger
//   - events.WithCompensationTrigger
//   - events.WithConditionalTrigger
//   - events.WithErrorTrigger
//   - events.WithEscalationTrigger
//   - events.WithMessageTrigger
//   - evnets.WithSignalTrigger
//   - events.WithTimerTrigger
func NewEndEvent(
	name string,
	endEventOptions ...options.Option,
) (*EndEvent, error) {
	ec := endConfig{
		name:       name,
		props:      map[string]*data.Property{},
		baseOpts:   []options.Option{},
		defs:       []flow.EventDefinition{},
		dataInputs: map[string]*data.Parameter{},
	}

	ee := []error{}

	for _, opt := range endEventOptions {
		switch so := opt.(type) {
		case foundation.BaseOption:
			ec.baseOpts = append(ec.baseOpts, opt)

		case endOption, EventOption, data.PropertyOption:
			if err := so.Apply(&ec); err != nil {
				ee = append(ee, err)
			}

		default:
			ee = append(ee, fmt.Errorf("innapropriate option type: %s",
				reflect.TypeOf(so).Name()))
		}
	}

	if err := ec.Validate(); err != nil {
		ee = append(ee, err)
	}

	if len(ee) > 0 {
		return nil, errors.Join(ee...)
	}

	return ec.endEvent()
}

// ------------------ flow.Node interface --------------------------------------

// Node returns the EndEvent as a flow node.
func (ee *EndEvent) Node() flow.Node {
	return ee
}

// Clone returns a per-instance copy of the EndEvent: the embedded throwEvent is
// cloned (config shared by reference, fresh Event shell, zero dataPath, empty
// flows, no container).
func (ee *EndEvent) Clone() flow.Node {
	return &EndEvent{
		throwEvent: ee.clone(),
	}
}

// ------------------ flow.EventNode interface ---------------------------------

// EventClass returns the event class for EndEvent.
func (ee *EndEvent) EventClass() flow.EventClass {
	return flow.EndEventClass
}

// ------------------ flow.SequenceTarget interface ----------------------------

// AcceptIncomingFlow checks if the EndEvent accepts incoming sequence flow sf.
func (ee *EndEvent) AcceptIncomingFlow(_ *flow.SequenceFlow) error {
	// EndEvent has no restrictions on incoming sequence flows
	return nil
}

// ----------------- exec.NodeExecutor interface -------------------------------

// Exec runs the EndEvent.
//
// All events defined for the EndEvent should be thrown out or EndEvent
// execution failed.
func (ee *EndEvent) Exec(
	ctx context.Context,
	re renv.RuntimeEnvironment,
) ([]*flow.SequenceFlow, error) {
	// An Error End Event ends the process in error (BPMN §10.5.6). In 0.1.0's single
	// scope there is no enclosing Sub-Process to catch the thrown error, so it
	// resolves to an instance fault carrying the errorCode (SRD-029 FR-10, ADR-006
	// v.2 §2.6 engine note). The error definition itself is not propagated through
	// the event bus (no catcher); any co-located non-error definitions still emit
	// before the fault.
	var errCode string

	ers := []error{}

	for _, ed := range ee.definitions {
		if eed, ok := ed.(*ErrorEventDefinition); ok {
			errCode = eed.Error().ErrorCode()

			continue
		}

		if err := ee.emitDefinition(ctx, re, ed); err != nil {
			ers = append(ers, err)
		}
	}

	if len(ers) != 0 {
		return nil,
			errs.New(
				errs.M("event emitting failed for EndEvent %q[%s]",
					ee.Name(), ee.ID()),
				errs.C(errorClass, errs.OperationFailed),
				errs.E(errors.Join(ers...)))
	}

	// finding an error definition, end the process in error (FR-10): the track
	// fails and — an end event carries no boundary — the instance faults.
	if errCode != "" {
		return nil, &BpmnError{Code: errCode}
	}

	return []*flow.SequenceFlow{}, nil
}

// -----------------------------------------------------------------------------

// interfaces check
var (
	_ flow.Node             = (*EndEvent)(nil)
	_ flow.EventNode        = (*EndEvent)(nil)
	_ flow.SequenceTarget   = (*EndEvent)(nil)
	_ exec.NodeExecutor     = (*EndEvent)(nil)
	_ exec.NodeDataConsumer = (*EndEvent)(nil)
)
