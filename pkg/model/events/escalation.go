package events

import (
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/helpers"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// *****************************************************************************
//
// Escalation represents payload of EscalationEventDefinition.
type Escalation struct {
	foundation.BaseElement

	// The descriptive name of the Escalation.
	name string

	// For an End Event:
	//   - If the Result is an Escalation, then the escalationCode
	//     MUST be supplied.
	//   - For an Intermediate Event within normal flow:
	//     - If the trigger is an Escalation, then the escalationCode
	//        MUST be entered.
	//   - For an Intermediate Event attached to the boundary of an Activity:
	//       - If the trigger is an Escalation, then the escalationCode MAY
	//         be entered. This Event “catches” the Escalation. If there is no
	//         escalationCode, then any Escalation SHALL trigger the
	//         Event. If there is an escalationCode, then only an Escalation
	//         that matches the escalationCode SHALL trigger the Event.
	code string

	// An ItemDefinition is used to define the “payload” of the Escalation.
	structure *data.ItemDefinition
}

// NewEscalation creates a new Escalation object and returns its pointer.
func NewEscalation(
	name, code string,
	item *data.ItemDefinition,
	baseOpts ...options.Option,
) (*Escalation, error) {
	name = strings.TrimSpace(name)
	if err := helpers.CheckStr(
		name,
		"name should be provided for escalation",
		errorClass,
	); err != nil {

		return nil, err
	}

	code = strings.TrimSpace(code)

	if item == nil {
		return nil,
			errs.New(
				errs.M("empty itemDefiniiton isn' allowed"),
				errs.C(errorClass, errs.InvalidParameter))
	}

	be, err := foundation.NewBaseElement(baseOpts...)
	if err != nil {
		return nil, err
	}

	return &Escalation{
		BaseElement: *be,
		name:        name,
		code:        code,
		structure:   item,
	}, nil
}

// MustEscalation tries to create a new Escalation. It panics on failure.
func MustEscalation(
	name, code string,
	item *data.ItemDefinition,
	baseOpts ...options.Option,
) *Escalation {
	e, err := NewEscalation(name, code, item, baseOpts...)
	if err != nil {
		errs.Panic(err)
		return nil
	}

	return e
}

// Name returns the Escalation's name.
func (e *Escalation) Name() string {
	return e.name
}

// Code returns the Escalation's code.
func (e *Escalation) Code() string {
	return e.code
}

// Item returns the Escaltion's data structure.
func (e *Escalation) Item() *data.ItemDefinition {
	return e.structure
}

// *****************************************************************************
type EscalationEventDefinition struct {
	definition

	// If the trigger is an Escalation, then an Escalation payload MAY be
	// provided.
	escalation *Escalation
}

// NewEscalationEventDefintion creates a new EscalationEventDefintion and
// returns its pointer.
func NewEscalationEventDefintion(
	escalation *Escalation,
	baseOpts ...options.Option,
) (*EscalationEventDefinition, error) {
	if escalation == nil {
		return nil,
			errs.New(
				errs.M("empty escalation isn't allowed"),
				errs.C(errorClass, errs.InvalidParameter))
	}

	d, err := newDefinition(baseOpts...)
	if err != nil {
		return nil, err
	}

	return &EscalationEventDefinition{
		definition: *d,
		escalation: escalation,
	}, nil
}

// Escalation returns the EscalationEventDefinition's internal escalation
// structure.
func (eed *EscalationEventDefinition) Escalation() *Escalation {
	return eed.escalation
}

// ---------------- flow.EventDefinition interface -----------------------------

// Type implememnts Definition interface for EscalationEventDefinition.
func (e *EscalationEventDefinition) Type() flow.EventTrigger {
	return flow.TriggerEscalation
}

// CheckItemDefinition check if definition is related with
// data.ItemDefinition with iDefId Id.
func (eed *EscalationEventDefinition) CheckItemDefinition(iDefId string) bool {
	if eed.escalation.structure == nil {
		return false
	}

	return eed.escalation.structure.Id() == iDefId
}

// GetItemList returns a list of data.ItemDefinition the EventDefinition
// is based on.
// If EventDefiniton isn't based on any data.ItemDefiniton, empty list
// wil be returned.
func (eed *EscalationEventDefinition) GetItemsList() []*data.ItemDefinition {
	idd := []*data.ItemDefinition{}

	if eed.escalation.structure == nil {
		return idd
	}

	return append(idd, eed.escalation.structure)

}

// CloneEvent clones EventDefinition with dedicated data.ItemDefinition
// list.
func (eed *EscalationEventDefinition) CloneEvent(
	evtData []data.Data,
) (flow.EventDefinition, error) {
	var iDef *data.ItemDefinition

	if len(evtData) != 0 {
		d := evtData[0]

		if d.ItemDefinition().Id() != eed.escalation.structure.Id() {
			return nil,
				errs.New(
					errs.M("escalation itemDefinition and data itemDefinition have different ids"))
		}

		iDef = d.ItemDefinition()
	}

	ne, err := NewEscalation(
		eed.escalation.name,
		eed.escalation.code,
		iDef,
		foundation.WithId(eed.escalation.Id()))
	if err != nil {
		return nil,
			errs.New(
				errs.M("escalation %q[%s] creation failed",
					eed.escalation.name, eed.escalation.Id()),
				errs.E(err))
	}

	ned, err := NewEscalationEventDefintion(ne, foundation.WithId(eed.Id()))
	if err != nil {
		return nil,
			errs.New(
				errs.M("escalation definition #%s cloning failed", eed.Id()),
				errs.E(err))
	}

	return ned, nil
}

// -----------------------------------------------------------------------------

// interface check
var (
	_ flow.EventDefinition = (*EscalationEventDefinition)(nil)
)
