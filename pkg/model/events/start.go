package events

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/dr-dobermann/gobpm/internal/exec"
	"github.com/dr-dobermann/gobpm/internal/renv"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/set"
)

var startTriggers = set.New[flow.EventTrigger](
	flow.TriggerCompensation, // only for in-line Sub-Processes
	flow.TriggerConditional,
	flow.TriggerError,      // only for in-line Sub-Processes
	flow.TriggerEscalation, // only for in-line Sub-Processes
	flow.TriggerMessage,
	flow.TriggerSignal,
	flow.TriggerTimer,
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
// or error on failure.
func NewStartEvent(
	name string,
	startEventOptions ...options.Option,
) (*StartEvent, error) {
	sc := startConfig{
		name:          name,
		props:         map[string]*data.Property{},
		parallel:      false,
		interrurpting: false,
		baseOpts:      []options.Option{},
		defs:          []flow.EventDefinition{},
		dataOutputs:   make(map[string]*data.Parameter),
	}

	ee := []error{}

	for _, opt := range startEventOptions {
		switch so := opt.(type) {
		case foundation.BaseOption:
			sc.baseOpts = append(sc.baseOpts, opt)

		case startOption, eventOption, data.PropertyOption:
			if err := so.Apply(&sc); err != nil {
				ee = append(ee, err)
			}

		default:
			ee = append(ee, fmt.Errorf("innapropriate option type: %s",
				reflect.TypeOf(so).Name()))
		}
	}

	if err := sc.Validate(); err != nil {
		ee = append(ee, err)
	}

	if len(ee) > 0 {
		return nil, errors.Join(ee...)
	}

	return sc.startEvent()
}

// IsInterrupting returns interrupting setting of the StartEvent.
func (se *StartEvent) IsInterrupting() bool {
	return se.interrrupting
}

// ------------------ flow.SequenceSource interface ----------------------------

// SuportOutgoingFlow checks if it allowed to source sf from the StartEvent
func (se *StartEvent) SupportOutgoingFlow(sf *flow.SequenceFlow) error {
	// StartEvent don't restricted any source sequence flow from it
	return nil
}

// ----------------- flow.Node interface ---------------------------------------
func (se *StartEvent) Node() flow.Node {
	return se
}

// ----------------- flow.EventNode interface ----------------------------------
func (se *StartEvent) EventClass() flow.EventClass {
	return flow.StartEventClass
}

// ----------------- exec.NodeExecutor interface -------------------------------

// Exec runs the StartNode and saves all its Output data.Associations.
func (se *StartEvent) Exec(
	ctx context.Context,
	re renv.RuntimeEnvironment,
) ([]*flow.SequenceFlow, error) {
	return append([]*flow.SequenceFlow{}, se.Outgoing()...), nil
}

// ------------------- exec.NodeDataLoader interface ---------------------------

// RegisterData sends all StartEvent data.Data to the exec.Scope.
func (se *StartEvent) RegisterData(dp scope.DataPath, s scope.Scope) error {
	se.dataPath = dp

	return s.LoadData(se, se.catchEvent.getEventData()...)
}

// -----------------------------------------------------------------------------

// interfaces checks
var (
	_ flow.SequenceSource  = (*StartEvent)(nil)
	_ flow.Node            = (*StartEvent)(nil)
	_ flow.EventNode       = (*StartEvent)(nil)
	_ exec.NodeExecutor    = (*StartEvent)(nil)
	_ scope.NodeDataLoader = (*StartEvent)(nil)
)
