package bpmn

import (
	"encoding/xml"
	"strconv"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
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
	// testBefore, isSequential are the two boolean attributes.
	testBefore, isSequential bool
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
// builder table is. tagMultiInstance joins with its build (M3).
var loopBuilders = map[string]loopBuilder{
	tagStandardLoop: buildStandardLoop,
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
		return p.loopItem(&spec.inputItem, se)

	case tagOutputDataItem:
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
