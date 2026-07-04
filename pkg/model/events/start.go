package events

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/dr-dobermann/gobpm/pkg/exec"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/renv"
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

// StartEvent represents a start event in a process.
type StartEvent struct {
	// correlationKey, when set, is the CorrelationKey an instantiating message
	// start event correlates on: the engine derives the incoming message's key
	// from this declaration to decide create-or-route-or-join (ADR-016 v.1
	// §2.2/§2.3). nil = no key-based correlation (name-match only).
	correlationKey *bpmncommon.CorrelationKey

	catchEvent

	// This attribute only applies to Start Events of Event Sub-Processes; it is
	// ignored for other Start Events. This attribute denotes whether the
	// Sub-Process encompassing the Event Sub-Process should be canceled or not,
	// If the encompassing Sub-Process is not canceled, multiple instances of
	// the Event Sub-Process can run concurrently. This attribute cannot be
	// applied to Error Events (where it’s always true), or Compensation Events
	// (where it doesn’t apply).
	interrupting bool
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

		case startOption:
			if err := so(&sc); err != nil {
				ee = append(ee, err)
			}

		case EventOption: // *startConfig implements eventConfig
			if err := so(&sc); err != nil {
				ee = append(ee, err)
			}

		case data.PropertyOption: // *startConfig implements data.PropertyAdder
			if err := so(&sc); err != nil {
				ee = append(ee, err)
			}

		default:
			ee = append(ee, fmt.Errorf("inappropriate option type: %s",
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
	return se.interrupting
}

// ------------------ flow.SequenceSource interface ----------------------------

// SupportOutgoingFlow checks if it allowed to source sf from the StartEvent
func (se *StartEvent) SupportOutgoingFlow(_ *flow.SequenceFlow) error {
	// StartEvent don't restricted any source sequence flow from it
	return nil
}

// Node returns the node interface.
func (se *StartEvent) Node() flow.Node {
	return se
}

// Clone returns a per-instance copy of the StartEvent: the embedded catchEvent
// is cloned (config shared by reference, fresh Event shell, zero dataPath, empty
// flows, no container) and the interrupting flag is copied as configuration.
func (se *StartEvent) Clone() (flow.Node, error) {
	ce, err := se.clone()
	if err != nil {
		return nil, err
	}

	return &StartEvent{
		catchEvent:     ce,
		correlationKey: se.correlationKey,
		interrupting:   se.interrupting,
	}, nil
}

// CorrelationKey returns the CorrelationKey this start event correlates on, or
// nil for name-match only (ADR-016 v.1 §2.2). The engine reads it structurally
// to derive an incoming message's key for create-or-route-or-join.
func (se *StartEvent) CorrelationKey() *bpmncommon.CorrelationKey {
	return se.correlationKey
}

// EventClass returns the event class.
func (se *StartEvent) EventClass() flow.EventClass {
	return flow.StartEventClass
}

// ----------------- exec.NodeExecutor interface -------------------------------

// Exec runs the StartNode and saves all its Output data.Associations.
func (se *StartEvent) Exec(
	_ context.Context,
	_ renv.RuntimeEnvironment,
) ([]*flow.SequenceFlow, error) {
	return append([]*flow.SequenceFlow{}, se.Outgoing()...), nil
}

// -----------------------------------------------------------------------------

// interfaces checks
var (
	_ flow.SequenceSource   = (*StartEvent)(nil)
	_ flow.Node             = (*StartEvent)(nil)
	_ flow.EventNode        = (*StartEvent)(nil)
	_ exec.NodeExecutor     = (*StartEvent)(nil)
	_ exec.NodeDataProducer = (*StartEvent)(nil)
)
