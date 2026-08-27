package events

import (
	"slices"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// --------------------- flow.AssociationSource --------------------------------

// Outputs returns the catch event's data outputs — the sources its output
// associations push from (§10.4.2). A catch event is an association SOURCE.
func (ce *catchEvent) Outputs() []*data.ItemAwareElement {
	return elementsOf(ce.dataOutputs)
}

// BindOutgoing adds an output association to the catch event. A nil
// association and one already bound are refused.
func (ce *catchEvent) BindOutgoing(oa *data.Association) error {
	aa, err := bindAssociation(ce.outputAssociations, oa, ce.Name())
	if err != nil {
		return err
	}

	ce.outputAssociations = aa

	return nil
}

// --------------------- flow.AssociationTarget --------------------------------

// Inputs returns the throw event's data inputs — the targets its input
// associations fill (§10.4.2). A throw event is an association TARGET.
func (te *throwEvent) Inputs() []*data.ItemAwareElement {
	return elementsOf(te.dataInputs)
}

// BindIncoming adds an input association to the throw event. A nil
// association and one already bound are refused.
func (te *throwEvent) BindIncoming(ia *data.Association) error {
	aa, err := bindAssociation(te.inputAssociations, ia, te.Name())
	if err != nil {
		return err
	}

	te.inputAssociations = aa

	return nil
}

// -----------------------------------------------------------------------------

// elementsOf lists the parameters' item-aware elements, in order.
func elementsOf(pp []*data.Parameter) []*data.ItemAwareElement {
	ee := make([]*data.ItemAwareElement, 0, len(pp))
	for _, p := range pp {
		ee = append(ee, &p.ItemAwareElement)
	}

	return ee
}

// bindAssociation appends a to aa, refusing nil and a duplicate id — the
// task's rule (activities.task.bindAssociation), applied to an event.
func bindAssociation(
	aa []*data.Association, a *data.Association, owner string,
) ([]*data.Association, error) {
	if a == nil {
		return nil, errs.New(
			errs.M("event %q: a nil data association can't be bound", owner),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if slices.ContainsFunc(aa,
		func(da *data.Association) bool { return da.ID() == a.ID() }) {
		return nil, errs.New(
			errs.M("event %q: data association #%s is already bound",
				owner, a.ID()),
			errs.C(errorClass, errs.DuplicateObject))
	}

	return append(aa, a), nil
}

// interface checks: every catch kind is a source, every throw kind a target.
var (
	_ flow.AssociationSource = (*StartEvent)(nil)
	_ flow.AssociationSource = (*IntermediateCatchEvent)(nil)
	_ flow.AssociationSource = (*BoundaryEvent)(nil)
	_ flow.AssociationTarget = (*EndEvent)(nil)
	_ flow.AssociationTarget = (*IntermediateThrowEvent)(nil)
)
