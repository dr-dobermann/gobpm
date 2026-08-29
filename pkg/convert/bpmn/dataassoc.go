package bpmn

import (
	"strconv"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	dataobjects "github.com/dr-dobermann/gobpm/pkg/model/data_objects"
	datastores "github.com/dr-dobermann/gobpm/pkg/model/data_stores"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// eventNodeTags are the flow-node kinds that are events — the owners
// whose data associations take the one direction §10.4.2 gives them and
// whose parameters are bare, not an ioSpecification's (SRD-094 FR-7).
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

	// The owner first: an event takes the one direction the standard gives
	// it (§10.4.2, SRD-094 FR-7); everything else outside the task kinds is
	// the containment rule's territory, same as an ioSpecification.
	if eventNodeTags[s.se.Name.Local] {
		if err := eventAssocDirection(s, a, label); err != nil {
			return err
		}
	} else if !paramOwners[s.se.Name.Local] {
		return errs.New(
			errs.M("bpmn: <%s> %q carries %s, and an association's activity "+
				"end is a task's own parameter (§10.4.1); move it to the task "+
				"that reads or writes the data", s.se.Name.Local, s.id, label),
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

	// The one data end that is not a data element: the enclosing process's
	// own parameter, the standard's Start/End special case (SRD-094 FR-7).
	if ps := processParam(asm, a.elemRef); ps != nil {
		return bindProcessEnd(asm, s, a, ps, paramItemID, label)
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

	trans, shape, err := assocShape(
		a, asm.exprLanguage, label, assocTargetName(s, a, espec))
	if err != nil {
		return err
	}

	extras, err := extraSourceOpts(p, asm, a, label)
	if err != nil {
		return err
	}

	shape = append(shape, extras...)

	// The standard's own type constraint: with a PLAIN copy the two ends'
	// itemDefinitions MUST match, compared as the file wrote them (a task's
	// parameter carries its file item; an event's carries its definition's,
	// SRD-094). An event parameter that named no item adopted its
	// definition's and is not compared (§4.3).
	//
	// An expression shape is the standard's own escape from that rule: a
	// transformation's result replaces the target and an assignment writes
	// what its own expression produced, so neither is a copy between two
	// items and neither requires them to agree (§10.4.2 rules 1 and 2).
	fileItem := paramItemRef(s, a.paramRef, paramItemID)
	if !shaped(a) && fileItem != "" && espec.itemRef != fileItem {
		return errs.New(
			errs.M("bpmn: %s joins %q (item %q) to %s (item %q); §10.4.1 "+
				"makes the two ends' itemDefinitions match — give both the "+
				"same itemSubjectRef", label, a.paramRef, fileItem,
				espec.owner(), espec.itemRef),
			errs.C(errorClass, errs.InvalidObject))
	}

	return bindAssoc(asm, s, a, elem, paramItemID, label, trans, shape)
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

// extraSourceOpts turns the association's refs beyond the first into
// source options (SRD-097 FR-7): several sources are legal under an
// expression shape (§10.4.2 rule 3), and the model takes each one as a
// data.WithSource — the option NewAssociation already understands, which
// is why no new attach API is needed for them.
//
// Which refs those are depends on direction. On an INPUT association the
// element side is the source, so extraSources are data elements to
// resolve. On an OUTPUT association the parameter side is the source
// (associatedSources carries them) and a second ELEMENT-side ref would be
// a second target, which §10.4.1 does not allow — refused here.
func extraSourceOpts(
	p *parser, asm *assembly, a *dataAssocSpec, label string,
) ([]options.Option, error) {
	if a.dir == data.Output {
		if len(a.extraSources) != 0 {
			return nil, errs.New(
				errs.M("bpmn: %s names %d <targetRef>s; a data association "+
					"has ONE target (§10.4.1) — write one association per "+
					"element it writes", label, len(a.extraSources)+1),
				errs.C(errorClass, errs.InvalidObject))
		}

		return nil, nil
	}

	opts := make([]options.Option, 0, len(a.extraSources))

	for _, ref := range a.extraSources {
		espec := dataSpecFor(asm, ref)
		if espec == nil {
			site := refSite{from: label, attr: tagSourceRef, target: ref}

			if kind, taken := p.ids[ref]; taken {
				return nil, site.wrongKind("data element", kind)
			}

			return nil, site.notFound("data element")
		}

		elem, built := asm.dataElems[espec.id]
		if !built {
			return nil, errs.Invariant(
				"association pass reached unbuilt data element %q", espec.id)
		}

		src, ok := elem.(interface {
			ItemAware() *data.ItemAwareElement
		})
		if !ok {
			return nil, errs.Invariant(
				"data element %q (%T) exposes no item-aware element",
				espec.id, elem)
		}

		opts = append(opts, data.WithSource(src.ItemAware()))
	}

	return opts, nil
}

// assocTargetName is what the association's target is CALLED — the name an
// assignment's to path must lead with. On an output association the target
// is the data element; on an input one it is the node's own parameter, and
// the file names it by ref.
func assocTargetName(s *nodeSpec, a *dataAssocSpec, espec *dataSpec) string {
	if a.dir == data.Output {
		return espec.name
	}

	for i := range s.body.params {
		if s.body.params[i].id == a.paramRef {
			return fallbackName(s.body.params[i].id, s.body.params[i].name)
		}
	}

	// The parameter was resolved before this point (assocParam), so a miss
	// is unreachable; said in the form the coverage gate reads.
	return a.paramRef
}

// associatedSources lists the node outputs an OUTPUT association reads,
// BY ITEM ID — which is how the model's Associate* matches a node's
// parameters. A plain copy reads exactly one (§10.4.2 rule 3, enforced by
// the model); under an expression shape the file may name several, and
// every one of them gates the association even when the expression names
// none of them.
//
// On an output association the PARAMETER side is the source, so the extra
// refs are extraParams — extraSources there would be extra targets, which
// an association cannot have (§10.4.1) and extraSourceOpts refuses.
func associatedSources(
	asm *assembly, s *nodeSpec, a *dataAssocSpec, paramItemID string,
) ([]string, error) {
	ids := []string{paramItemID}

	if len(a.extraParams) == 0 {
		return ids, nil
	}

	node, built := asm.byID[s.id]
	if !built {
		return nil, errs.Invariant(
			"association pass reached unbuilt node %q", s.id)
	}

	params := nodeParams(node, a.dir)

	for _, ref := range a.extraParams {
		item := ""

		for _, iae := range params {
			if iae.ID() == ref {
				item = iae.ItemDefinition().ID()

				break
			}
		}

		if item == "" {
			return nil, errs.New(
				errs.M("bpmn: %s names %s %q, which this activity does not "+
					"declare (§10.4.1)", assocLabel(a), paramLocal(a), ref),
				errs.C(errorClass, errs.ObjectNotFound))
		}

		ids = append(ids, item)
	}

	return ids, nil
}

// shaped reports whether the document gave the association an expression
// shape — a transformation or at least one assignment.
func shaped(a *dataAssocSpec) bool {
	return a.transformation != nil || len(a.assignments) != 0
}

// elemRefAttr names the attribute the data-element ref arrived in.
func elemRefAttr(a *dataAssocSpec) string {
	if a.dir == data.Input {
		return tagSourceRef
	}

	return tagTargetRef
}

// assocShape builds the association's expression options from the parsed
// document (§10.4.2 rules 1 and 2, SRD-097 FR-7): a <transformation>
// becomes data.WithTransformation, each <assignment> a data.Assignment
// under data.WithAssignments. The model owns what the shapes mean — the
// converter only maps them (ADR-024 §2.16).
func assocShape(
	a *dataAssocSpec, docLang, label, targetName string,
) (data.FormalExpression, []options.Option, error) {
	var (
		trans data.FormalExpression
		err   error
	)

	if a.transformation != nil {
		if trans, err = newValueExpression(*a.transformation, docLang); err != nil {
			return nil, nil, wrapErr(
				"bpmn: "+label+" carries a <transformation> this converter "+
					"cannot make runnable", errs.InvalidObject, err)
		}
	}

	if len(a.assignments) == 0 {
		return trans, nil, nil
	}

	assigns := make([]*data.Assignment, 0, len(a.assignments))

	for i, as := range a.assignments {
		built, err := buildAssignment(as, i, docLang, label, targetName)
		if err != nil {
			return nil, nil, err
		}

		assigns = append(assigns, built)
	}

	return trans, []options.Option{data.WithAssignments(assigns...)}, nil
}

// buildAssignment maps one <assignment> onto the model.
func buildAssignment(
	as assignSpec, idx int, docLang, label, targetName string,
) (*data.Assignment, error) {
	if as.from == nil {
		return nil, errs.New(
			errs.M("bpmn: %s: <assignment> #%d declares no <from> expression",
				label, idx),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	from, err := newValueExpression(*as.from, docLang)
	if err != nil {
		return nil, wrapErr(
			"bpmn: "+label+": <assignment>'s <from> isn't runnable",
			errs.InvalidObject, err)
	}

	to, head, err := toPath(as.to)
	if err != nil {
		return nil, wrapErr(
			"bpmn: "+label+": <assignment> #"+strconv.Itoa(idx),
			errs.InvalidObject, err)
	}

	// The head must name the association's own target (ADR-011 §2.4): one
	// association writes one element, and its availability gate, its report
	// and its movement fact all name that element. A <to> that is an
	// expression rather than a path fails here — nothing else it could be
	// is called what the target is called.
	if head != targetName {
		return nil, errs.New(
			errs.M("bpmn: %s: <assignment> #%d writes at <to> %q, which "+
				"doesn't name the association's target %q — an assignment "+
				"writes inside its own target at a path (ADR-011 §2.4); "+
				"compute the value in <from> instead",
				label, idx, as.to, targetName),
			errs.C(errorClass, errs.InvalidObject))
	}

	built, err := data.NewAssignment(from, to, withDeclaredID(as.toID)...)
	if err != nil {
		return nil, wrapErr(
			"bpmn: "+label+": couldn't build <assignment> #"+strconv.Itoa(idx),
			errs.BulidingFailed, err)
	}

	return built, nil
}

// bindAssoc wires the resolved pair through the element's own
// Associate* — the model's attach path, which builds the Association,
// seeds the store ref where the element is a store reference, and binds
// it into the node (SRD-089.G §1).
func bindAssoc(
	asm *assembly, s *nodeSpec, a *dataAssocSpec,
	elem flow.Element, paramItemID, label string,
	trans data.FormalExpression, shape []options.Option,
) error {
	srcIDs, err := associatedSources(asm, s, a, paramItemID)
	if err != nil {
		return err
	}

	node := asm.byID[s.id]

	// Every paramOwner kind embeds task, which implements both binding
	// interfaces; an event implements the one its direction needs (a catch
	// is a source, a throw a target — SRD-094 FR-1), and the direction
	// check above let only that one through. A miss means the owners table
	// let a non-binding kind through.
	target, isTarget := node.(flow.AssociationTarget)
	source, isSource := node.(flow.AssociationSource)

	if (a.dir == data.Input && !isTarget) || (a.dir == data.Output && !isSource) {
		return errs.Invariant("node %q (%T) binds no %s data associations",
			s.id, node, a.dir)
	}

	// A task's input is addressed by item — its file item is its model
	// item; an event's input carries its definition's placeholder, so the
	// association names the input itself, as the file did (SRD-094 FR-7).
	byInputID := eventNodeTags[s.se.Name.Local]

	switch e := elem.(type) {
	case *dataobjects.DataObject:
		switch {
		case a.dir == data.Output:
			err = e.AssociateSource(
				source, srcIDs, trans, shape...)
		case byInputID:
			err = e.AssociateTargetInput(target, a.paramRef, trans, shape...)
		default:
			err = e.AssociateTarget(target, trans, shape...)
		}

	case *datastores.DataStoreReference:
		switch {
		case a.dir == data.Output:
			err = e.AssociateSource(
				source, srcIDs, trans, shape...)
		case byInputID:
			err = e.AssociateTargetInput(target, a.paramRef, trans, shape...)
		default:
			err = e.AssociateTarget(target, trans, shape...)
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
