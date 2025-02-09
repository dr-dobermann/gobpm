package events

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/dr-dobermann/gobpm/internal/exec"
	"github.com/dr-dobermann/gobpm/internal/renv"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
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
		inputSet:   &data.Set{},
	}

	ee := []error{}

	for _, opt := range endEventOptions {
		switch so := opt.(type) {
		case foundation.BaseOption:
			ec.baseOpts = append(ec.baseOpts, opt)

		case endOption, eventOption, data.PropertyOption:
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

func (ee *EndEvent) Node() flow.Node {
	return ee
}

// ------------------ flow.EventNode interface ---------------------------------

func (ee *EndEvent) EventClass() flow.EventClass {
	return flow.EndEventClass
}

// ------------------ flow.SequenceTarget interface ----------------------------

// AcceptIncomingFlow checks if the EndEvent accepts incoming sequence flow sf.
func (ee *EndEvent) AcceptIncomingFlow(sf *flow.SequenceFlow) error {
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
	ers := []error{}

	for _, ed := range ee.definitions {
		if err := ee.emitEvent(re, re.EventProducer(), ed); err != nil {
			ers = append(ers, err)
		}
	}

	if len(ers) != 0 {
		return nil,
			errs.New(
				errs.M("event emitting failed for EndEvent %q[%s]",
					ee.Name(), ee.Id()),
				errs.C(errorClass, errs.OperationFailed),
				errs.E(errors.Join(ers...)))
	}

	return []*flow.SequenceFlow{}, nil
}

// ------------------- scope.NodeDataLoader interface ---------------------------

// RegisterData sends all EndEvent data.Data to the exec.Scope.
func (ee *EndEvent) RegisterData(dp scope.DataPath, s scope.Scope) error {
	ee.dataPath = dp

	return s.LoadData(ee, ee.getEventData()...)
}

// -----------------------------------------------------------------------------

// interfaces check
var (
	_ flow.Node            = (*EndEvent)(nil)
	_ flow.EventNode       = (*EndEvent)(nil)
	_ flow.SequenceTarget  = (*EndEvent)(nil)
	_ exec.NodeExecutor    = (*EndEvent)(nil)
	_ scope.NodeDataLoader = (*EndEvent)(nil)
)
