package bpmn

import (
	"encoding/xml"
	"strconv"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/convert"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// assocSpec is an <association> as read. BPMN gives one tag to two
// different things: the line from a comment to what it annotates, and the
// link naming a compensation handler. Only the second carries execution
// semantics (conformance.md line 176), and only the second is built
// (SRD-089.E §4.7 and §4.9).
type assocSpec struct {
	id, srcRef, trgRef string
	// used marks the association a compensation boundary consumed. What
	// nothing consumes is a plain association, and plain associations are
	// refused — see refusePlainAssociations.
	used bool
}

// parseAssociationElem records one <association> for pass 2.
//
// It is recorded rather than acted on because its source is typically a
// boundary event declared later in the file, and its target an activity
// that does not exist until pass 2 builds it.
func parseAssociationElem(p *parser, asm *assembly, se xml.StartElement) error {
	src := strings.TrimSpace(attrValue(se, "sourceRef"))
	trg := strings.TrimSpace(attrValue(se, "targetRef"))

	if src == "" || trg == "" {
		return errs.New(
			errs.M("bpmn: association %q needs both sourceRef and targetRef "+
				"(got %q/%q)", attrValue(se, "id"), src, trg),
			errs.C(errorClass, errs.InvalidParameter))
	}

	// The id stays optional — an Artifact, not a flow element — but a
	// declared one joins the document's one ledger like every other
	// (SRD-089.F §4.11): before this claim, an association could silently
	// reuse a task's id.
	id := strings.TrimSpace(attrValue(se, "id"))
	if id != "" {
		if err := p.claimID(id, se.Name.Local); err != nil {
			return err
		}
	}

	asm.assocs = append(asm.assocs, assocSpec{
		id:     id,
		srcRef: src,
		trgRef: trg,
	})

	return p.skipElement()
}

// compensationHandler resolves the activity a compensation boundary event
// routes to: the target of the <association> leaving that event.
//
// BPMN draws the link in one direction — from the boundary event to the
// handler — and the model's constructor takes the handler, so the search
// is by source. An association pointing the other way is not read as a
// handler link, because reading it both ways would make a diagram's
// meaning depend on which end a modeler happened to drag first.
func compensationHandler(
	asm *assembly, owner, id string,
) (flow.ActivityNode, error) {
	for i := range asm.assocs {
		a := &asm.assocs[i]
		if a.srcRef != id {
			continue
		}

		n, ok := asm.byID[a.trgRef]
		if !ok {
			return nil, errs.New(
				errs.M("bpmn: %s is associated with %q, and no flow node "+
					"with that id is declared", owner, a.trgRef),
				errs.C(errorClass, errs.ObjectNotFound))
		}

		act, ok := n.(flow.ActivityNode)
		if !ok {
			return nil, errs.New(
				errs.M("bpmn: %s names %q as its compensation handler, and "+
					"that element is a %T rather than an activity — only an "+
					"activity can be compensated", owner, a.trgRef, n),
				errs.C(errorClass, errs.InvalidObject))
		}

		a.used = true

		return act, nil
	}

	return nil, errs.New(
		errs.M("bpmn: %s carries a compensation trigger, whose handler BPMN "+
			"names through an <association> from the event to the handling "+
			"activity — the reason the standard keeps Association in an "+
			"execution-conformance scope at all. This event has none, so "+
			"there is nothing to run when compensation is thrown", owner),
		errs.C(errorClass, errs.InvalidObject))
}

// refusePlainAssociations refuses every association no compensation
// boundary consumed.
//
// A plain association is the line from a <textAnnotation> to what it
// annotates. The annotation itself is skipped — dropping a comment leaves
// the imported definition meaning the same — but the link cannot be:
// pkg/model/artifacts declares Association with no constructor, nothing
// imports that package, and a Process has nowhere to put an artifact
// (#323). Skipping the link too would discard a stated relationship
// silently, which is the disposition ADR-024 §2.9 reserves for elements
// whose absence changes nothing.
func refusePlainAssociations(asm *assembly) error {
	for _, a := range asm.assocs {
		if a.used {
			continue
		}

		return errs.New(
			errs.E(&convert.UnsupportedElementError{
				Tag: tagAssociation,
				ID:  a.id,
			}),
			errs.M("bpmn: association %s links %q to %q and is not a "+
				"compensation link. A plain association needs an artifact "+
				"collection on the container and a constructor for "+
				"artifacts.Association, neither of which this model has "+
				"(#323) — the annotation it draws from is imported, the line "+
				"is not",
				strconv.Quote(a.id), a.srcRef, a.trgRef),
			errs.C(errorClass, errs.InvalidObject))
	}

	return nil
}
