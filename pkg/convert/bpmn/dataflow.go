package bpmn

import (
	"encoding/xml"
	"strconv"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// paramSpec is a <dataInput> or <dataOutput> as read from an
// <ioSpecification>. Which direction it is comes from the tag, kept as
// the data.Direction the model already names.
type paramSpec struct {
	id, name string
	itemRef  string // itemSubjectRef
	dir      data.Direction
	docs     []docSpec
	// optional and whileExecuting arrive from the SET's ref lists, not
	// from the parameter's own attributes (SRD-089.G §4.4).
	optional       bool
	whileExecuting bool
}

// ioSpec is one activity's <ioSpecification> as read: its parameters and
// the one set per direction the engine models. Sets beyond the first
// refuse at parse (FR-2).
type ioSpec struct {
	// setSeen tracks that at most one set per direction was read.
	setSeen map[data.Direction]bool
	params  []paramSpec
}

// param returns the spec of the parameter declared under id, for the set
// reader that flags it and the association pass that types it.
func (io *ioSpec) param(id string) *paramSpec {
	for i := range io.params {
		if io.params[i].id == id {
			return &io.params[i]
		}
	}

	return nil
}

// dataAssocSpec is one data association as read from an activity's body:
// its own id, the activity-side parameter reference, and the data-element
// side. (The name avoids the .E assocSpec, which is the artifact
// <association>.)
type dataAssocSpec struct {
	id string
	// dir tells which tag it was: Input (dataInputAssociation) or
	// Output (dataOutputAssociation).
	dir data.Direction
	// paramRef is targetRef on an input association, sourceRef on an
	// output one — the end that names the activity's own parameter.
	paramRef string
	// elemRef is the other end: the data element in scope.
	elemRef string
	// extraSources are sourceRefs beyond the first on an input
	// association — refused with the transformation rule (§4.6).
	extraSources []string
	// hasTransformation records a <transformation> child — refused,
	// never mapped (§4.6); the flag exists so the refusal can name the
	// association that carries it.
	hasTransformation bool
	// hasAssignment records an <assignment> child, refused the same way
	// with its own wording (§4.6).
	hasAssignment bool
}

// paramTags maps the two parameter tags to their direction — the fixed
// classification as data, not control flow.
var paramTags = map[string]data.Direction{
	tagDataInput:  data.Input,
	tagDataOutput: data.Output,
}

// setTags maps the two set tags to the direction whose single set they
// declare.
var setTags = map[string]data.Direction{
	tagInputSet:  data.Input,
	tagOutputSet: data.Output,
}

// setRefTags maps a set's ref-list children to what membership in each
// means for the named parameter. The plain membership list is not here —
// it flags nothing — and is handled by name in parseSetChild.
var setRefTags = map[string]func(*paramSpec){
	"optionalInputRefs":        func(p *paramSpec) { p.optional = true },
	"optionalOutputRefs":       func(p *paramSpec) { p.optional = true },
	"whileExecutingInputRefs":  func(p *paramSpec) { p.whileExecuting = true },
	"whileExecutingOutputRefs": func(p *paramSpec) { p.whileExecuting = true },
}

// memberRefTags are the plain membership lists per direction, consumed
// for the §4.4 membership check and flagging nothing.
var memberRefTags = map[string]bool{
	"dataInputRefs":  true,
	"dataOutputRefs": true,
}

// paramOwners are the node kinds the standard lets declare an
// <ioSpecification>: its Tasks. CallableElements — the Process and the
// GlobalTask family — are not flow nodes here, and everything else an
// XML file could put one on (an embedded sub-process, a transaction, a
// call activity, an event, a gateway) is the containment rule's to
// refuse (§4.7a; semantics/data.md:96-98).
var paramOwners = map[string]bool{
	tagTask:             true,
	tagManualTask:       true,
	tagUserTask:         true,
	tagServiceTask:      true,
	tagScriptTask:       true,
	tagBusinessRuleTask: true,
	tagSendTask:         true,
	tagReceiveTask:      true,
}

// parseIOSpecElem records a node's <ioSpecification> into its body.
func parseIOSpecElem(p *parser, body *nodeBody, se xml.StartElement) error {
	if body.io != nil {
		return errs.New(
			errs.M("bpmn: %q carries a second <ioSpecification>; an activity "+
				"has at most one (§10.4.1 Table 10.58)", p.owner),
			errs.C(errorClass, errs.InvalidObject))
	}

	io, err := p.parseIOSpecification(se)
	if err != nil {
		return err
	}

	body.io = io

	return nil
}

// parseDataAssocElem records a node's data association into its body —
// wired in pass 2, once both ends exist (§4.1).
func parseDataAssocElem(p *parser, body *nodeBody, se xml.StartElement) error {
	spec, err := p.parseDataAssociation(assocDirs[se.Name.Local], se)
	if err != nil {
		return err
	}

	body.dataAssocs = append(body.dataAssocs, spec)

	return nil
}

// assocDirs maps the two association tags to their direction.
var assocDirs = map[string]data.Direction{
	tagDataInputAssoc:  data.Input,
	tagDataOutputAssoc: data.Output,
}

// The two ref ELEMENTS of a data association — children carrying an id
// as text, unlike the sequence flow's same-named attributes.
const (
	tagSourceRef = "sourceRef"
	tagTargetRef = "targetRef"
)

// ioSpecMisplaced refuses an <ioSpecification> on a node the standard
// does not give one to — "Only Tasks and CallableElements (Processes,
// GlobalTasks) MAY define DataInputs/DataOutputs … Embedded SubProcesses
// MUST NOT define DataInputs/DataOutputs directly" (§10.4.1,
// semantics/data.md:96-98). The standard's refusal, not the engine's: an
// embedded container reaches data through its parent scope, and an
// event's I/O is its own form, not an ioSpecification.
func ioSpecMisplaced(s *nodeSpec) error {
	return errs.New(
		errs.M("bpmn: <%s> %q carries an <ioSpecification>, which §10.4.1 "+
			"gives only to tasks (and to callable elements — a process, a "+
			"global task); move the parameters to the tasks that read them",
			s.se.Name.Local, s.id),
		errs.C(errorClass, errs.InvalidObject))
}

// buildIOParams builds a node's parameters from its parsed ioSpec and
// returns the WithParameters options its constructor takes — the one
// door the model has (activity_options.go:190, SRD-089.G §1).
func buildIOParams(
	p *parser, asm *assembly, s *nodeSpec,
) ([]options.Option, error) {
	byDir, err := buildParamSpecs(s.body.io.params, true,
		func(spec *paramSpec, from string) (*data.ItemDefinition, error) {
			return paramItem(p, asm, s, spec, from)
		})
	if err != nil {
		return nil, err
	}

	// Fixed order, not a map range: option order is behaviorally inert
	// today (each option touches only its own direction's list), but
	// deterministic enumeration is the house rule (ADR-011 §2.9) — the
	// one order stable across runs is the one a debugger can rely on.
	opts := make([]options.Option, 0, len(byDir))
	for _, dir := range []data.Direction{data.Input, data.Output} {
		if params := byDir[dir]; len(params) != 0 {
			opts = append(opts, activities.WithParameters(dir, params...))
		}
	}

	return opts, nil
}

// itemResolver resolves one parameter's item definition: an activity's
// version adopts an association partner's item when the parameter names
// none (paramItem); a process has no associations to adopt from and
// resolves the ref or takes the empty item (SRD-093 FR-11).
type itemResolver func(
	spec *paramSpec, from string,
) (*data.ItemDefinition, error)

// buildParamSpecs builds the parameters of one <ioSpecification> per
// direction — the owner-agnostic half of the build, shared by a task and
// a process (SRD-093 §3.5): the element construction, the set flags and
// the name fallback. dedupByItem turns on the §4.3a duplicate-item guard:
// an ACTIVITY addresses a direction's parameters by item-definition id
// (the association match, the readiness gate, the load loop), so two
// parameters over one item are one parameter declared twice; a PROCESS
// addresses its contract by name (ADR-040 §2.4), and two inputs typed by
// the same item are simply two inputs.
func buildParamSpecs(
	specs []paramSpec, dedupByItem bool, resolve itemResolver,
) (map[data.Direction][]*data.Parameter, error) {
	byDir := map[data.Direction][]*data.Parameter{}
	seen := map[data.Direction]map[string]string{
		data.Input:  {},
		data.Output: {},
	}

	for i := range specs {
		spec := &specs[i]
		from := spec.local() + " " + strconv.Quote(spec.id)

		item, err := resolve(spec, from)
		if err != nil {
			return nil, err
		}

		if prior, dup := seen[spec.dir][item.ID()]; dedupByItem && dup {
			return nil, errs.New(
				errs.M("bpmn: %s and %q declare one itemSubjectRef %q; the "+
					"engine addresses a direction's parameters by item id — "+
					"the association match, the readiness gate, the load loop "+
					"— so this is one parameter declared twice; give each its "+
					"own <itemDefinition>", from, prior, item.ID()),
				errs.C(errorClass, errs.DuplicateObject))
		}

		seen[spec.dir][item.ID()] = spec.id

		// Unreachable for any document: the item is non-nil (itemFor), the
		// default states exist (build creates them first), and the base
		// options are a non-empty id plus documentation. Said in the form
		// the coverage gate reads.
		iae, err := data.NewItemAwareElement(item, nil,
			append([]options.Option{foundation.WithID(spec.id)},
				docOptions(spec.docs)...)...)
		if err != nil {
			return nil, errs.Invariant(
				"%s rejected its own ItemAwareElement: %w", from, err)
		}

		var popts []data.ParameterOption
		if spec.optional {
			popts = append(popts, data.Optional())
		}

		if spec.whileExecuting {
			popts = append(popts, data.WhileExecuting())
		}

		param, err := data.NewParameter(
			fallbackName(spec.id, spec.name), iae, popts...)
		if err != nil {
			return nil, wrapErr(
				"bpmn: couldn't create "+from, errs.BulidingFailed, err)
		}

		byDir[spec.dir] = append(byDir[spec.dir], param)
	}

	return byDir, nil
}

// local names a parameter's element as the file wrote it.
func (s *paramSpec) local() string {
	if s.dir == data.Input {
		return tagDataInput
	}

	return tagDataOutput
}

// paramItem resolves the item a parameter is built over: its own
// itemSubjectRef when it names one; its association partner's item when
// it does not (§4.3 case 2 — the parameter ADOPTS); an empty item of its
// own when it is no association's end at all.
//
// The partner is resolved through its parse SPEC, never a built element:
// data elements build after the nodes (SRD-089.F §4.4), but their specs
// are complete by pass 2.
func paramItem(
	p *parser, asm *assembly, s *nodeSpec, spec *paramSpec, from string,
) (*data.ItemDefinition, error) {
	if spec.itemRef != "" {
		return itemFor(p, asm, from, spec.id, spec.itemRef)
	}

	elem := assocPartnerSpec(asm, s, spec.id, spec.dir)
	if elem == nil {
		return emptyItem(spec.id)
	}

	// §4.3 case 3: the element never adopts — it may feed several nodes,
	// and typing it from one association would make the others' types a
	// function of build order.
	if elem.itemRef == "" {
		return nil, errs.New(
			errs.M("bpmn: %s is an association end and neither it nor %s "+
				"names an itemSubjectRef; the element may feed several nodes, "+
				"so the converter will not choose its type from one of them — "+
				"give %s an itemSubjectRef",
				from, elem.owner(), elem.owner()),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	return itemFor(p, asm, from, spec.id, elem.itemRef)
}

// assocPartnerSpec finds the data element at the other end of the
// association naming param as its activity-side end — nil when the
// parameter is no association's end, or the end is not a data element
// (the wiring pass owns that refusal).
func assocPartnerSpec(
	asm *assembly, s *nodeSpec, param string, dir data.Direction,
) *dataSpec {
	for i := range s.body.dataAssocs {
		a := &s.body.dataAssocs[i]
		if a.dir != dir || a.paramRef != param {
			continue
		}

		return dataSpecFor(asm, a.elemRef)
	}

	return nil
}

// dataSpecFor finds a data element's parse spec by id, following a
// <dataObjectReference> to its object — SAD-001 §14.1 rule 2, applied at
// the spec level. One hop only: a reference's target is validated to be
// a data object when the elements build, so a deeper chain never wires.
func dataSpecFor(asm *assembly, id string) *dataSpec {
	s := rawDataSpec(asm, id)
	if s != nil && s.local == tagDataObjectRef {
		s = rawDataSpec(asm, s.targetRef)
	}

	if s == nil || s.local == tagDataObjectRef {
		return nil
	}

	return s
}

// rawDataSpec finds a data element's parse spec by id, no following.
func rawDataSpec(asm *assembly, id string) *dataSpec {
	for i := range asm.datas {
		if asm.datas[i].id == id {
			return &asm.datas[i]
		}
	}

	return nil
}

// parseIOSpecification reads one <ioSpecification> into an ioSpec.
//
// It reads parameters and both sets in one walk: BPMN serializes the
// parameters ahead of the sets, so a set's refs resolve against
// already-read parameters, and a ref that does not is the §4.4 dangling
// refusal either way.
func (p *parser) parseIOSpecification(se xml.StartElement) (*ioSpec, error) {
	id, err := requiredID(se)
	if err != nil {
		return nil, err
	}

	err = p.claimID(id, se.Name.Local)
	if err != nil {
		return nil, err
	}

	io := &ioSpec{setSeen: map[data.Direction]bool{}}

	outer := p.owner
	p.owner = id

	err = p.parseIOSpecBody(io, se)

	p.owner = outer

	if err != nil {
		return nil, err
	}

	p.reportUnmappedAttrs(se, id, nil)

	return io, nil
}

// parseIOSpecBody walks the children of <ioSpecification>.
func (p *parser) parseIOSpecBody(io *ioSpec, se xml.StartElement) error {
	for {
		tok, err := p.token()
		if err != nil {
			return err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			if err := p.parseIOSpecChild(io, t); err != nil {
				return err
			}

		case xml.EndElement:
			if t.Name == se.Name {
				return nil
			}
		}
	}
}

// parseIOSpecChild handles one child of <ioSpecification>: a parameter,
// a set, or a stranger settled through the disposition tables.
func (p *parser) parseIOSpecChild(io *ioSpec, se xml.StartElement) error {
	if se.Name.Space != nsBPMN {
		return p.skipElement()
	}

	if dir, ok := paramTags[se.Name.Local]; ok {
		return p.parseIOParam(io, dir, se)
	}

	if dir, ok := setTags[se.Name.Local]; ok {
		return p.parseIOSet(io, dir, se)
	}

	return p.settle(ctxData, se)
}

// parseIOParam reads one <dataInput> or <dataOutput>.
func (p *parser) parseIOParam(
	io *ioSpec, dir data.Direction, se xml.StartElement,
) error {
	id, err := requiredID(se)
	if err != nil {
		return err
	}

	err = p.claimID(id, se.Name.Local)
	if err != nil {
		return err
	}

	// A parameter's content model is a data element's: documentation and
	// <dataState> — the same body reader serves it through a carrier.
	carrier := dataSpec{
		local:   se.Name.Local,
		id:      id,
		name:    strings.TrimSpace(attrValue(se, "name")),
		itemRef: strings.TrimSpace(attrValue(se, attrItemSubjectRef)),
	}

	outer := p.owner
	p.owner = id

	err = p.parseDataBody(&carrier, se)

	p.owner = outer

	if err != nil {
		return err
	}

	if carrier.state != "" {
		p.report(id, tagDataState, dataStateLoss)
	}

	p.reportUnmappedAttrs(se, id, nil)

	io.params = append(io.params, paramSpec{
		id:      carrier.id,
		name:    carrier.name,
		itemRef: carrier.itemRef,
		dir:     dir,
		docs:    carrier.docs,
	})

	return nil
}

// parseIOSet reads one <inputSet> or <outputSet>: the single set the
// engine models per direction (ADR-011 §2.2). A second one refuses as
// the standing non-goal it is (§4.4).
func (p *parser) parseIOSet(
	io *ioSpec, dir data.Direction, se xml.StartElement,
) error {
	id, err := requiredID(se)
	if err != nil {
		return err
	}

	err = p.claimID(id, se.Name.Local)
	if err != nil {
		return err
	}

	if io.setSeen[dir] {
		return errs.New(
			errs.M("bpmn: a second <%s> is more input/output modes than this "+
				"engine models: an activity has ONE set per direction, its "+
				"parameter list (ADR-011 §2.2) — model genuine alternative "+
				"modes with gateways or boundary events", se.Name.Local),
			errs.C(errorClass, errs.InvalidObject))
	}

	io.setSeen[dir] = true

	outer := p.owner
	p.owner = id

	err = p.parseIOSetBody(io, se)

	p.owner = outer

	if err != nil {
		return err
	}

	p.reportUnmappedAttrs(se, id, nil)

	return nil
}

// parseIOSetBody walks a set's ref lists, flagging the named parameters.
func (p *parser) parseIOSetBody(io *ioSpec, se xml.StartElement) error {
	for {
		tok, err := p.token()
		if err != nil {
			return err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			if err := p.parseSetChild(io, t); err != nil {
				return err
			}

		case xml.EndElement:
			if t.Name == se.Name {
				return nil
			}
		}
	}
}

// parseSetChild handles one ref-list child of a set. Every ref names a
// declared parameter or refuses — the extract's own membership rule
// ("MUST NOT reference DataInputs not listed", semantics/data.md) is a
// plain dangling-reference check here (§4.4).
func (p *parser) parseSetChild(io *ioSpec, se xml.StartElement) error {
	if se.Name.Space != nsBPMN {
		return p.skipElement()
	}

	flag, flags := setRefTags[se.Name.Local]
	if !flags && !memberRefTags[se.Name.Local] {
		return p.settle(ctxData, se)
	}

	ref, err := p.readText(se)
	if err != nil {
		return err
	}

	ref = strings.TrimSpace(ref)

	param := io.param(ref)
	if param == nil {
		site := refSite{from: p.owner, attr: se.Name.Local, target: ref}

		return site.notFound("declared parameter")
	}

	if flags {
		flag(param)
	}

	return nil
}

// parseDataAssociation reads one <dataInputAssociation> or
// <dataOutputAssociation> into a spec for the pass-2 wiring (§4.1).
func (p *parser) parseDataAssociation(
	dir data.Direction, se xml.StartElement,
) (dataAssocSpec, error) {
	id, err := requiredID(se)
	if err != nil {
		return dataAssocSpec{}, err
	}

	err = p.claimID(id, se.Name.Local)
	if err != nil {
		return dataAssocSpec{}, err
	}

	spec := dataAssocSpec{id: id, dir: dir}

	outer := p.owner
	p.owner = id

	err = p.parseDataAssocBody(&spec, se)

	p.owner = outer

	if err != nil {
		return dataAssocSpec{}, err
	}

	p.reportUnmappedAttrs(se, id, nil)

	return spec, nil
}

// parseDataAssocBody walks an association's children: the two ref ends,
// and the shapes this stage refuses but must still see to name (§4.6).
func (p *parser) parseDataAssocBody(
	spec *dataAssocSpec, se xml.StartElement,
) error {
	for {
		tok, err := p.token()
		if err != nil {
			return err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			if err := p.parseDataAssocChild(spec, t); err != nil {
				return err
			}

		case xml.EndElement:
			if t.Name == se.Name {
				return nil
			}
		}
	}
}

// parseDataAssocChild handles one child of a data association.
//
// On an INPUT association the parameter end is targetRef and the element
// end sourceRef; an output association mirrors them. The mapping below
// keeps that fact in one place.
func (p *parser) parseDataAssocChild(
	spec *dataAssocSpec, se xml.StartElement,
) error {
	if se.Name.Space != nsBPMN {
		return p.skipElement()
	}

	switch se.Name.Local {
	case tagSourceRef:
		ref, err := p.readText(se)
		if err != nil {
			return err
		}

		spec.setEnd(data.Input, strings.TrimSpace(ref))

		return nil

	case tagTargetRef:
		ref, err := p.readText(se)
		if err != nil {
			return err
		}

		spec.setEnd(data.Output, strings.TrimSpace(ref))

		return nil

	case "transformation":
		spec.hasTransformation = true

		return p.skipElement()

	case "assignment":
		spec.hasAssignment = true

		return p.skipElement()

	case tagDocumentation:
		// Skipped by declaration, the policy-table pattern for a context
		// with no model element to attach it to: the association is built
		// by the data element's Associate* methods, which take no
		// documentation.
		return p.skipElement()
	}

	return p.settle(ctxData, se)
}

// setEnd records one ref end. from says which SIDE the ref arrived on:
// data.Input for a <sourceRef>, data.Output for a <targetRef>. Crossing
// it with the association's own direction decides whether the ref names
// the activity's parameter or the data element in scope — and a second
// element-side ref is a multi-source, kept for §4.6's refusal.
func (s *dataAssocSpec) setEnd(from data.Direction, ref string) {
	// On an input association the parameter is the target; on an output
	// one it is the source. The element side is the other.
	paramSide := data.Output
	if s.dir == data.Output {
		paramSide = data.Input
	}

	if from == paramSide {
		s.paramRef = ref

		return
	}

	if s.elemRef == "" {
		s.elemRef = ref

		return
	}

	s.extraSources = append(s.extraSources, ref)
}
