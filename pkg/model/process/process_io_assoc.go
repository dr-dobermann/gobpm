package process

import (
	"slices"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// AssociateInput wires a Start Event's data output into the process's
// declared input named inputName — the standard's process-level special
// case: the process DataInputs are targets of its Start Event's output
// associations (§10.4.2 p224), so a Message-Flow-triggered launch fills the
// same contract a Call Activity binds directly (ADR-040 v.2 §2.7). from must
// be a Start Event of this process and sourceID the id of one of its data
// outputs — the element, not its item, since two definitions may share an
// item; the mirror of AssociateOutput's targetID.
// The association's target is named after the input — its root-scope name
// — so the run-time copy lands where the contract reads (SRD-094 FR-4).
func (p *Process) AssociateInput(
	inputName string, from flow.AssociationSource, sourceID string,
) error {
	in, err := p.contractParam(data.Input, inputName)
	if err != nil {
		return err
	}

	if oerr := p.ownEvent(from, "AssociateInput", func(n flow.Node) bool {
		_, ok := n.(*events.StartEvent)

		return ok
	}, "a Start Event"); oerr != nil {
		return oerr
	}

	outputs := from.Outputs()

	idx := slices.IndexFunc(outputs, func(iae *data.ItemAwareElement) bool {
		return iae.ID() == sourceID
	})
	if idx == -1 {
		return errs.New(
			errs.M("AssociateInput: start event %q has no data output %q",
				from.Name(), sourceID),
			errs.C(errorClass, errs.ObjectNotFound))
	}

	target, err := namedElement(in, inputName)
	if err != nil {
		return err
	}

	// both ends exist and carry items; NewAssociation cannot refuse them —
	// said in the form the coverage gate reads
	a, err := data.NewAssociation(target, data.WithSource(outputs[idx]))
	if err != nil {
		return errs.New(
			errs.M("AssociateInput: association building failed for input %q",
				inputName),
			errs.C(errorClass, errs.BulidingFailed),
			errs.E(err))
	}

	return from.BindOutgoing(a)
}

// AssociateOutput wires the process's declared output named outputName into
// an End Event's data input — the process DataOutputs are sources of its
// End Event's input associations (§10.4.2 p224). to must be an End Event of
// this process and targetID one of its data inputs (the mirror of
// AssociateInput's sourceID). The association's source is named after the
// output — its root-scope name — so the run-time copy reads what the flow
// left there (SRD-094 FR-4, FR-6).
func (p *Process) AssociateOutput(
	outputName string, to flow.AssociationTarget, targetID string,
) error {
	out, err := p.contractParam(data.Output, outputName)
	if err != nil {
		return err
	}

	if oerr := p.ownEvent(to, "AssociateOutput", func(n flow.Node) bool {
		_, ok := n.(*events.EndEvent)

		return ok
	}, "an End Event"); oerr != nil {
		return oerr
	}

	inputs := to.Inputs()

	idx := slices.IndexFunc(inputs, func(iae *data.ItemAwareElement) bool {
		return iae.ID() == targetID
	})
	if idx == -1 {
		return errs.New(
			errs.M("AssociateOutput: end event %q has no data input %q for "+
				"output %q", to.Name(), targetID, outputName),
			errs.C(errorClass, errs.ObjectNotFound))
	}

	source, err := namedElement(out, outputName)
	if err != nil {
		return err
	}

	// both ends exist and carry items; NewAssociation cannot refuse them —
	// said in the form the coverage gate reads
	a, err := data.NewAssociation(inputs[idx], data.WithSource(source))
	if err != nil {
		return errs.New(
			errs.M("AssociateOutput: association building failed for output %q",
				outputName),
			errs.C(errorClass, errs.BulidingFailed),
			errs.E(err))
	}

	return to.BindIncoming(a)
}

// contractParam returns the declared parameter of direction dir named name,
// refusing a contract-less process and an undeclared name.
func (p *Process) contractParam(
	dir data.Direction, name string,
) (*data.Parameter, error) {
	if p.ioSpec == nil {
		return nil, errs.New(
			errs.M("process %q declares no I/O contract to wire %q to",
				p.Name(), name),
			errs.C(errorClass, errs.InvalidState))
	}

	// the direction is one of the two constants; Parameters cannot fail
	// here — said in the form the coverage gate reads
	params, err := p.ioSpec.Parameters(dir)
	if err != nil {
		return nil, errs.Invariant("parameters of %q: %w", dir, err)
	}

	idx := slices.IndexFunc(params, func(pp *data.Parameter) bool {
		return pp.Name() == name
	})
	if idx == -1 {
		return nil, errs.New(
			errs.M("process %q declares no %s named %q", p.Name(), strings.ToLower(string(dir)), name),
			errs.C(errorClass, errs.ObjectNotFound))
	}

	return params[idx], nil
}

// ownEvent checks that n is a node of this process of the kind is accepts —
// the two positions the standard names are the process's own Start and End
// Events.
func (p *Process) ownEvent(
	n flow.Node, op string, is func(flow.Node) bool, kind string,
) error {
	if n == nil {
		return errs.New(
			errs.M("%s: a nil event isn't allowed", op),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	own, ok := p.nodes[n.ID()]
	if !ok || own != n {
		return errs.New(
			errs.M("%s: %q isn't a node of process %q", op, n.Name(), p.Name()),
			errs.C(errorClass, errs.InvalidObject))
	}

	if !is(n) {
		return errs.New(
			errs.M("%s: %q isn't %s of process %q (§10.4.2 names only the "+
				"process's Start and End Events)", op, n.Name(), kind, p.Name()),
			errs.C(errorClass, errs.InvalidObject))
	}

	return nil
}

// namedElement builds the association end that stands for the declared
// parameter: an element over the parameter's item, named after the
// parameter — the name the association's routing reports and the runtime
// resolves in the root scope (SRD-063 FR-5). The declaration itself is left
// untouched (NewAssociation resets its target's state).
func namedElement(param *data.Parameter, name string) (*data.ItemAwareElement, error) {
	iae, err := data.NewItemAwareElement(param.ItemDefinition(),
		data.UnavailableDataState)
	if err != nil {
		// the declaration's item was accepted once already; said in the
		// form the coverage gate reads
		return nil, errs.Invariant("element of %q: %w", name, err)
	}

	iae.SetName(name)

	return iae, nil
}
