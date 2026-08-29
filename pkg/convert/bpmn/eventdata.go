package bpmn

import (
	"encoding/xml"
	"strconv"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// eventParamDirs maps each event kind to the one data direction the
// standard gives it (§10.4.2 p224): a catch event has data outputs and
// output associations, a throw event data inputs and input associations.
// The fixed classification as data, not control flow.
var eventParamDirs = map[string]data.Direction{
	tagStartEvent:        data.Output,
	tagIntermediateCatch: data.Output,
	tagBoundaryEvent:     data.Output,
	tagEndEvent:          data.Input,
	tagIntermediateThrow: data.Input,
}

// parseEventParamElem records a node's bare <dataInput>/<dataOutput> into
// its body — an event's data (§10.4.2), consumed by the event builders;
// any other owner is refused at build, where the owner's kind is known.
func parseEventParamElem(p *parser, body *nodeBody, se xml.StartElement) error {
	spec, err := p.parseParamSpec(paramTags[se.Name.Local], se)
	if err != nil {
		return err
	}

	body.params = append(body.params, spec)

	return nil
}

// bareParamMisplaced refuses a bare parameter on a node that is not an
// event: on a task it lives inside the <ioSpecification> (§10.4.1); on
// anything else the standard gives no parameter at all.
func bareParamMisplaced(s *nodeSpec) error {
	owner := "<" + s.se.Name.Local + "> " + strconv.Quote(s.id)

	if paramOwners[s.se.Name.Local] {
		return errs.New(
			errs.M("bpmn: %s carries a bare <%s>; a task's parameter lives "+
				"inside its <ioSpecification> (§10.4.1) — write it there",
				owner, s.body.params[0].local()),
			errs.C(errorClass, errs.InvalidObject))
	}

	return errs.New(
		errs.M("bpmn: %s carries a <%s>; §10.4.1 gives parameters to tasks "+
			"(inside an <ioSpecification>) and §10.4.2 to events — move it to "+
			"the task or event that reads or writes the data",
			owner, s.body.params[0].local()),
		errs.C(errorClass, errs.InvalidObject))
}

// eventDataOptions renders an event's bare parameters as the data option
// its constructor takes (SRD-094 FR-7): WithDataOutputs on a catch kind,
// WithDataInputs on a throw kind. A parameter of the other direction is
// refused as the position the standard reserves (§10.4.2). A parameter
// naming no itemSubjectRef adopts the item of the definition it pairs
// with by position (p217; SRD-089.G §4.3's adoption) — one that pairs with
// nothing is refused here, with the same words the model uses.
func eventDataOptions(
	p *parser, asm *assembly, se xml.StartElement, id string,
	defs []flow.EventDefinition, specs []defSpec, params []paramSpec,
) ([]options.Option, error) {
	if len(params) == 0 {
		return nil, nil
	}

	owner := "<" + se.Name.Local + "> " + strconv.Quote(id)
	dir := eventParamDirs[se.Name.Local]

	for i := range params {
		if params[i].dir != dir {
			return nil, errs.New(
				errs.M("bpmn: %s carries <%s> %q; §10.4.2 gives a catch event "+
					"data outputs only and a throw event data inputs only — the "+
					"other direction has no place on it", owner,
					params[i].local(), params[i].id),
				errs.C(errorClass, errs.InvalidObject))
		}
	}

	adopted, err := adoptedItems(owner, asm, defs, specs, params)
	if err != nil {
		return nil, err
	}

	// Every event parameter takes the item of the definition it pairs
	// with: the model binds an event's data by the definition's own item,
	// which the converter builds as the definition's placeholder (§4.1),
	// so a file's itemSubjectRef can only be checked for existence here —
	// and is compared, as the file wrote it, where an association joins it
	// to a data element (§10.4.1's match).
	// buildParamSpecs fails only through the resolver here (its own
	// element build is an invariant it says so about); the return is said
	// in the form the coverage gate reads.
	byDir, err := buildParamSpecs(params, false,
		func(ps *paramSpec, from string) (*data.ItemDefinition, error) {
			if ps.itemRef != "" {
				if _, ierr := itemFor(p, asm, from, ps.id, ps.itemRef); ierr != nil {
					return nil, ierr
				}
			}

			return adopted[ps.id], nil
		})
	if err != nil {
		return nil, err
	}

	if dir == data.Output {
		return []options.Option{events.WithDataOutputs(byDir[data.Output]...)}, nil
	}

	return []options.Option{events.WithDataInputs(byDir[data.Input]...)}, nil
}

// bearingDef is an item-bearing definition as built and as the file wrote
// its payload type.
type bearingDef struct {
	item    *data.ItemDefinition
	fileRef string
}

// adoptedItems pairs each parameter with the item of the definition at
// its position — the standard's correspondence is by order (p217) — and
// refuses one past the item-bearing definitions. A parameter that names an
// itemSubjectRef must name the one its definition's element named in the
// file (a message's itemRef, an escalation's structureRef): the model
// carries a placeholder for both, so the file's two references are the
// only pair p217's MUST can be checked on.
func adoptedItems(
	owner string, asm *assembly, defs []flow.EventDefinition,
	specs []defSpec, params []paramSpec,
) (map[string]*data.ItemDefinition, error) {
	bearing := make([]bearingDef, 0, len(defs))

	for i, def := range defs {
		if items := def.GetItemsList(); len(items) > 0 && items[0].Structure() != nil {
			var fileRef string
			if i < len(specs) {
				fileRef = asm.cat.itemRefs[specs[i].ref]
			}

			bearing = append(bearing, bearingDef{item: items[0], fileRef: fileRef})
		}
	}

	adopted := map[string]*data.ItemDefinition{}

	for i := range params {
		if i >= len(bearing) {
			return nil, errs.New(
				errs.M("bpmn: %s declares <%s> %q, and no item-bearing "+
					"definition stands at its position to pair with (§10.4.2 "+
					"p217) — an event's data comes from what triggers it; drop it",
					owner, params[i].local(), params[i].id),
				errs.C(errorClass, errs.InvalidObject))
		}

		if ref := params[i].itemRef; ref != "" && bearing[i].fileRef != "" &&
			ref != bearing[i].fileRef {
			return nil, errs.New(
				errs.M("bpmn: %s declares <%s> %q over item %q, but the "+
					"definition it pairs with carries %q; §10.4.2 p217 makes "+
					"them the same itemDefinition — give both one itemSubjectRef",
					owner, params[i].local(), params[i].id, ref, bearing[i].fileRef),
				errs.C(errorClass, errs.InvalidObject))
		}

		adopted[params[i].id] = bearing[i].item
	}

	return adopted, nil
}

// paramItemRef returns the itemSubjectRef the file gave the node's
// parameter paramID — the identity §10.4.1's match compares, as written.
// A task's parameter carries its file item as its model item; an event's
// parameter carries its definition's placeholder, so its file ref is read
// from the spec. "" means the file named none.
func paramItemRef(s *nodeSpec, paramID, modelItemID string) string {
	if !eventNodeTags[s.se.Name.Local] {
		return modelItemID
	}

	for i := range s.body.params {
		if s.body.params[i].id == paramID {
			return s.body.params[i].itemRef
		}
	}

	// nodeParamItem already matched paramID to one of the event's
	// parameters, and a file can only name the ones it declared; said in
	// the form the coverage gate reads.
	return ""
}

// eventAssocDirection refuses a data association of the direction the
// standard does not give the event (§10.4.2): a catch event has output
// associations only, a throw event input associations only.
func eventAssocDirection(s *nodeSpec, a *dataAssocSpec, label string) error {
	if a.dir == eventParamDirs[s.se.Name.Local] {
		return nil
	}

	return errs.New(
		errs.M("bpmn: <%s> %q carries %s; §10.4.2 gives a catch event output "+
			"associations only and a throw event input associations only — "+
			"the other direction has no place on it",
			s.se.Name.Local, s.id, label),
		errs.C(errorClass, errs.InvalidObject))
}

// processParam finds the enclosing process's declared parameter with id,
// or nil — the process's <ioSpecification> parameters are the one data end
// that is not a data element (SRD-094 FR-7).
func processParam(asm *assembly, id string) *paramSpec {
	if asm.spec.io == nil {
		return nil
	}

	for i := range asm.spec.io.params {
		if asm.spec.io.params[i].id == id {
			return &asm.spec.io.params[i]
		}
	}

	return nil
}

// bindProcessEnd wires an association whose data end is a process
// parameter through the process's own Associate* (ADR-040 v.2 §2.7): a
// Start Event's output association may target a process input, an End
// Event's input association may source a process output — the two
// positions the standard names (§10.4.2 p224) — and nothing else.
func bindProcessEnd(
	asm *assembly, s *nodeSpec, a *dataAssocSpec, ps *paramSpec,
	paramItemID, label string,
) error {
	// the standard's type constraint, as the file wrote both ends
	fileItem := paramItemRef(s, a.paramRef, paramItemID)
	if fileItem != "" && ps.itemRef != "" && ps.itemRef != fileItem {
		return errs.New(
			errs.M("bpmn: %s joins %q (item %q) to the process's <%s> %q (item "+
				"%q); §10.4.1 makes the two ends' itemDefinitions match — give "+
				"both the same itemSubjectRef", label, a.paramRef, fileItem,
				ps.local(), ps.id, ps.itemRef),
			errs.C(errorClass, errs.InvalidObject))
	}

	node := asm.byID[s.id]
	kind := s.se.Name.Local
	name := fallbackName(ps.id, ps.name)

	var err error

	switch {
	case a.dir == data.Output && kind == tagStartEvent && ps.dir == data.Input:
		src, _ := node.(flow.AssociationSource)
		err = asm.proc.AssociateInput(name, src, a.paramRef)

	case a.dir == data.Input && kind == tagEndEvent && ps.dir == data.Output:
		trg, _ := node.(flow.AssociationTarget)
		err = asm.proc.AssociateOutput(name, trg, a.paramRef)

	default:
		return errs.New(
			errs.M("bpmn: %s on <%s> %q names the process's <%s> %q as its data "+
				"end; §10.4.2 gives the process DataInputs to a Start Event's "+
				"output associations and the process DataOutputs to an End "+
				"Event's input associations, and to nothing else",
				label, kind, s.id, ps.local(), ps.id),
			errs.C(errorClass, errs.InvalidObject))
	}

	// Both process ends validate what this pass already validated — the
	// event is the process's, the parameter is declared, the ids exist —
	// so the propagation is said in the form the coverage gate reads.
	if err != nil {
		return wrapErr(
			"bpmn: couldn't wire "+label+" on <"+kind+"> "+strconv.Quote(s.id),
			errs.BulidingFailed, err)
	}

	return nil
}
