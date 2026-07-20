package events

import (
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// Escalation represents payload of EscalationEventDefinition.
type Escalation struct {
	structure *data.ItemDefinition
	name      string
	code      string
	foundation.BaseElement
}

// NewEscalation creates a new Escalation object and returns its pointer.
func NewEscalation(
	name, code string,
	item *data.ItemDefinition,
	baseOpts ...options.Option,
) (*Escalation, error) {
	name = strings.TrimSpace(name)
	if err := errs.CheckStr(
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

// EscalationEventDefinition represents an escalation event definition.
type EscalationEventDefinition struct {
	escalation *Escalation
	definition
}

// Compile-time conformance (FIX-011): CloneEventDefinition must match
// flow.EventDefCloner, else the throw-path clone-with-data step silently no-ops.
var (
	_ flow.EventDefinition = (*EscalationEventDefinition)(nil)
	_ flow.EventDefCloner  = (*EscalationEventDefinition)(nil)
)

// NewEscalationEventDefinition creates a new EscalationEventDefinition and
// returns its pointer.
func NewEscalationEventDefinition(
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

// MustEscalationEventDefinition tries to create a new
// EscalationEventDefinition. If error occurred, it fires panic.
func MustEscalationEventDefinition(
	escalation *Escalation,
	baseOpts ...options.Option,
) *EscalationEventDefinition {
	eed, err := NewEscalationEventDefinition(escalation, baseOpts...)
	if err != nil {
		errs.Panic(err.Error())
	}

	return eed
}

// Escalation returns the EscalationEventDefinition's internal escalation
// structure.
func (eed *EscalationEventDefinition) Escalation() *Escalation {
	return eed.escalation
}

// ---------------- flow.EventDefinition interface -----------------------------

// Type implememnts Definition interface for EscalationEventDefinition.
func (eed *EscalationEventDefinition) Type() flow.EventTrigger {
	return flow.TriggerEscalation
}

// CheckItemDefinition check if definition is related with
// data.ItemDefinition with iDefId Id.
func (eed *EscalationEventDefinition) CheckItemDefinition(iDefID string) bool {
	if eed.escalation.structure == nil {
		return false
	}

	return eed.escalation.structure.ID() == iDefID
}

// GetItemsList returns a list of data.ItemDefinition the EventDefinition
// is based on.
// If EventDefinition isn't based on any data.ItemDefinition, empty list
// will be returned.
func (eed *EscalationEventDefinition) GetItemsList() []*data.ItemDefinition {
	idd := make([]*data.ItemDefinition, 0, 1)

	if eed.escalation.structure == nil {
		return idd
	}

	return append(idd, eed.escalation.structure)
}

// CloneEventDefinition clones EventDefinition with dedicated data.ItemDefinition
// list. It satisfies flow.EventDefCloner (was previously named CloneEvent and so
// never satisfied the interface — FIX-011).
func (eed *EscalationEventDefinition) CloneEventDefinition(
	evtData []data.Data,
) (flow.EventDefinition, error) {
	var iDef *data.ItemDefinition

	if len(evtData) != 0 {
		d := evtData[0]

		if d.ItemDefinition().ID() != eed.escalation.structure.ID() {
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
		foundation.WithID(eed.escalation.ID()))
	if err != nil {
		return nil,
			errs.New(
				errs.M("escalation %q[%s] creation failed",
					eed.escalation.name, eed.escalation.ID()),
				errs.E(err))
	}

	ned, err := NewEscalationEventDefinition(ne, foundation.WithID(eed.ID()))
	if err != nil {
		return nil,
			errs.New(
				errs.M("escalation definition #%s cloning failed", eed.ID()),
				errs.E(err))
	}

	return ned, nil
}

// -----------------------------------------------------------------------------
