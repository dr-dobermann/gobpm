package events

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// *****************************************************************************
//
// Escalation represents payload of EscalationEventDefinition.
type Escalation struct {
	foundation.BaseElement

	name string
	code string

	structure *data.ItemDefinition
}

// NewEscalation creates a new Escalation object and returns its pointer.
func NewEscalation(
	name, code string,
	item *data.ItemDefinition,
	baseOpts ...options.Option,
) (*Escalation, error) {
	name = trim(name)
	if err := checkStr(
		name,
		"name should be provided for escalation"); err != nil {

		return nil, err
	}

	code = trim(code)

	if item == nil {
		return nil,
			&errs.ApplicationError{
				Message: "empty itemDefiniiton isn' allowed",
				Classes: []string{
					errorClass,
					errs.InvalidParameter}}
	}

	be, err := foundation.NewBaseElement(baseOpts...)
	if err != nil {
		return nil,
			&errs.ApplicationError{
				Err:     err,
				Message: "escalation creation error",
				Classes: []string{
					errorClass,
					errs.BulidingFailed,
				},
			}
	}

	return &Escalation{
		BaseElement: *be,
		name:        name,
		code:        code,
		structure:   item,
	}, nil
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

// Type implememnts Definition interface for EscalationEventDefinition.
func (e *EscalationEventDefinition) Type() Trigger {
	return TriggerEscalation
}

// NewEscalationEventDefintion creates a new EscalationEventDefintion and
// returns its pointer.
func NewEscalationEventDefintion(
	escalation *Escalation,
	baseOpts ...options.Option,
) (*EscalationEventDefinition, error) {
	if escalation == nil {
		return nil,
			&errs.ApplicationError{
				Message: "empty escalation isn't allowed",
				Classes: []string{
					errorClass,
					errs.InvalidParameter}}
	}

	d, err := newDefinition(baseOpts...)
	if err != nil {
		return nil,
			&errs.ApplicationError{
				Err:     err,
				Message: "escalation event definition building error",
				Classes: []string{
					errorClass,
					errs.BulidingFailed}}
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
