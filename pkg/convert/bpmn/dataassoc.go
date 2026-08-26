package bpmn

import (
	"strconv"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	dataobjects "github.com/dr-dobermann/gobpm/pkg/model/data_objects"
	datastores "github.com/dr-dobermann/gobpm/pkg/model/data_stores"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// eventNodeTags are the flow-node kinds that are events — the owners
// whose data associations BPMN allows and the model cannot attach
// (§4.7, #329).
var eventNodeTags = map[string]bool{
	tagStartEvent:        true,
	tagEndEvent:          true,
	tagIntermediateCatch: true,
	tagIntermediateThrow: true,
	tagBoundaryEvent:     true,
}

// wireDataAssociations is the pass after the data elements exist (§4.1):
// every node's data associations, resolved across the two element
// families and wired through the data element's own Associate* methods —
// so the store routing (SRD-068 FR-4) and the scope routing (SRD-063
// FR-5) arrive from the model untouched.
func wireDataAssociations(p *parser, asm *assembly) error {
	for i := range asm.specs {
		s := &asm.specs[i]

		for j := range s.body.dataAssocs {
			if err := wireDataAssoc(p, asm, s, &s.body.dataAssocs[j]); err != nil {
				return err
			}
		}
	}

	return nil
}

// assocLabel names an association in an error, as the file wrote it.
func assocLabel(a *dataAssocSpec) string {
	local := tagDataInputAssoc
	if a.dir == data.Output {
		local = tagDataOutputAssoc
	}

	return "<" + local + "> " + strconv.Quote(a.id)
}

// wireDataAssoc resolves and wires one association, or refuses it by its
// ADR-024 §2.16 class (SRD-089.G §4.6, §4.7).
func wireDataAssoc(
	p *parser, asm *assembly, s *nodeSpec, a *dataAssocSpec,
) error {
	label := assocLabel(a)

	// The owner first: events are the shape BPMN allows and the model
	// cannot attach (#329); everything else outside the task kinds is
	// the containment rule's territory, same as an ioSpecification.
	if eventNodeTags[s.se.Name.Local] {
		return errs.New(
			errs.M("bpmn: <%s> %q carries %s; the model's events have no "+
				"attachment for a data association — the capability is #329 — "+
				"move the data through a task beside the event",
				s.se.Name.Local, s.id, label),
			errs.C(errorClass, errs.InvalidObject))
	}

	if !paramOwners[s.se.Name.Local] {
		return errs.New(
			errs.M("bpmn: <%s> %q carries %s, and an association's activity "+
				"end is a task's own parameter (§10.4.1); move it to the task "+
				"that reads or writes the data", s.se.Name.Local, s.id, label),
			errs.C(errorClass, errs.InvalidObject))
	}

	if a.hasAssignment {
		return errs.New(
			errs.M("bpmn: %s carries an <assignment>, which has no model "+
				"counterpart — the association-expression capability is #328 — "+
				"model the mapping as a script task before or after this one",
				label),
			errs.C(errorClass, errs.InvalidObject))
	}

	if a.hasTransformation || len(a.extraSources) != 0 {
		return errs.New(
			errs.M("bpmn: %s needs an expression the engine's copy path does "+
				"not evaluate — a <transformation>, which several sources also "+
				"require (§10.4.1 rule 3); the capability is SRD-063 §10.3's "+
				"follow-up, #328 — align the two ends' itemDefinitions and "+
				"copy plainly, or map through a script task", label),
			errs.C(errorClass, errs.InvalidObject))
	}

	if a.paramRef == "" || a.elemRef == "" {
		return errs.New(
			errs.M("bpmn: %s names no %s; an association is its two ends",
				label, missingEnd(a)),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	paramItemID, err := nodeParamItem(p, asm, s, a)
	if err != nil {
		return err
	}

	elem, espec, err := assocElement(p, asm, a)
	if err != nil {
		return err
	}

	// §4.3 case 3 first: an untyped element never adopts, and the wiring
	// cannot invent its type — the refusal says what to declare.
	if espec.itemRef == "" {
		return errs.New(
			errs.M("bpmn: %s wires %s, which names no itemSubjectRef; the "+
				"element may feed several nodes, so the converter will not "+
				"choose its type from one association — give %s an "+
				"itemSubjectRef", label, espec.owner(), espec.owner()),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	// The standard's own type constraint: the two ends' itemDefinitions
	// MUST match — the transformation escape hatch is refused above —
	// and the model matches by exactly that id (§4.3).
	if espec.itemRef != paramItemID {
		return errs.New(
			errs.M("bpmn: %s joins %q (item %q) to %s (item %q); §10.4.1 "+
				"makes the two ends' itemDefinitions match — give both the "+
				"same itemSubjectRef", label, a.paramRef, paramItemID,
				espec.owner(), espec.itemRef),
			errs.C(errorClass, errs.InvalidObject))
	}

	return bindAssoc(asm, s, a, elem, paramItemID, label)
}

// missingEnd names which ref an incomplete association lacks, in the
// file's own vocabulary.
func missingEnd(a *dataAssocSpec) string {
	if a.paramRef == "" && a.dir == data.Input {
		return tagTargetRef
	}

	if a.paramRef == "" {
		return tagSourceRef
	}

	if a.dir == data.Input {
		return tagSourceRef
	}

	return tagTargetRef
}

// nodeParamItem resolves the association's activity-side end to one of
// the node's own parameters and returns that parameter's item id — the
// identity everything downstream matches on.
func nodeParamItem(
	p *parser, asm *assembly, s *nodeSpec, a *dataAssocSpec,
) (string, error) {
	node, built := asm.byID[s.id]
	if !built {
		// Unreachable: the wiring pass runs after buildNodes, which built
		// every spec or aborted the import.
		return "", errs.Invariant("association pass reached unbuilt node %q", s.id)
	}

	params := nodeParams(node, a.dir)

	for _, iae := range params {
		if iae.ID() == a.paramRef {
			return iae.ItemDefinition().ID(), nil
		}
	}

	site := refSite{from: assocLabel(a), attr: paramRefAttr(a), target: a.paramRef}

	if kind, taken := p.ids[a.paramRef]; taken {
		// The id exists and is even the right KIND — it just belongs to
		// some other activity. Saying "wrong kind" would send the modeler
		// hunting a typo that is not there.
		if kind == paramLocal(a) {
			return "", errs.New(
				errs.M("bpmn: %s targets %s %q, which belongs to another "+
					"activity; an association wires its own activity's "+
					"parameter (§10.4.1)", assocLabel(a), kind, a.paramRef),
				errs.C(errorClass, errs.ObjectNotFound))
		}

		return "", site.wrongKind(paramLocal(a), kind)
	}

	return "", site.notFound("own " + paramLocal(a))
}

// nodeParams returns the node's parameters for the association's
// direction: an input association targets a dataInput, an output one
// sources a dataOutput.
func nodeParams(node flow.Node, dir data.Direction) []*data.ItemAwareElement {
	if dir == data.Input {
		if t, ok := node.(flow.AssociationTarget); ok {
			return t.Inputs()
		}

		return nil
	}

	if s, ok := node.(flow.AssociationSource); ok {
		return s.Outputs()
	}

	return nil
}

// paramRefAttr names the attribute the activity-side ref arrived in.
func paramRefAttr(a *dataAssocSpec) string {
	if a.dir == data.Input {
		return tagTargetRef
	}

	return tagSourceRef
}

// paramLocal names the parameter element kind the association's activity
// side must resolve to.
func paramLocal(a *dataAssocSpec) string {
	if a.dir == data.Input {
		return tagDataInput
	}

	return tagDataOutput
}

// assocElement resolves the association's data-element end: a data
// object (a reference retargets to its object, SAD-001 §14.1 rule 2), a
// data store reference — or the refusals for a property (#331) and for
// anything the document never declared.
func assocElement(
	p *parser, asm *assembly, a *dataAssocSpec,
) (flow.Element, *dataSpec, error) {
	espec := dataSpecFor(asm, a.elemRef)
	if espec == nil {
		label := assocLabel(a)

		if p.ids[a.elemRef] == tagProperty {
			return nil, nil, errs.New(
				errs.M("bpmn: %s names <property> %q as its data end; the "+
					"model's Property cannot be an association end — the "+
					"capability is #331 — use a <dataObject> for data that "+
					"flows between nodes", label, a.elemRef),
				errs.C(errorClass, errs.InvalidObject))
		}

		site := refSite{from: label, attr: elemRefAttr(a), target: a.elemRef}

		if kind, taken := p.ids[a.elemRef]; taken {
			return nil, nil, site.wrongKind("data element", kind)
		}

		return nil, nil, site.notFound("data element")
	}

	elem, built := asm.dataElems[espec.id]
	if !built {
		// Unreachable: the pass runs after buildDataElements, which built
		// every spec's element or aborted the import.
		return nil, nil, errs.Invariant(
			"association pass reached unbuilt data element %q", espec.id)
	}

	return elem, espec, nil
}

// elemRefAttr names the attribute the data-element ref arrived in.
func elemRefAttr(a *dataAssocSpec) string {
	if a.dir == data.Input {
		return tagSourceRef
	}

	return tagTargetRef
}

// bindAssoc wires the resolved pair through the element's own
// Associate* — the model's attach path, which builds the Association,
// seeds the store ref where the element is a store reference, and binds
// it into the node (SRD-089.G §1).
func bindAssoc(
	asm *assembly, s *nodeSpec, a *dataAssocSpec,
	elem flow.Element, paramItemID, label string,
) error {
	node := asm.byID[s.id]

	// Every paramOwner kind embeds task, which implements both binding
	// interfaces (task.go:586-587) — a miss means the owners table let a
	// non-task through.
	target, isTarget := node.(flow.AssociationTarget)
	source, isSource := node.(flow.AssociationSource)

	if !isTarget || !isSource {
		return errs.Invariant("node %q (%T) binds no data associations",
			s.id, node)
	}

	var err error

	switch e := elem.(type) {
	case *dataobjects.DataObject:
		if a.dir == data.Input {
			err = e.AssociateTarget(target, nil)
		} else {
			err = e.AssociateSource(source, []string{paramItemID}, nil)
		}

	case *datastores.DataStoreReference:
		if a.dir == data.Input {
			err = e.AssociateTarget(target, nil)
		} else {
			err = e.AssociateSource(source, []string{paramItemID}, nil)
		}

	default:
		// Unreachable: buildDataElements records only the two kinds above.
		return errs.Invariant("data element %q is a %T", a.elemRef, elem)
	}

	if err != nil {
		return wrapErr(
			"bpmn: couldn't wire "+label+" on <"+s.se.Name.Local+"> "+
				strconv.Quote(s.id),
			errs.BulidingFailed, err)
	}

	return nil
}
