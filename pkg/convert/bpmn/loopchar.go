package bpmn

import (
	"encoding/xml"
	"strconv"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// loopSpec is an activity's loop marker as read — one of the two kinds,
// at most one per activity (SRD-089.H FR-5). Which fields mean what
// follows the kind, the dataSpec pattern.
type loopSpec struct {
	kind string // tagStandardLoop or tagMultiInstance
	id   string
	// loopMaximum is the attribute's raw text, "" when absent — parsed
	// at build so the refusal can quote what the file wrote.
	loopMaximum string
	// condition, cardinality, completion are the expression children, in
	// the exprSpec shape the language machinery consumes; nil = absent.
	condition, cardinality, completion *exprSpec
	// inputRef/inputItem and outputRef/outputItem are the collection
	// pairs: the IDREF and the item's name-or-id (§4.4).
	inputRef, inputItem   string
	outputRef, outputItem string
	// behavior and the two event refs arrive as attributes; complex holds
	// the parsed complexBehaviorDefinition children (M4 builds them).
	behavior        string
	noneRef, oneRef string
	complex         []complexSpec
	// testBefore, isSequential are the two boolean attributes. The two
	// has*Item flags record the item ELEMENTS' presence — an item with
	// neither name nor id resolves to "", and "present but unnameable"
	// must not be mistaken for "absent" when the refusal names what to
	// fix.
	testBefore, isSequential    bool
	hasInputItem, hasOutputItem bool
}

// complexSpec is one <complexBehaviorDefinition> as read: its condition
// and its implicit throw event's body — built in M4, where both halves
// are required by the model (multiinstance.go:41-53).
type complexSpec struct {
	id        string
	condition *exprSpec
	// eventDefs are the implicit throw event's event definitions, read
	// through the .D defSpec shape.
	eventDefs []defSpec
	hasEvent  bool
}

// parseLoopElem records a node's loop marker into its body — at most
// one of either kind (FR-5: loopCharacteristics is 0..1 on Activity,
// elements/activities.md:29).
func parseLoopElem(p *parser, body *nodeBody, se xml.StartElement) error {
	if body.loop != nil {
		return errs.New(
			errs.M("bpmn: %q carries a second loop marker; an activity has "+
				"at most one loopCharacteristics of either kind", p.owner),
			errs.C(errorClass, errs.InvalidObject))
	}

	spec, err := p.parseLoopChar(se)
	if err != nil {
		return err
	}

	body.loop = spec

	return nil
}

// buildLoopOption builds the model's loop characteristics from the
// node's marker and returns the WithLoop option its constructor takes.
func buildLoopOption(
	p *parser, asm *assembly, s *nodeSpec,
) (options.Option, error) {
	spec := s.body.loop

	lc, err := loopBuilders[spec.kind](p, asm, s, spec)
	if err != nil {
		return nil, err
	}

	return activities.WithLoop(lc), nil
}

// loopBuilder builds one marker kind's model object.
type loopBuilder func(
	*parser, *assembly, *nodeSpec, *loopSpec,
) (activities.LoopCharacteristics, error)

// loopBuilders maps a marker to what it becomes, keyed by tag as every
// builder table is.
var loopBuilders = map[string]loopBuilder{
	tagStandardLoop:  buildStandardLoop,
	tagMultiInstance: buildMultiInstance,
}

// miBehaviors maps the behavior attribute onto the model's enumeration —
// the fixed classification as data. The empty key is the absent
// attribute, whose default the extract sets to All.
var miBehaviors = map[string]activities.MultiInstanceBehavior{
	"":        activities.BehaviorAll,
	"All":     activities.BehaviorAll,
	"None":    activities.BehaviorNone,
	"One":     activities.BehaviorOne,
	"Complex": activities.BehaviorComplex,
}

// buildMultiInstance builds the count-driven kind (SRD-089.H §4.3-§4.7).
func buildMultiInstance(
	p *parser, asm *assembly, s *nodeSpec, spec *loopSpec,
) (activities.LoopCharacteristics, error) {
	owner := "<" + s.se.Name.Local + "> " + strconv.Quote(s.id)

	opts, err := miCardinalityOpts(p, asm, s, spec, owner)
	if err != nil {
		return nil, err
	}

	if spec.isSequential {
		opts = append(opts, activities.WithSequential())
	}

	outOpts, err := miOutputOpts(p, asm, spec, owner)
	if err != nil {
		return nil, err
	}

	opts = append(opts, outOpts...)

	if spec.completion != nil {
		cond, condErr := newBoolExpression(*spec.completion, asm.exprLanguage)
		if condErr != nil {
			return nil, condErr
		}

		opts = append(opts, activities.WithCompletionCondition(cond))
	}

	bOpts, err := miBehaviorOpts(p, asm, spec, owner)
	if err != nil {
		return nil, err
	}

	opts = append(opts, bOpts...)

	mi, err := activities.NewMultiInstance(opts...)
	if err != nil {
		return nil, wrapErr(
			"bpmn: couldn't build the multi-instance of "+owner,
			errs.BulidingFailed, err)
	}

	return mi, nil
}

// miCardinalityOpts implements the §4.3 matrix: exactly one instance
// count, in the converter's words — the model's message names Go
// options, not the file's elements.
func miCardinalityOpts(
	p *parser, asm *assembly, _ *nodeSpec, spec *loopSpec, owner string,
) ([]activities.MultiInstanceOption, error) {
	hasCard, hasColl := spec.cardinality != nil, spec.inputRef != ""

	switch {
	case hasCard && hasColl:
		return nil, errs.New(
			errs.M("bpmn: %s declares both a loopCardinality and a "+
				"loopDataInputRef; the instance count is determined by ONE "+
				"of the two (§13.3.7) — drop one", owner),
			errs.C(errorClass, errs.InvalidObject))

	case !hasCard && !hasColl:
		return nil, errs.New(
			errs.M("bpmn: %s declares neither a loopCardinality nor a "+
				"loopDataInputRef; a multi-instance with no instance count "+
				"has nothing to iterate (§13.3.7) — declare one of the two",
				owner),
			errs.C(errorClass, errs.EmptyNotAllowed))

	case hasCard:
		// An <inputDataItem> beside a cardinality is an orphan — the
		// item names collection elements, and there is no collection.
		// Refused like every other half-pair, not silently dropped.
		if spec.hasInputItem {
			return nil, errs.New(
				errs.M("bpmn: %s declares a loopCardinality and an "+
					"<inputDataItem> but no loopDataInputRef; the item names "+
					"the elements of a collection — pair it with a "+
					"loopDataInputRef or drop it", owner),
				errs.C(errorClass, errs.InvalidObject))
		}

		expr, err := newIntExpression(*spec.cardinality, asm.exprLanguage)
		if err != nil {
			return nil, err
		}

		return []activities.MultiInstanceOption{
			activities.WithCardinality(expr)}, nil
	}

	if !spec.hasInputItem {
		return nil, errs.New(
			errs.M("bpmn: %s names a loopDataInputRef with no inputDataItem; "+
				"the item is the name each instance reads its element under — "+
				"declare an <inputDataItem> beside the ref", owner),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if spec.inputItem == "" {
		return nil, errs.New(
			errs.M("bpmn: %s carries an <inputDataItem> with neither a name "+
				"nor an id; the item is the name each instance reads its "+
				"element under — give it one", owner),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	name, err := loopCollectionName(p, asm, owner, tagLoopDataInRef, spec.inputRef)
	if err != nil {
		return nil, err
	}

	return []activities.MultiInstanceOption{
		activities.WithInputCollection(name, spec.inputItem)}, nil
}

// miOutputOpts maps the output pair: both halves or neither (§4.3).
func miOutputOpts(
	p *parser, asm *assembly, spec *loopSpec, owner string,
) ([]activities.MultiInstanceOption, error) {
	if spec.outputRef == "" && !spec.hasOutputItem {
		return nil, nil
	}

	if spec.outputRef == "" || !spec.hasOutputItem {
		missing := "loopDataOutputRef"
		if spec.outputRef != "" {
			missing = "outputDataItem"
		}

		return nil, errs.New(
			errs.M("bpmn: %s declares half an output collection pair; the "+
				"%s is missing — results are assembled from the item into "+
				"the collection, so both are needed", owner, missing),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if spec.outputItem == "" {
		return nil, errs.New(
			errs.M("bpmn: %s carries an <outputDataItem> with neither a name "+
				"nor an id; the item is the name each instance writes its "+
				"result under — give it one", owner),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	name, err := loopCollectionName(p, asm, owner, tagLoopDataOutRef, spec.outputRef)
	if err != nil {
		return nil, err
	}

	return []activities.MultiInstanceOption{
		activities.WithOutputCollection(name, spec.outputItem)}, nil
}

// loopCollectionName resolves a collection IDREF to the scope-datum NAME
// the model wants (§4.4): the id resolves through the data-element specs
// — a reference retargeting to its object (SAD-001 §14.1 rule 2) — and
// the target must be a data object, the extract's own constraint ("MUST
// be linked to a process-scope DataObject",
// semantics/multi-instance.md:108).
func loopCollectionName(
	p *parser, asm *assembly, owner, attr, ref string,
) (string, error) {
	site := refSite{from: owner, attr: attr, target: ref}

	espec := dataSpecFor(asm, ref)
	if espec == nil {
		if kind, taken := p.ids[ref]; taken {
			return "", site.wrongKind(tagDataObject, kind)
		}

		return "", site.notFound(tagDataObject)
	}

	if espec.local != tagDataObject {
		return "", site.wrongKind(tagDataObject, espec.local)
	}

	return fallbackName(espec.id, espec.name), nil
}

// miBehaviorOpts maps the behavior attribute and its event sources
// (§4.7): None/One resolve their refs against the definitions-level
// event definitions; a complex definition needs both its halves. The
// behavior/source consistency rules stay the model's voice.
func miBehaviorOpts(
	p *parser, asm *assembly, spec *loopSpec, owner string,
) ([]activities.MultiInstanceOption, error) {
	behavior, known := miBehaviors[spec.behavior]
	if !known {
		return nil, errs.New(
			errs.M("bpmn: %s declares behavior=%q; the enumeration is All, "+
				"None, One or Complex (§13.3.7)", owner, spec.behavior),
			errs.C(errorClass, errs.InvalidParameter))
	}

	var opts []activities.MultiInstanceOption

	if spec.behavior != "" {
		opts = append(opts, activities.WithBehavior(behavior))
	}

	if spec.noneRef != "" {
		def, err := rootDef(p, asm, owner, attrNoneBehaviorEv, spec.noneRef)
		if err != nil {
			return nil, err
		}

		opts = append(opts, activities.WithNoneBehaviorEvent(def))
	}

	if spec.oneRef != "" {
		def, err := rootDef(p, asm, owner, attrOneBehaviorEv, spec.oneRef)
		if err != nil {
			return nil, err
		}

		opts = append(opts, activities.WithOneBehaviorEvent(def))
	}

	if len(spec.complex) != 0 {
		defs, err := buildComplexDefs(p, asm, spec, owner)
		if err != nil {
			return nil, err
		}

		opts = append(opts, activities.WithComplexBehavior(defs...))
	}

	return opts, nil
}

// rootDef resolves a behavior ref against the definitions-level event
// definitions and builds the flow.EventDefinition the model options
// take, through the same builders an event's own definition uses.
func rootDef(
	p *parser, asm *assembly, owner, attr, ref string,
) (flow.EventDefinition, error) {
	spec, declared := p.rootDefs[ref]
	if !declared {
		site := refSite{from: owner, attr: attr, target: ref}

		if kind, taken := p.ids[ref]; taken {
			return nil, site.wrongKind("event definition", kind)
		}

		return nil, site.notFound("event definition")
	}

	built, err := defBuilders[spec.local](asm, owner, spec)
	if err != nil {
		return nil, err
	}

	return built.def, nil
}

// buildComplexDefs builds each <complexBehaviorDefinition>: the model
// requires both the condition and the implicit throw event
// (multiinstance.go:41-53), so a partial one refuses naming the missing
// half (§4.7).
func buildComplexDefs(
	_ *parser, asm *assembly, spec *loopSpec, owner string,
) ([]*activities.ComplexBehaviorDefinition, error) {
	out := make([]*activities.ComplexBehaviorDefinition, 0, len(spec.complex))

	for i := range spec.complex {
		cx := &spec.complex[i]
		label := "complexBehaviorDefinition " + strconv.Quote(cx.id) + " of " + owner

		if cx.condition == nil || !cx.hasEvent || len(cx.eventDefs) == 0 {
			return nil, errs.New(
				errs.M("bpmn: %s is missing its %s; the model requires a "+
					"boolean condition AND an event to throw when it holds",
					label, complexMissing(cx)),
				errs.C(errorClass, errs.EmptyNotAllowed))
		}

		// One definition, or a refusal — never a silent eventDefs[0]:
		// the implicit throw carries exactly one trigger, and dropping
		// the file's extras would mask its intent.
		if len(cx.eventDefs) > 1 {
			return nil, errs.New(
				errs.M("bpmn: %s carries %d event definitions; the implicit "+
					"throw event carries exactly one — keep the one to throw",
					label, len(cx.eventDefs)),
				errs.C(errorClass, errs.InvalidObject))
		}

		cond, err := newBoolExpression(*cx.condition, asm.exprLanguage)
		if err != nil {
			return nil, err
		}

		built, err := defBuilders[cx.eventDefs[0].local](asm, label, cx.eventDefs[0])
		if err != nil {
			return nil, err
		}

		// The event's name is model plumbing (the implicit throw is
		// engine-emitted, never drawn) — the id when the file gave one,
		// a derived label otherwise.
		name := cx.id
		if name == "" {
			name = "behavior event"
		}

		// Unreachable for any document: the definition is non-nil (built
		// above) and the name is non-empty by the fallback. Said in the
		// form the coverage gate reads.
		ev, err := events.NewImplicitThrowEvent(name, built.def)
		if err != nil {
			return nil, errs.Invariant(
				"%s rejected its implicit throw: %w", label, err)
		}

		// Unreachable too: both halves are non-nil and the condition is
		// boolean by lite.Cond's own declaration.
		cbd, err := activities.NewComplexBehaviorDefinition(cond, ev)
		if err != nil {
			return nil, errs.Invariant(
				"%s rejected its own halves: %w", label, err)
		}

		out = append(out, cbd)
	}

	return out, nil
}

// complexMissing names the absent half in a partial complex definition.
func complexMissing(cx *complexSpec) string {
	if cx.condition == nil && (!cx.hasEvent || len(cx.eventDefs) == 0) {
		return "condition and event"
	}

	if cx.condition == nil {
		return "condition"
	}

	return "event (with an event definition inside)"
}

// buildStandardLoop builds the condition-driven kind.
//
// The grammar makes loopCondition 0..1; the model refuses nil — and
// passing the nil through would surface "NewStandardLoop: a nil
// loopCondition isn't allowed", true and useless to a modeler who does
// not know what gobpm's constructor is. The converter refuses first,
// with the § and the fix (SRD-089.H §4.2).
func buildStandardLoop(
	_ *parser, asm *assembly, s *nodeSpec, spec *loopSpec,
) (activities.LoopCharacteristics, error) {
	owner := "<" + s.se.Name.Local + "> " + strconv.Quote(s.id)

	if spec.condition == nil {
		return nil, errs.New(
			errs.M("bpmn: %s carries a standardLoopCharacteristics with no "+
				"loopCondition; a loop with no condition never decides to "+
				"stop, and this engine requires one (§13.3.6) — write a "+
				"boolean loopCondition, or count iterations with a "+
				"multiInstanceLoopCharacteristics", owner),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	cond, err := newBoolExpression(*spec.condition, asm.exprLanguage)
	if err != nil {
		return nil, err
	}

	var opts []activities.StandardLoopOption

	if spec.testBefore {
		opts = append(opts, activities.WithTestBefore())
	}

	if spec.loopMaximum != "" {
		// The model never sees the raw text, so a non-integer is the
		// converter's to refuse; the model's own positive-only guard
		// stays the model's voice, wrapped below.
		n, convErr := strconv.Atoi(spec.loopMaximum)
		if convErr != nil {
			return nil, errs.New(
				errs.M("bpmn: %s declares loopMaximum=%q, which is not an "+
					"integer (§13.3.6)", owner, spec.loopMaximum),
				errs.C(errorClass, errs.InvalidParameter))
		}

		opts = append(opts, activities.WithLoopMaximum(n))
	}

	lc, err := activities.NewStandardLoop(cond, opts...)
	if err != nil {
		return nil, wrapErr(
			"bpmn: couldn't build the standard loop of "+owner,
			errs.BulidingFailed, err)
	}

	return lc, nil
}

// parseLoopChar reads one loop marker into a loopSpec.
func (p *parser) parseLoopChar(se xml.StartElement) (*loopSpec, error) {
	spec := &loopSpec{
		kind:         se.Name.Local,
		id:           strings.TrimSpace(attrValue(se, "id")),
		loopMaximum:  strings.TrimSpace(attrValue(se, attrLoopMaximum)),
		testBefore:   attrBool(se, attrTestBefore, false),
		isSequential: attrBool(se, attrIsSequential, false),
		behavior:     strings.TrimSpace(attrValue(se, attrBehavior)),
		noneRef:      strings.TrimSpace(attrValue(se, attrNoneBehaviorEv)),
		oneRef:       strings.TrimSpace(attrValue(se, attrOneBehaviorEv)),
	}

	// The marker's own id joins the ledger and is NOT preserved: the
	// model's loop types carry an empty BaseElement, and nothing
	// references a marker's id (§4.5, the set-id precedent).
	if spec.id != "" {
		if err := p.claimID(spec.id, spec.kind); err != nil {
			return nil, err
		}
	}

	outer := p.owner
	if spec.id != "" {
		p.owner = spec.id
	}

	err := p.parseLoopBody(spec, se)

	p.owner = outer

	if err != nil {
		return nil, err
	}

	p.reportUnmappedAttrs(se, p.owner, nil)

	return spec, nil
}

// parseLoopBody walks the marker's children.
func (p *parser) parseLoopBody(spec *loopSpec, se xml.StartElement) error {
	for {
		tok, err := p.token()
		if err != nil {
			return err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			if err := p.parseLoopChild(spec, t); err != nil {
				return err
			}

		case xml.EndElement:
			if t.Name == se.Name {
				return nil
			}
		}
	}
}

// parseLoopChild handles one child of a loop marker.
func (p *parser) parseLoopChild(spec *loopSpec, se xml.StartElement) error {
	if se.Name.Space != nsBPMN {
		return p.skipElement()
	}

	switch se.Name.Local {
	case tagLoopCondition:
		return p.loopExpr(spec, &spec.condition, "loopCondition", se)

	case tagLoopCardinality:
		return p.loopExpr(spec, &spec.cardinality, "loopCardinality", se)

	case tagCompletionCond:
		return p.loopExpr(spec, &spec.completion, "completionCondition", se)

	case tagLoopDataInRef:
		return p.loopRef(&spec.inputRef, se)

	case tagLoopDataOutRef:
		return p.loopRef(&spec.outputRef, se)

	case tagInputDataItem:
		spec.hasInputItem = true

		return p.loopItem(&spec.inputItem, se)

	case tagOutputDataItem:
		spec.hasOutputItem = true

		return p.loopItem(&spec.outputItem, se)

	case tagComplexBehavior:
		return p.parseComplexBehavior(spec, se)
	}

	return p.settle(ctxNode, se)
}

// loopExpr reads one expression child into the exprSpec shape the
// language machinery consumes (SRD-089.B); an empty body means absent.
func (p *parser) loopExpr(
	spec *loopSpec, dst **exprSpec, role string, se xml.StartElement,
) error {
	body, err := p.readText(se)
	if err != nil {
		return err
	}

	if body = strings.TrimSpace(body); body == "" {
		return nil
	}

	*dst = &exprSpec{
		ownerKind: spec.kind,
		ownerID:   p.owner,
		role:      role,
		id:        attrValue(se, "id"),
		lang:      attrValue(se, "language"),
		body:      body,
	}

	return nil
}

// loopRef reads one IDREF child (loopDataInputRef/loopDataOutputRef).
func (p *parser) loopRef(dst *string, se xml.StartElement) error {
	ref, err := p.readText(se)
	if err != nil {
		return err
	}

	*dst = strings.TrimSpace(ref)

	return nil
}

// loopItem reads an inputDataItem/outputDataItem: the model's binding is
// its NAME (id fallback); a declared itemSubjectRef has no model slot —
// the item's type comes from the collection's elements at run time — and
// is reported rather than silently dropped (§4.5).
func (p *parser) loopItem(dst *string, se xml.StartElement) error {
	id := strings.TrimSpace(attrValue(se, "id"))

	if id != "" {
		if err := p.claimID(id, se.Name.Local); err != nil {
			return err
		}
	}

	if ref := strings.TrimSpace(attrValue(se, attrItemSubjectRef)); ref != "" {
		p.report(fallbackName(id, p.owner), attrItemSubjectRef,
			"types a Multi-Instance item, whose binding is a NAME — the "+
				"item's type comes from the collection's elements at run "+
				"time, so the declared type has nowhere to land")
	}

	*dst = fallbackName(id, strings.TrimSpace(attrValue(se, "name")))

	return p.skipElement()
}

// parseComplexBehavior reads one <complexBehaviorDefinition>.
func (p *parser) parseComplexBehavior(spec *loopSpec, se xml.StartElement) error {
	cx := complexSpec{id: strings.TrimSpace(attrValue(se, "id"))}

	if cx.id != "" {
		if err := p.claimID(cx.id, se.Name.Local); err != nil {
			return err
		}
	}

	for {
		tok, err := p.token()
		if err != nil {
			return err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			if err := p.parseComplexChild(&cx, t); err != nil {
				return err
			}

		case xml.EndElement:
			if t.Name == se.Name {
				spec.complex = append(spec.complex, cx)

				return nil
			}
		}
	}
}

// parseComplexChild handles one child of a complexBehaviorDefinition:
// its condition, or its implicit throw event (whose event definitions
// ride the .D defSpec shape).
func (p *parser) parseComplexChild(cx *complexSpec, se xml.StartElement) error {
	if se.Name.Space != nsBPMN {
		return p.skipElement()
	}

	switch se.Name.Local {
	case "condition":
		body, err := p.readText(se)
		if err != nil {
			return err
		}

		if body = strings.TrimSpace(body); body != "" {
			cx.condition = &exprSpec{
				ownerKind: tagComplexBehavior,
				ownerID:   fallbackName(cx.id, p.owner),
				role:      "condition",
				id:        attrValue(se, "id"),
				lang:      attrValue(se, "language"),
				body:      body,
			}
		}

		return nil

	case "event":
		return p.parseImplicitEvent(cx, se)
	}

	return p.settle(ctxNode, se)
}

// parseImplicitEvent reads the complex behavior's implicit throw event:
// only its event definitions matter to the model's constructor.
func (p *parser) parseImplicitEvent(cx *complexSpec, se xml.StartElement) error {
	cx.hasEvent = true

	if id := strings.TrimSpace(attrValue(se, "id")); id != "" {
		if err := p.claimID(id, "event"); err != nil {
			return err
		}
	}

	for {
		tok, err := p.token()
		if err != nil {
			return err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Space != nsBPMN {
				if err := p.skipElement(); err != nil {
					return err
				}

				continue
			}

			if _, isDef := defBuilders[t.Name.Local]; isDef {
				body := nodeBody{}
				if err := p.parseEventDef(&body, t); err != nil {
					return err
				}

				cx.eventDefs = append(cx.eventDefs, body.defs...)

				continue
			}

			if err := p.settle(ctxNode, t); err != nil {
				return err
			}

		case xml.EndElement:
			if t.Name == se.Name {
				return nil
			}
		}
	}
}
