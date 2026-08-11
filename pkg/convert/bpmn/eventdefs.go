package bpmn

import (
	"encoding/xml"
	"strings"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/expression/lite"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
)

// defSpec is one <*EventDefinition> as pass 1 read it. Like a nodeSpec it
// is held rather than built, because what it refers to — a catalog object
// or an activity — may be declared later in the document (§4.7).
type defSpec struct {
	// local is the element name, which selects the builder.
	local string
	id    string
	// ref is the single catalog or flow-element id this definition names:
	// messageRef, signalRef, errorRef, escalationRef or activityRef. Only
	// one of them exists per definition, so one field carries it and
	// refAttrs remembers what the file called it.
	ref string
	// name is a link's name, the key link events are paired by.
	name string
	// opRef is a message definition's operationRef, the service operation
	// the message is exchanged through.
	opRef string
	// exprs are the expression children keyed by role: a timer's
	// timeDate/timeCycle/timeDuration, a conditional's condition.
	exprs map[string]exprSpec
	docs  []docSpec
	// wait is compensation's waitForCompletion, whose default is True
	// (elements/event-definitions.md:142).
	wait bool
}

// opts renders the definition's own construction options, id first.
func (s defSpec) opts() []options.Option {
	return append([]options.Option{foundation.WithID(s.id)}, docOptions(s.docs)...)
}

// refAttrs names the attribute each definition carries its reference in,
// so a refusal quotes the file's own vocabulary rather than "ref".
var refAttrs = map[string]string{
	tagMessageEventDef:    "messageRef",
	tagSignalEventDef:     "signalRef",
	tagErrorEventDef:      "errorRef",
	tagEscalationEventDef: "escalationRef",
	tagCompensateEventDef: "activityRef",
}

// linkPairingLoss is why an explicit link source/target does not survive
// the import.
const linkPairingLoss = "names the link's counterpart explicitly, while this " +
	"engine pairs link events by their name; the link imports under its name, " +
	"so a counterpart named differently will not connect to it"

// builtDef is one event definition together with the option that
// attaches it to a start or end event.
//
// Both come from the same builder because only there is the definition's
// concrete type known. A table mapping definition → option would have to
// assert that type back out of the flow.EventDefinition interface, and an
// assertion that "cannot fail" is exactly the kind that eventually does.
//
// A nil trigger means the model offers no way to put this definition on a
// start or end event. That is how Link is excluded without the converter
// owning the rule: the standard confines a link to the intermediate
// position (semantics/event-handling.md:67), and the model has no
// WithLinkTrigger to call.
type builtDef struct {
	def     flow.EventDefinition
	trigger options.Option
}

// defBuilder builds one event definition from its spec. owner names the
// element carrying it — "startEvent \"s1\"" — so every refusal points at
// something a reader can find in the file.
type defBuilder func(asm *assembly, owner string, s defSpec) (builtDef, error)

// defBuilders maps an event-definition element to its constructor. Keyed
// by tag exactly as nodeBuilders is, so the eleventh definition the
// standard may grow is one row.
var defBuilders = map[string]defBuilder{
	tagMessageEventDef:     buildMessageDef,
	tagTimerEventDef:       buildTimerDef,
	tagSignalEventDef:      buildSignalDef,
	tagErrorEventDef:       buildErrorDef,
	tagEscalationEventDef:  buildEscalationDef,
	tagConditionalEventDef: buildConditionalDef,
	tagTerminateEventDef:   buildTerminateDef,
	tagCancelEventDef:      buildCancelDef,
	tagCompensateEventDef:  buildCompensationDef,
	tagLinkEventDef:        buildLinkDef,
}

// parseEventDef reads one <*EventDefinition> element into the node body
// being collected. The definition is built in pass 2 — see defSpec.
func (p *parser) parseEventDef(body *nodeBody, se xml.StartElement) error {
	local := se.Name.Local

	s := defSpec{
		local: local,
		// A definition's id is optional in BPMN and required by the model,
		// so it falls back to its owner's — the same answer §4.2 gave a
		// catalog object. The owner is the element currently being parsed.
		id:    fallbackName(p.owner+":"+local, attrValue(se, "id")),
		ref:   strings.TrimSpace(attrValue(se, refAttrs[local])),
		name:  strings.TrimSpace(attrValue(se, "name")),
		wait:  attrBool(se, "waitForCompletion", true),
		exprs: map[string]exprSpec{},
	}

	if err := p.parseDefBody(&s, se); err != nil {
		return err
	}

	body.defs = append(body.defs, s)

	return nil
}

// parseDefBody reads an event definition's children: its expressions, its
// operationRef, its documentation.
func (p *parser) parseDefBody(s *defSpec, se xml.StartElement) error {
	for {
		tok, err := p.token()
		if err != nil {
			return err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			if err := p.parseDefChild(s, t); err != nil {
				return err
			}

		case xml.EndElement:
			if t.Name == se.Name {
				return nil
			}
		}
	}
}

// defExprRoles are the children that are expressions, and the role each
// plays for its definition.
var defExprRoles = map[string]bool{
	tagTimeDate:     true,
	tagTimeCycle:    true,
	tagTimeDuration: true,
	tagCondition:    true,
}

// parseDefChild handles one child of an event definition.
func (p *parser) parseDefChild(s *defSpec, se xml.StartElement) error {
	if se.Name.Space != nsBPMN {
		return p.skipElement()
	}

	local := se.Name.Local

	switch {
	case defExprRoles[local]:
		return p.parseDefExpr(s, se, local)

	case local == tagOperationRef:
		ref, err := p.readText(se)
		if err != nil {
			return err
		}

		s.opRef = strings.TrimSpace(ref)

		return nil

	case local == tagLinkSource, local == tagLinkTarget:
		// The model pairs link events BY NAME (link.go:26 takes a name and
		// nothing else), so an explicit reference to the counterpart has
		// nowhere to land. It is reported rather than skipped: a file whose
		// two links disagree — paired by ref, differently named — imports
		// as two links that do not connect, and only the report says so.
		if err := p.skipElement(); err != nil {
			return err
		}

		p.report(s.id, local, linkPairingLoss)

		return nil

	case local == tagDocumentation:
		d, err := p.parseDoc(se)
		if err != nil {
			return err
		}

		s.docs = append(s.docs, d)

		return nil
	}

	return p.settle(ctxEventDef, se)
}

// parseDefExpr reads one expression child of an event definition.
func (p *parser) parseDefExpr(s *defSpec, se xml.StartElement, role string) error {
	body, err := p.readText(se)
	if err != nil {
		return err
	}

	s.exprs[role] = exprSpec{
		ownerKind: s.local,
		ownerID:   s.id,
		role:      role,
		id:        strings.TrimSpace(attrValue(se, "id")),
		lang:      attrValue(se, "language"),
		body:      strings.TrimSpace(body),
	}

	return nil
}

// buildDefs builds every definition a node's body carried, together with
// the options that attach them to a start or end event.
func buildDefs(
	asm *assembly, owner string, body nodeBody,
) ([]builtDef, error) {
	out := make([]builtDef, 0, len(body.defs))

	for _, s := range body.defs {
		build, ok := defBuilders[s.local]
		if !ok {
			// Unreachable through nodeChildParsers, which routes only the
			// names this table also carries.
			return nil, errs.New(
				errs.M("bpmn: no event-definition constructor for %q", s.local),
				errs.C(errorClass, errs.InvalidObject))
		}

		bd, err := build(asm, owner, s)
		if err != nil {
			return nil, err
		}

		out = append(out, bd)
	}

	return out, nil
}

// triggerOptions renders built definitions as the options a start or end
// event takes, refusing one the model cannot put there.
func triggerOptions(owner string, defs []builtDef) ([]options.Option, error) {
	opts := make([]options.Option, 0, len(defs))

	for _, d := range defs {
		if d.trigger == nil {
			return nil, errs.New(
				errs.M("bpmn: %s carries a %s, which the model offers no way to "+
					"put on a start or end event; BPMN confines it to an "+
					"intermediate event (semantics/event-handling.md:67)",
					owner, d.def.Type()),
				errs.C(errorClass, errs.InvalidParameter))
		}

		opts = append(opts, d.trigger)
	}

	return opts, nil
}

// refSiteFor builds the referring end of a definition's reference.
func refSiteFor(owner string, s defSpec) refSite {
	return refSite{from: owner, attr: refAttrs[s.local], target: s.ref}
}

// resolveCatalogRef looks a definition's reference up in one of the
// catalog's maps.
//
// A missing id and an id of the wrong kind are different errors: the
// second one IS in the file, and telling its author it is missing sends
// them hunting a typo that is not there. asm.declared answers the
// flow-element half, and it is complete — pass 1 recorded every id before
// the first constructor ran.
func resolveCatalogRef[T any](
	asm *assembly, m map[string]T, site refSite, want string,
) (T, error) {
	if v, ok := m[site.target]; ok {
		return v, nil
	}

	var zero T

	if kind, taken := asm.cat.kinds[site.target]; taken {
		return zero, site.wrongKind(want, kind)
	}

	if kind, isNode := asm.declared[site.target]; isNode {
		return zero, site.wrongKind(want, kind)
	}

	return zero, site.notFound(want)
}

// buildMessageDef builds a message definition and, when the file names
// one, the service operation the message is exchanged through.
func buildMessageDef(asm *assembly, owner string, s defSpec) (builtDef, error) {
	msg, err := resolveCatalogRef(asm, asm.cat.messages, refSiteFor(owner, s), tagMessage)
	if err != nil {
		return builtDef{}, err
	}

	var op service.Operation

	if s.opRef != "" {
		spec, ok := asm.ops[s.opRef]
		if !ok {
			return builtDef{}, refSite{
				from: owner, attr: tagOperationRef, target: s.opRef,
			}.notFound("operation")
		}

		if op, err = service.NewOperation(spec.name, nil, nil, nil,
			foundation.WithID(spec.id)); err != nil {
			return builtDef{}, err
		}
	}

	def, err := events.NewMessageEventDefinition(msg, op, s.opts()...)
	if err != nil {
		return builtDef{}, err
	}

	return builtDef{def: def, trigger: events.WithMessageTrigger(def)}, nil
}

// buildSignalDef builds a signal definition.
func buildSignalDef(asm *assembly, owner string, s defSpec) (builtDef, error) {
	sig, err := resolveCatalogRef(asm, asm.cat.signals, refSiteFor(owner, s), tagSignal)
	if err != nil {
		return builtDef{}, err
	}

	def, err := events.NewSignalEventDefinition(sig, s.opts()...)
	if err != nil {
		return builtDef{}, err
	}

	return builtDef{def: def, trigger: events.WithSignalTrigger(def)}, nil
}

// buildErrorDef builds an error definition.
func buildErrorDef(asm *assembly, owner string, s defSpec) (builtDef, error) {
	e, err := resolveCatalogRef(asm, asm.cat.errors, refSiteFor(owner, s), tagError)
	if err != nil {
		return builtDef{}, err
	}

	def, err := events.NewErrorEventDefinition(e, s.opts()...)
	if err != nil {
		return builtDef{}, err
	}

	return builtDef{def: def, trigger: events.WithErrorTrigger(def)}, nil
}

// buildEscalationDef builds an escalation definition.
func buildEscalationDef(asm *assembly, owner string, s defSpec) (builtDef, error) {
	esc, err := resolveCatalogRef(
		asm, asm.cat.escalations, refSiteFor(owner, s), tagEscalation)
	if err != nil {
		return builtDef{}, err
	}

	def, err := events.NewEscalationEventDefinition(esc, s.opts()...)
	if err != nil {
		return builtDef{}, err
	}

	return builtDef{def: def, trigger: events.WithEscalationTrigger(def)}, nil
}

// buildConditionalDef builds a conditional definition from its
// <condition> child, which the model requires to yield a boolean
// (conditional.go:39-46) — exactly what the expression layer's
// newBoolExpression declares.
func buildConditionalDef(asm *assembly, owner string, s defSpec) (builtDef, error) {
	e, ok := s.exprs[tagCondition]
	if !ok {
		return builtDef{}, errs.New(
			errs.M("bpmn: %s carries a conditionalEventDefinition with no "+
				"<condition>; there is nothing to evaluate", owner),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	cond, err := newBoolExpression(e, asm.exprLanguage)
	if err != nil {
		return builtDef{}, err
	}

	def, err := events.NewConditionalEventDefinition(cond, s.opts()...)
	if err != nil {
		return builtDef{}, err
	}

	return builtDef{def: def, trigger: events.WithConditionalTrigger(def)}, nil
}

// buildTerminateDef builds a terminate definition, which takes nothing.
func buildTerminateDef(_ *assembly, _ string, s defSpec) (builtDef, error) {
	def, err := events.NewTerminateEventDefinition(s.opts()...)
	if err != nil {
		return builtDef{}, err
	}

	return builtDef{def: def, trigger: events.WithTerminateTrigger(def)}, nil
}

// buildCancelDef builds a cancel definition, which takes nothing.
func buildCancelDef(_ *assembly, _ string, s defSpec) (builtDef, error) {
	def, err := events.NewCancelEventDefinition(s.opts()...)
	if err != nil {
		return builtDef{}, err
	}

	return builtDef{def: def, trigger: events.WithCancelTrigger(def)}, nil
}

// buildCompensationDef builds a compensation definition around the
// activity it compensates.
//
// A missing activityRef is legal and means the whole enclosing scope
// (compensation.go:32-34), so only a reference that names something
// other than an activity is an error.
func buildCompensationDef(asm *assembly, owner string, s defSpec) (builtDef, error) {
	var activity flow.ActivityNode

	if s.ref != "" {
		site := refSiteFor(owner, s)

		node, ok := asm.byID[s.ref]
		if !ok {
			if kind, taken := asm.cat.kinds[s.ref]; taken {
				return builtDef{}, site.wrongKind("activity", kind)
			}

			return builtDef{}, site.notFound("activity")
		}

		if activity, ok = node.(flow.ActivityNode); !ok {
			return builtDef{}, site.wrongKind("activity", asm.declared[s.ref])
		}
	}

	def, err := events.NewCompensationEventDefinition(activity, s.wait, s.opts()...)
	if err != nil {
		return builtDef{}, err
	}

	return builtDef{def: def, trigger: events.WithCompensationTrigger(def)}, nil
}

// buildLinkDef builds a link definition.
//
// It yields no trigger: the standard confines a link event to the
// intermediate position (semantics/event-handling.md:67), and the model
// has no WithLinkTrigger — so a link on a start or end event is refused
// by the absence rather than by a rule the converter wrote down.
func buildLinkDef(_ *assembly, owner string, s defSpec) (builtDef, error) {
	if s.name == "" {
		return builtDef{}, errs.New(
			errs.M("bpmn: %s carries a linkEventDefinition with no name; a link "+
				"is paired with its counterpart BY name, so an unnamed one "+
				"connects nothing", owner),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	def, err := events.NewLinkEventDefinition(s.name, s.opts()...)
	if err != nil {
		return builtDef{}, err
	}

	return builtDef{def: def}, nil
}

// timerRoles are the three forms of a timer, and what this converter can
// do with each (§4.6). A form the engine cannot express carries the
// result type the expression language would have to declare.
var timerRefusals = map[string]string{
	tagTimeCycle:    "int",
	tagTimeDuration: "Duration",
}

// buildTimerDef builds a timer definition from its <timeDate>, and
// refuses the other two forms.
//
// NewTimerEventDefinition checks each expression's declared result type
// against a fixed table — timeDate→"Time", timeCycle→"int",
// timeDuration→"Duration" (timer.go:75-92) — and the lite engine emits
// only bool, float64, string and Time (lite.go:108-118). There is no
// "int" and no "Duration", so no expression an importer can write builds
// a recurrence or an interval. This is the same wall stage .B recorded
// against Camunda's failedJobRetryTimeCycle, and it gets the same
// verdict: refused with the reason named, not silently dropped.
func buildTimerDef(_ *assembly, owner string, s defSpec) (builtDef, error) {
	for role, resultType := range timerRefusals {
		if _, ok := s.exprs[role]; ok {
			return builtDef{}, errs.New(
				errs.M("bpmn: %s carries a <%s>, which this engine cannot express: "+
					"the model requires an expression declaring the result type %q, "+
					"and the expression language produces only bool, float64, "+
					"string and Time. Build the timer in Go, or use <timeDate>",
					owner, role, resultType),
				errs.C(errorClass, errs.InvalidParameter))
		}
	}

	e, ok := s.exprs[tagTimeDate]
	if !ok {
		return builtDef{}, errs.New(
			errs.M("bpmn: %s carries a timerEventDefinition with no <timeDate>; "+
				"there is no moment to fire at", owner),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	date, err := timeDateExpression(owner, e)
	if err != nil {
		return builtDef{}, err
	}

	def, err := events.NewTimerEventDefinition(date, nil, nil, s.opts()...)
	if err != nil {
		return builtDef{}, err
	}

	return builtDef{def: def, trigger: events.WithTimerTrigger(def)}, nil
}

// timeDateExpression turns a <timeDate> literal into the Time-typed
// expression the model demands.
//
// The literal is validated HERE rather than at evaluation. BPMN types the
// child as a bare Expression with no format constraint
// (elements/event-definitions.md:56-58), while lite's time() accepts
// RFC3339 alone (eval.go:411-426) — so a zone-less "2026-08-11T10:00:00"
// is something a modeler may legitimately write and this engine cannot
// read. Minting it anyway would move the failure to the first firing,
// long after the file that caused it is out of sight.
func timeDateExpression(owner string, e exprSpec) (data.FormalExpression, error) {
	if _, err := time.Parse(time.RFC3339, e.body); err != nil {
		return nil, errs.New(
			errs.M("bpmn: %s carries the <timeDate> %q, which is not an RFC3339 "+
				"instant; this engine reads a timer's date through lite's time(), "+
				"which accepts nothing else", owner, e.body),
			errs.C(errorClass, errs.InvalidParameter),
			errs.E(err))
	}

	return lite.Expr(
		"time('"+e.body+"')",
		data.WithResultType("Time"),
		foundation.WithID(e.exprID()))
}

// attrBool reads a BPMN boolean attribute, falling back to the standard's
// default when the file does not carry it.
func attrBool(se xml.StartElement, local string, def bool) bool {
	v := strings.TrimSpace(attrValue(se, local))
	if v == "" {
		return def
	}

	return strings.EqualFold(v, "true")
}
