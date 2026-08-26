package bpmn

import (
	"encoding/xml"
	"strconv"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/artifacts"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// assocSpec is an <association> as read. BPMN gives one tag to two
// different things: the line from a comment to what it annotates, and the
// link naming a compensation handler. The second is execution semantics
// and becomes the boundary's handler wiring (SRD-089.E §4.7); the first
// is a carried artifact, built by buildAssociations (ADR-039, SRD-092
// FR-9).
type assocSpec struct {
	id, srcRef, trgRef string
	// direction is the associationDirection attribute, "" when absent —
	// the model defaults it to the standard's None (§8.4.1).
	direction string
	// container is the id of the declaring container, "" for the process —
	// the same convention as nodeSpec (SRD-089.E §4.1).
	container string
	// used marks the association a compensation boundary consumed. That
	// one document fact already has its model representation — the
	// handler wiring — so it is NOT additionally carried as an artifact
	// (ADR-039 §2.4). What nothing consumes is a plain association and
	// becomes one.
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
		id:        id,
		srcRef:    src,
		trgRef:    trg,
		direction: strings.TrimSpace(attrValue(se, "associationDirection")),
		container: p.container,
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

// endFor resolves one association end against everything pass 2 built:
// the flow nodes, the data elements, the sequence flows, and the carried
// artifacts. The standard types an end as any BaseElement (§8.4.1), so
// the universe is deliberately the whole assembly, not a curated subset.
func endFor(
	asm *assembly, flowByID map[string]*flow.SequenceFlow, ref string,
) (foundation.Identifyer, bool) {
	if n, ok := asm.byID[ref]; ok {
		return n, true
	}

	if e, ok := asm.dataElems[ref]; ok {
		return e, true
	}

	if f, ok := flowByID[ref]; ok {
		return f, true
	}

	if a, ok := asm.artsByID[ref]; ok {
		return a, true
	}

	return nil, false
}

// buildAssociations materializes every association no compensation
// boundary consumed as a carried artifact on its declaring container
// (SRD-092 FR-9). An end naming nothing this import built degrades that
// one association to a report — the file survives, and the host is told
// which reference failed (FR-10, ADR-039 §2.6).
func buildAssociations(
	p *parser, asm *assembly, flowByID map[string]*flow.SequenceFlow,
) error {
	for _, a := range asm.assocs {
		if a.used {
			continue
		}

		src, ok := endFor(asm, flowByID, a.srcRef)
		if !ok {
			p.report(a.id, tagAssociation,
				assocEndLoss("sourceRef", a.srcRef))

			continue
		}

		trg, ok := endFor(asm, flowByID, a.trgRef)
		if !ok {
			p.report(a.id, tagAssociation,
				assocEndLoss("targetRef", a.trgRef))

			continue
		}

		na, err := artifacts.NewAssociation(src, trg,
			artifacts.AssociationDirection(a.direction),
			withDeclaredID(a.id)...)
		if err != nil {
			return errs.New(
				errs.M("bpmn: association %s cannot be built",
					strconv.Quote(a.id)),
				errs.C(errorClass, errs.InvalidObject),
				errs.E(err))
		}

		if err := attachArtifact(asm, a.container, na); err != nil {
			return err
		}
	}

	return nil
}

// assocEndLoss words the report for an association end that names nothing
// this import built.
func assocEndLoss(role, ref string) string {
	return "its " + role + " " + strconv.Quote(ref) +
		" resolves to no element this import built — an association states " +
		"a relationship, and one end of this one has no model home, so the " +
		"association is dropped and the rest of the file imports " +
		"(ADR-039 §2.6)"
}
