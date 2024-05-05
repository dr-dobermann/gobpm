package events

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/dr-dobermann/gobpm/internal/exec"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/set"
)

var endTriggers = set.New[flow.EventTrigger](
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
// It tries to load its input data.Parameter from the input data associations
// and fill its event definitions by their data.Data.
// All events defined for the EndEvent should be thrown out or EndEvent
// execution failed.
func (ee *EndEvent) Exec(
	ctx context.Context,
	re exec.RuntimeEnvironment,
) ([]*flow.SequenceFlow, error) {
	if err := ee.throwEvent.fillInputs(); err != nil {
		return nil,
			errs.New(
				errs.M("input parameters loading failed for EndEvent %q[%s]",
					ee.Name(), ee.Id()),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(err))
	}

	ers := []error{}

	for _, ed := range ee.definitions {
		if err := ee.emitEvent(re.Scope(), re.EventProducer(), ed); err != nil {
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

// ------------------- exec.NodeDataLoader interface ---------------------------

// RegisterData sends all EndEvent data.Data to the exec.Scope.
func (ee *EndEvent) RegisterData(dp exec.DataPath, s exec.Scope) error {
	ee.dataPath = dp

	return s.LoadData(ee, ee.throwEvent.getEventData()...)
}

// -----------------------------------------------------------------------------
