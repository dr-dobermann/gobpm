package bpmn

import (
	"encoding/xml"
	"slices"
	"strconv"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/observability"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/lanes"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// parseCtx names the position in the document where an element was met.
// The same local name means different things in two contexts —
// <operation> under <interface> is a service operation, while the same
// name elsewhere is not — so dispatch is keyed by context and name
// together (SRD-089.A §3).
type parseCtx uint8

const (
	ctxDefinitions parseCtx = iota
	ctxProcess
	ctxNode
	ctxSequenceFlow
	ctxInterface
	ctxOperation
	ctxCatalog
	ctxEventDef
)

// elementKey identifies one element in one parse context.
type elementKey struct {
	local string
	ctx   parseCtx
}

// dispositionKind is what the converter does with an in-namespace element
// no parser claims.
type dispositionKind uint8

const (
	// refused is the default, and it is deliberately the zero value: an
	// element that no parser table claims and no policy row names is
	// reported as unsupported. Absence from the tables can therefore
	// never mean silent acceptance (ADR-024 §2.9).
	refused dispositionKind = iota

	// skipped swallows the element's subtree without a word. It is only
	// correct where dropping the element leaves the imported definition
	// meaning the same thing.
	skipped

	// notYet refuses an element that is waiting on a subsystem rather
	// than on this converter. The same file imports unchanged once that
	// subsystem lands, and saying so is the difference between "come
	// back later" and "rewrite your diagram" (ADR-024 §2.13).
	notYet

	// notExpressible refuses an element whose XML form and model form do
	// not correspond — the engine executes it, and no mechanical reading
	// of the document can produce it. Neither waiting nor a later slice
	// helps; the reason names what to do instead.
	notExpressible
)

// refusalReasons explains a notExpressible element. Without the reason
// such a refusal reads as an oversight, which is the one thing it is not.
var refusalReasons = map[string]string{
	tagComplexGateway: "BPMN carries its activation as an `activationCondition` " +
		"expression, while this engine's complex gateway is activated by " +
		"per-incoming-flow token counts — a threshold or a set of triples. " +
		"The two describe the same domain in forms a converter cannot " +
		"translate between: recovering counts from an arbitrary Boolean " +
		"expression is not mechanical, and guessing one changes WHEN the " +
		"gateway fires. Build it programmatically with WithActivationThreshold " +
		"or WithActivation",

	tagAdHocSubProcess: "an ad-hoc container is entered by a host-supplied " +
		"adhoc.Router — a Go value deciding which of its activities run and " +
		"in what order (ADR-035 §2.1) — where the document carries only a " +
		"completion condition. A file cannot contain a Router, and inventing " +
		"one would decide the container's execution order on the modeler's " +
		"behalf. Build it programmatically with activities.WithAdHoc",
}

// annotations are the BPMN-namespace children carrying no execution
// semantics, skipped in every context that can hold one. They are
// near-universal in modeler output, so refusing them would reject files
// whose flow graph is entirely supported (ADR-024 §2.6).
//
// <documentation> left this list when the model gained a place to put it:
// it is now parsed wherever a gobpm element can carry it (a process, a
// flow node, a sequence flow) and skipped by declaration where none can
// (see policy).
var annotations = []string{tagExtensionElems}

// policy declares every non-default disposition that is not context-wide.
// Lookup order is: the context's parser table, then this table, then
// refused — so an element appears in exactly one place and the three
// cannot drift apart.
var policy = map[elementKey]dispositionKind{
	// The GlobalTask family is reuse BY REFERENCE: a task defined once at
	// definitions level and invoked through a callActivity. Resolving that
	// reference needs a registry of callable definitions, which is the
	// server tier's, so these are refused as a deferral rather than as a
	// verdict on the file.
	{local: "globalTask", ctx: ctxDefinitions}:             notYet,
	{local: "globalUserTask", ctx: ctxDefinitions}:         notYet,
	{local: "globalManualTask", ctx: ctxDefinitions}:       notYet,
	{local: "globalScriptTask", ctx: ctxDefinitions}:       notYet,
	{local: "globalBusinessRuleTask", ctx: ctxDefinitions}: notYet,

	// The complex gateway is executable here and unreachable from XML —
	// see refusalReasons. The ad-hoc container is the same class for a
	// different reason: its entry point is a Go closure.
	{local: tagComplexGateway, ctx: ctxProcess}:  notExpressible,
	{local: tagAdHocSubProcess, ctx: ctxProcess}: notExpressible,

	// A node's incoming/outgoing duplicate the wiring <sequenceFlow>
	// already carries through sourceRef/targetRef.
	{local: tagIncoming, ctx: ctxNode}: skipped,
	{local: tagOutgoing, ctx: ctxNode}: skipped,

	// <operation> carries exactly three children (inMessageRef,
	// outMessageRef, errorRef — BPMN elements/service-interfaces.md
	// :41-43). The first two are parsed; errorRef is catalog detail this
	// slice does not bind, so it is skipped by declaration rather than by
	// a lenient default.
	{local: tagErrorRef, ctx: ctxOperation}: skipped,

	// <documentation> is imported wherever the model can hold it. These
	// three contexts have no model element to attach it to — <definitions>
	// and <interface> are not gobpm objects at all, and an operation is a
	// catalog stub — so it is dropped here by declaration.
	{local: tagDocumentation, ctx: ctxDefinitions}: skipped,
	{local: tagDocumentation, ctx: ctxInterface}:   skipped,
	{local: tagDocumentation, ctx: ctxOperation}:   skipped,

	// Visual artifacts. The vendored extract calls these "pure visual",
	// and they are near-universal in modeler output — a file was being
	// refused for carrying a comment. Dropping them leaves the imported
	// definition meaning the same thing, which is the test ADR-024
	// §2.9 sets for skipping rather than refusing.
	//
	// <association> is NOT one of them and stays refused: the extract
	// keeps it in scope because it carries compensation semantics, so it
	// is mapped work for the composites stage, not a comment to skip.
	{local: tagTextAnnotation, ctx: ctxProcess}: skipped,
	{local: tagGroup, ctx: ctxProcess}:          skipped,
	{local: tagCategory, ctx: ctxDefinitions}:   skipped,

	// Not execution-related (extract, out-of-scope table).
	{local: tagRelationship, ctx: ctxDefinitions}: skipped,

	// <import> declares an external type/schema namespace. foundation.Import
	// exists, but only as an ItemDefinition field — there is no
	// definitions-level container to hold a document's imports, and this
	// stage maps no itemDefinition, so nothing could consult one. Skipping
	// it changes nothing TODAY; when itemDefinition lands with the data
	// stage, its typeRef makes the declaration meaningful and this row has
	// to be revisited with it.
	{local: tagImport, ctx: ctxDefinitions}: skipped,
}

// sections pins the BPMN 2.0 § for elements the converter refuses, so the
// UnsupportedElementError carries actionable modeler feedback (ADR-024
// §2.7 / SAD-001 §5). A tag absent from the table yields an error
// with no §.
//
// An element being importable does NOT retire its row. The tables claim
// an element in a CONTEXT — <subProcess> is claimed under <process> and
// under another container, and refused anywhere else — so the § stays
// reachable for every context that does not claim it. Removing a row
// because the element now imports somewhere costs the § exactly where a
// modeler put the element somewhere it does not belong, which is when
// they need it most.
var sections = map[string]string{
	"sendTask":                         "§13.3.3",
	"receiveTask":                      "§13.3.3",
	"scriptTask":                       "§13.3.3",
	"businessRuleTask":                 "§13.3.3",
	"callActivity":                     "§13.3.3",
	"subProcess":                       "§13.3.4",
	"adHocSubProcess":                  "§13.3.4",
	"transaction":                      "§13.3.4",
	"complexGateway":                   "§13.4",
	"intermediateCatchEvent":           "§13.5",
	"intermediateThrowEvent":           "§13.5",
	"boundaryEvent":                    "§13.5.5",
	"messageEventDefinition":           "§13.5",
	"timerEventDefinition":             "§13.5",
	"signalEventDefinition":            "§13.5",
	"errorEventDefinition":             "§13.5",
	"escalationEventDefinition":        "§13.5",
	"compensateEventDefinition":        "§13.5",
	"conditionalEventDefinition":       "§13.5",
	"linkEventDefinition":              "§13.5",
	"terminateEventDefinition":         "§13.5",
	"cancelEventDefinition":            "§13.5",
	"laneSet":                          "§10.5",
	"lane":                             "§10.5",
	"dataObject":                       "§10.3",
	"dataObjectReference":              "§10.3",
	"dataStoreReference":               "§10.3",
	"collaboration":                    "§10.1",
	"participant":                      "§10.1",
	"messageFlow":                      "§10.1",
	"ioSpecification":                  "§10.3",
	"property":                         "§10.3",
	"dataInput":                        "§10.3",
	"dataOutput":                       "§10.3",
	"dataInputAssociation":             "§10.3",
	"dataOutputAssociation":            "§10.3",
	"multiInstanceLoopCharacteristics": "§13.3.5",
	"standardLoopCharacteristics":      "§13.3.5",

	// The Conversation family is a separate conformance concern; the
	// vendored extract pins its metamodel at §9.5.1
	// (semantics/correlation.md:210). The Choreography family is refused
	// too, but carries no § here — the extract does not pin one, and a
	// section asserted from memory is worse feedback than none.
	"conversation":            "§9.5.1",
	"subConversation":         "§9.5.1",
	"callConversation":        "§9.5.1",
	"globalConversation":      "§9.5.1",
	"conversationLink":        "§9.5.1",
	"conversationAssociation": "§9.5.1",
}

// dispositionFor answers what to do with local in ctx when no parser
// table claims it.
func dispositionFor(ctx parseCtx, local string) dispositionKind {
	if slices.Contains(annotations, local) {
		return skipped
	}

	return policy[elementKey{local: local, ctx: ctx}]
}

// settle applies the disposition of an unclaimed in-namespace element:
// skip its subtree, or refuse it with its pinned spec section.
//
// <extensionElements> is skipped THROUGH the reporting walk: it carries
// no execution semantics of its own, which is why it is skipped, but it
// is also where a recognized dialect keeps the constructs a host needs
// to be told about.
func (p *parser) settle(ctx parseCtx, se xml.StartElement) error {
	switch dispositionFor(ctx, se.Name.Local) {
	case skipped:
		if se.Name.Local == tagExtensionElems {
			return p.skipReporting(p.owner)
		}

		return p.skipElement()

	case notYet:
		return notSupportedYet(se)

	case notExpressible:
		return notExpressibleHere(se)
	}

	return unsupported(se)
}

// defsParser parses one child of <bpmn:definitions>. It returns a
// non-nil assembly when the child was the document's first <process>.
type defsParser func(p *parser, asm *assembly, se xml.StartElement) (*assembly, error)

// definitionsParsers claims the children of <bpmn:definitions>.
var definitionsParsers = map[string]defsParser{
	tagInterface:  parseInterfaceElem,
	tagProcess:    parseProcessElem,
	tagMessage:    parseCatalogElem,
	tagSignal:     parseCatalogElem,
	tagError:      parseCatalogElem,
	tagEscalation: parseCatalogElem,
}

// parseCatalogElem parses one definitions-level catalog object — the
// <message>, <signal>, <error> or <escalation> an event definition refers
// to.
func parseCatalogElem(p *parser, _ *assembly, se xml.StartElement) (*assembly, error) {
	return nil, p.parseCatalogElement(se)
}

// parseInterfaceElem parses a definitions-level service catalog entry.
func parseInterfaceElem(p *parser, _ *assembly, se xml.StartElement) (*assembly, error) {
	return nil, p.parseInterface(se)
}

// parseProcessElem parses the document's process; a second one is
// refused, since Import yields one definition (the document-level
// capability arrives with the collaboration slice).
func parseProcessElem(p *parser, asm *assembly, se xml.StartElement) (*assembly, error) {
	if asm != nil {
		return nil, unsupported(se)
	}

	return p.parseProcess(se)
}

// procParser parses one child of <bpmn:process>.
type procParser func(p *parser, asm *assembly, se xml.StartElement) error

// processParsers claims the flow elements of <bpmn:process>. Every flow
// node is derived from nodeBuilders rather than listed again, so the two
// tables cannot disagree about which elements are nodes — the drift a
// second hand-maintained list would invite.
var processParsers = func() map[string]procParser {
	pp := map[string]procParser{
		tagSequenceFlow: parseSequenceFlowElem,
	}

	for local := range nodeBuilders {
		pp[local] = parseNodeElem
	}

	return pp
}()

// A container's children are flow elements, not a node body, so it takes
// a different PARSER while keeping its builder in nodeBuilders. It is a
// container that is one element where the two legitimately differ —
// everywhere else the table is derived, which is what keeps parser and
// builder in step.
//
// Wired here rather than in the initializer above because
// parseContainerChild dispatches through processParsers: naming the
// parser inside the expression that builds the table is an
// initialization cycle, even though the call happens long afterwards.
func init() { //nolint:gochecknoinits // breaks the processParsers cycle
	processParsers[tagSubProcess] = parseContainerElem
	processParsers[tagTransaction] = parseContainerElem
	processParsers[tagAssociation] = parseAssociationElem
}

// parseContainerElem reads one container element and everything it holds.
func parseContainerElem(p *parser, asm *assembly, se xml.StartElement) error {
	return p.parseContainer(asm, se)
}

// buildSubProcess builds a Sub-Process in whichever of its variants the
// file wrote. Its inner elements are added afterwards by buildNodes, from
// the specs that named it as their container — the shape rules are the
// model's, checked at Validate.
//
// The variants differ by one option each, and both are passed exactly as
// the file wrote them. A <transaction triggeredByEvent="true"> therefore
// reaches NewSubProcess carrying both, and is refused by the model, whose
// message names the ADR clause (ADR-028 §2.6). Deciding here which of the
// two the author "meant" would be the converter holding a second copy of
// a model rule (SRD-089.E §4.4).
func buildSubProcess(
	p *parser, asm *assembly, se xml.StartElement, id, name string, body nodeBody,
) (flow.Node, error) {
	opts := body.opts(id)

	sets, err := buildLaneSets(&asm.places, body.laneSets)
	if err != nil {
		return nil, err
	}

	if len(sets) != 0 {
		opts = append(opts, lanes.WithLaneSets(sets...))
	}

	if attrBool(se, attrTriggeredByEvent, false) {
		opts = append(opts, activities.WithTriggeredByEvent())
	}

	if se.Name.Local == tagTransaction {
		txOpts, err := transactionOptions(p, se, id)
		if err != nil {
			return nil, err
		}

		opts = append(opts, txOpts...)
	}

	return activities.NewSubProcess(fallbackName(id, name), opts...)
}

// buildCallActivity builds a Call Activity keyed by calledElement.
//
// The registry is NOT consulted, here or anywhere in the import: the
// model resolves a callable at CALL time (ADR-023 §2.7), so the callable
// may be registered after the file is imported, or re-versioned later. An
// import that failed because a callable was not yet registered would make
// import order significant, which is the property call-time resolution
// exists to avoid.
//
// An empty key is refused by NewCallActivity in its own words, so nothing
// is checked for it here.
func buildCallActivity(
	_ *parser, _ *assembly, se xml.StartElement, id, name string, body nodeBody,
) (flow.Node, error) {
	key := strings.TrimSpace(attrValue(se, attrCalledElement))

	if prefix, _, found := strings.Cut(key, ":"); found {
		return nil, errs.New(
			errs.M("bpmn: callActivity %q: calledElement %q names a callable "+
				"in another definitions document (prefix %q), which needs a "+
				"callable-resolution seam this engine does not have (#325). "+
				"Taking the text as a key would silently call whatever the "+
				"host happens to have registered under it. Register the "+
				"called process under a key and name that key",
				id, key, prefix),
			errs.C(errorClass, errs.InvalidParameter))
	}

	return activities.NewCallActivity(fallbackName(id, name), key, body.opts(id)...)
}

// transactionMethods says, per BPMN Transaction `method` value, whether
// this engine realizes that abort protocol (ADR-028 §2.7). The empty key
// is the absent attribute: BPMN's default is compensate.
//
// A table rather than a switch because it is a fixed classification, and
// because the one thing a reader needs from it — which values are in and
// which are out — should be readable in one glance.
var transactionMethods = map[string]bool{
	"":           true,
	"compensate": true,
	"store":      false,
	"image":      false,
}

// transactionOptions reads a <transaction>'s own attributes.
//
// `method` is not a lost datum, it is the abort semantics: importing
// store or image as compensate would hand back a process that undoes by
// running handlers where the document said the resource managers roll
// back, and a report calling that a dropped attribute would understate it
// (SRD-089.E §4.5). So the two the engine does not realize are refused,
// and the one it does is silent — reporting the value the engine
// implements would train a host to ignore the report.
//
// `protocol` names the coordinator those two would have needed, so with
// the only supported method nothing executable depends on it: reported,
// and the transaction imports without it.
func transactionOptions(
	p *parser, se xml.StartElement, id string,
) ([]options.Option, error) {
	method := strings.TrimSpace(attrValue(se, attrTransactionMethod))

	realized, known := transactionMethods[method]
	if !known {
		return nil, errs.New(
			errs.M("bpmn: transaction %q: method %q is not one of BPMN's "+
				"compensate, store or image (§10.7)", id, method),
			errs.C(errorClass, errs.InvalidParameter))
	}

	if !realized {
		return nil, errs.New(
			errs.M("bpmn: transaction %q: method %q selects resource-manager "+
				"coordination this engine does not implement and has decided "+
				"not to (ADR-028 §2.7) — only compensate, undo by compensation "+
				"handlers, is a process-level mechanism it can realize. Model "+
				"the undo as compensation handlers", id, method),
			errs.C(errorClass, errs.InvalidParameter))
	}

	if proto := strings.TrimSpace(attrValue(se, attrTransactionProto)); proto != "" {
		p.report(id, attrTransactionProto,
			"names a coordination protocol, which only means something for "+
				"the store and image methods this engine does not implement "+
				"(ADR-028 §2.7); the transaction imports without it")
	}

	return []options.Option{activities.WithTransaction()}, nil
}

// parseNodeElem builds one flow node and records it in the assembly.
func parseNodeElem(p *parser, asm *assembly, se xml.StartElement) error {
	return p.parseNode(asm, se)
}

// parseSequenceFlowElem records one sequence flow for pass 2.
func parseSequenceFlowElem(p *parser, asm *assembly, se xml.StartElement) error {
	fs, err := p.parseSequenceFlow(se)
	if err != nil {
		return err
	}

	asm.flows = append(asm.flows, *fs)

	return nil
}

// nodeChildParser parses one child of a flow node into the body being
// collected before the node is built.
type nodeChildParser func(p *parser, body *nodeBody, se xml.StartElement) error

// nodeChildParsers claims the children of a flow node that take part in
// building it. The node's children are read BEFORE its constructor runs,
// because the model has no way to attach documentation afterwards —
// foundation.BaseElement exposes ID() and Docs() and nothing that sets
// either. The later element stages need the same order for a stronger
// reason: an event definition or a loop characteristic decides WHAT is
// built, not how it is decorated.
var nodeChildParsers = func() map[string]nodeChildParser {
	ncp := map[string]nodeChildParser{
		tagDocumentation: parseNodeDocElem,
		tagScript:        parseScriptElem,
	}

	// Every event definition is a node child, derived from defBuilders
	// rather than listed again so the two tables cannot disagree about
	// which children are definitions.
	for local := range defBuilders {
		ncp[local] = parseEventDefElem
	}

	return ncp
}()

// parseEventDefElem records one <*EventDefinition> child of an event.
func parseEventDefElem(p *parser, body *nodeBody, se xml.StartElement) error {
	return p.parseEventDef(body, se)
}

// parseScriptElem records a script task's body. It is read as text, not
// interpreted: whether the engine can run it is the format's question,
// answered before the task is built.
func parseScriptElem(p *parser, body *nodeBody, se xml.StartElement) error {
	text, err := p.readText(se)
	if err != nil {
		return err
	}

	body.script = text

	return nil
}

// parseNodeDocElem records one <documentation> child of a flow node.
func parseNodeDocElem(p *parser, body *nodeBody, se xml.StartElement) error {
	d, err := p.parseDoc(se)
	if err != nil {
		return err
	}

	body.docs = append(body.docs, d)

	return nil
}

// nodeBuilder builds the model node behind one flow-node element. id and
// name are already resolved (name falls back to the id where the model
// demands one), and body carries what the element's children contributed.
type nodeBuilder func(
	p *parser, asm *assembly, se xml.StartElement, id, name string, body nodeBody,
) (flow.Node, error)

// nodeBuilders maps a flow-node element to its model constructor — the
// heart of the import mapping, kept as a table so a new element is a row
// rather than a case.
var nodeBuilders = map[string]nodeBuilder{
	tagStartEvent:        buildStartEvent,
	tagEndEvent:          buildEndEvent,
	tagIntermediateCatch: buildIntermediateCatch,
	tagIntermediateThrow: buildIntermediateThrow,
	tagBoundaryEvent:     buildBoundaryEvent,
	tagSendTask:          buildSendTask,
	tagReceiveTask:       buildReceiveTask,

	tagTask:       buildManualTask,
	tagManualTask: buildManualTask,

	tagSubProcess:   buildSubProcess,
	tagTransaction:  buildSubProcess,
	tagCallActivity: buildCallActivity,

	tagUserTask: func(
		p *parser, _ *assembly, se xml.StartElement, id, name string, body nodeBody,
	) (flow.Node, error) {
		// gobpm's UserTask demands at least one output resource parameter
		// (bpmncommon.NewResource rejects an empty parameter list), while
		// this slice carries no ioSpecification — so import synthesizes one
		// optional placeholder output. It is model plumbing, not BPMN
		// content, and is not written back on export.
		opts := append(body.opts(id),
			activities.WithoutParams(),
			activities.WithOutput("result", typeBool, false))

		return activities.NewUserTask(fallbackName(id, name),
			append(opts, p.camundaOptions(se, id)...)...)
	},

	tagServiceTask: func(
		p *parser, _ *assembly, se xml.StartElement, id, name string, body nodeBody,
	) (flow.Node, error) {
		return p.parseServiceTask(se, id, name, body)
	},

	tagExclusiveGateway: buildGateway,
	tagParallelGateway:  buildGateway,
	tagInclusiveGateway: buildGateway,
	tagEventBasedGtw:    buildGateway,
	tagScriptTask:       buildScriptTask,
	tagBusinessRuleTask: buildBusinessRuleTask,
}

// buildStartEvent builds a start event, typed by whichever definitions
// its body carried.
//
// A start and an end event take no positional definition, only options —
// so the definitions arrive through the trigger each builder produced,
// and one the model has no trigger for is refused there rather than here.
func buildStartEvent(
	_ *parser, asm *assembly, se xml.StartElement, id, name string, body nodeBody,
) (flow.Node, error) {
	opts, err := eventOptions(asm, se, id, body)
	if err != nil {
		return nil, err
	}

	return events.NewStartEvent(name, append(opts, startAttrOptions(se)...)...)
}

// buildEndEvent builds an end event, typed by its definitions.
func buildEndEvent(
	_ *parser, asm *assembly, se xml.StartElement, id, name string, body nodeBody,
) (flow.Node, error) {
	opts, err := eventOptions(asm, se, id, body)
	if err != nil {
		return nil, err
	}

	return events.NewEndEvent(name, opts...)
}

// eventOptions renders a node body as the options an event constructor
// takes: its id, its documentation, and its definitions as triggers.
func eventOptions(
	asm *assembly, se xml.StartElement, id string, body nodeBody,
) ([]options.Option, error) {
	owner := se.Name.Local + " " + strconv.Quote(id)

	defs, err := buildDefs(asm, owner, body)
	if err != nil {
		return nil, err
	}

	triggers, err := triggerOptions(owner, defs)
	if err != nil {
		return nil, err
	}

	return append(body.opts(id), triggers...), nil
}

// buildIntermediateCatch builds an intermediate catch event around the
// single definition it waits for.
func buildIntermediateCatch(
	_ *parser, asm *assembly, se xml.StartElement, id, name string, body nodeBody,
) (flow.Node, error) {
	def, err := soleDefinition(asm, se, id, body)
	if err != nil {
		return nil, err
	}

	return events.NewIntermediateCatchEvent(
		fallbackName(id, name), def, body.opts(id)...)
}

// buildIntermediateThrow builds an intermediate throw event around the
// single definition it throws.
func buildIntermediateThrow(
	_ *parser, asm *assembly, se xml.StartElement, id, name string, body nodeBody,
) (flow.Node, error) {
	def, err := soleDefinition(asm, se, id, body)
	if err != nil {
		return nil, err
	}

	return events.NewIntermediateThrowEvent(
		fallbackName(id, name), def, body.opts(id)...)
}

// buildSendTask builds a send task around the message it sends.
func buildSendTask(
	p *parser, asm *assembly, se xml.StartElement, id, name string, body nodeBody,
) (flow.Node, error) {
	msg, err := taskMessage(p, asm, se, id)
	if err != nil {
		return nil, err
	}

	return activities.NewSendTask(fallbackName(id, name), msg,
		append(body.opts(id), p.camundaOptions(se, id)...)...)
}

// buildReceiveTask builds a receive task around the message it waits for.
func buildReceiveTask(
	p *parser, asm *assembly, se xml.StartElement, id, name string, body nodeBody,
) (flow.Node, error) {
	msg, err := taskMessage(p, asm, se, id)
	if err != nil {
		return nil, err
	}

	opts := append(body.opts(id), p.camundaOptions(se, id)...)

	// instantiate marks a receive task that STARTS a process instance
	// rather than waiting inside one; its default is false.
	if attrBool(se, "instantiate", false) {
		opts = append(opts, activities.WithInstantiate())
	}

	return activities.NewReceiveTask(fallbackName(id, name), msg, opts...)
}

// messagingTaskLosses are the attributes BPMN gives a send and a receive
// task that this model has nowhere to put, and why.
//
// Both are reported rather than dropped. `implementation` names the
// mechanism the message travels by, and a SendTask's field for it has no
// setter — reading it back would report a mechanism the engine never
// used. `operationRef` binds the task to a service operation, which these
// two constructors do not take at all.
var messagingTaskLosses = map[string]string{
	observability.AttrImplementation: "names the mechanism the message travels " +
		"by, while this engine always exchanges it through the MessageBroker; " +
		"the task imports and sends the same message either way",
	"operationRef": "binds the task to a service operation, which the send and " +
		"receive constructors do not take — use a serviceTask when the exchange " +
		"IS the operation",
}

// taskMessage resolves the message a send or receive task exchanges, and
// reports the attributes around it that the model cannot hold.
//
// messageRef is required here even though BPMN makes it optional: both
// constructors reject a nil message (send_task.go:42, receive_task.go:71),
// and a messaging task with no message has nothing to send or wait for.
func taskMessage(
	p *parser, asm *assembly, se xml.StartElement, id string,
) (*bpmncommon.Message, error) {
	owner := se.Name.Local + " " + strconv.Quote(id)

	for attr, reason := range messagingTaskLosses {
		if strings.TrimSpace(attrValue(se, attr)) != "" {
			p.report(id, attr, reason)
		}
	}

	ref := strings.TrimSpace(attrValue(se, attrMessageRef))
	if ref == "" {
		return nil, errs.New(
			errs.M("bpmn: %s names no messageRef; a messaging task has nothing "+
				"to send or wait for without one", owner),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	return resolveCatalogRef(asm, asm.cat.messages,
		refSite{from: owner, attr: attrMessageRef, target: ref}, tagMessage)
}

// buildBoundaryEvent builds a boundary event attached to the activity its
// attachedToRef names.
//
// Which triggers may sit on a boundary, whether an Error boundary may be
// non-interrupting, and whether a Cancel boundary's host is a transaction
// are all the model's rules (boundary.go:87-124) and stay there: the
// converter passes the definition and reports the refusal with the file's
// element id attached, which is the one thing the model cannot do (§4.3).
func buildBoundaryEvent(
	_ *parser, asm *assembly, se xml.StartElement, id, name string, body nodeBody,
) (flow.Node, error) {
	owner := se.Name.Local + " " + strconv.Quote(id)

	host, err := attachedActivity(asm, owner, se, id)
	if err != nil {
		return nil, err
	}

	def, err := soleDefinition(asm, se, id, body)
	if err != nil {
		return nil, err
	}

	// A compensation boundary takes its handler rather than a
	// cancelActivity flag, and BPMN names that handler through the
	// <association> leaving the event. The model has its own constructor
	// for the pair, including the isForCompensation check on the handler,
	// whose message names the option a modeler's file is missing.
	if def.Type() == flow.TriggerCompensation {
		handler, err := compensationHandler(asm, owner, id)
		if err != nil {
			return nil, err
		}

		ced, ok := def.(*events.CompensationEventDefinition)
		if !ok {
			return nil, errs.New(
				errs.M("bpmn: %s carries a compensation trigger on a %T",
					owner, def),
				errs.C(errorClass, errs.InvalidObject))
		}

		return events.NewCompensationBoundaryEvent(
			fallbackName(id, name), host, ced, handler, body.opts(id)...)
	}

	return events.NewBoundaryEvent(
		fallbackName(id, name), host, def,
		// The standard's default is interrupting
		// (elements/events.md:252).
		attrBool(se, "cancelActivity", true),
		body.opts(id)...)
}

// attachedActivity resolves a boundary event's attachedToRef.
//
// A boundary event with no host is not a modeling detail to fall back
// from — it is an event with nothing to guard, so the reference is
// required rather than optional.
func attachedActivity(
	asm *assembly, owner string, se xml.StartElement, id string,
) (flow.ActivityNode, error) {
	site := refSite{
		from:   owner,
		attr:   attrAttachedTo,
		target: strings.TrimSpace(attrValue(se, attrAttachedTo)),
	}

	if site.target == "" {
		return nil, errs.New(
			errs.M("bpmn: boundaryEvent %q names no attachedToRef; a boundary "+
				"event guards an activity, and one attached to nothing has no "+
				"meaning to import", id),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	node, ok := asm.byID[site.target]
	if !ok {
		if kind, taken := asm.cat.kinds[site.target]; taken {
			return nil, site.wrongKind("activity", kind)
		}

		return nil, site.notFound("activity")
	}

	host, ok := node.(flow.ActivityNode)
	if !ok {
		return nil, site.wrongKind("activity", asm.declared[site.target])
	}

	return host, nil
}

// soleDefinition returns the one definition an intermediate event
// carries.
//
// Both constructors take it POSITIONALLY and reject nil
// (intermediate_catch.go:43, intermediate_throw.go:49), so an
// intermediate event with no definition cannot be built — and the
// standard has no untyped intermediate event either, so nothing legal is
// lost by saying so.
//
// More than one is refused rather than silently reduced. BPMN lets a
// catch event list several triggers, while this model takes exactly one
// (§4.10); importing the first would produce an event that waits for
// less than the file asked for, and nothing downstream would ever say so.
func soleDefinition(
	asm *assembly, se xml.StartElement, id string, body nodeBody,
) (flow.EventDefinition, error) {
	owner := se.Name.Local + " " + strconv.Quote(id)

	defs, err := buildDefs(asm, owner, body)
	if err != nil {
		return nil, err
	}

	switch len(defs) {
	case 1:
		return defs[0].def, nil

	case 0:
		return nil, errs.New(
			errs.M("bpmn: %s carries no event definition; an intermediate "+
				"event is defined by what it waits for or throws, and BPMN "+
				"has no untyped one", owner),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	return nil, errs.New(
		errs.M("bpmn: %s carries %d event definitions, and this model's "+
			"intermediate events take exactly one; splitting the triggers "+
			"across separate events keeps the meaning the file described",
			owner, len(defs)),
		errs.C(errorClass, errs.InvalidParameter))
}

// startAttrOptions carries the two start-event attributes the model
// models.
//
// Only a NON-default value produces an option: BPMN defaults
// isInterrupting to true and parallelMultiple to false, and so does the
// model, so passing the defaults back would assert as a decision what the
// file never said.
func startAttrOptions(se xml.StartElement) []options.Option {
	var opts []options.Option

	if attrBool(se, "parallelMultiple", false) {
		opts = append(opts, events.WithParallel())
	}

	if !attrBool(se, "isInterrupting", true) {
		opts = append(opts, events.WithNonInterrupting())
	}

	return opts
}

// buildBusinessRuleTask builds a rule task around the decision reference
// the document carries.
//
// The reference is OPAQUE here by design (ADR-024 §2.12): the
// converter parses no DMN and resolves nothing — the host's configured
// rule engine does that. What the converter must not do is import a rule
// task with no decision at all, which would fail at its first execution
// with far less context than this refusal has.
func buildBusinessRuleTask(
	p *parser, _ *assembly, se xml.StartElement, id, name string, body nodeBody,
) (flow.Node, error) {
	ref := decisionReference(se)
	if ref == "" {
		return nil, errs.New(
			errs.M("bpmn: businessRuleTask %q names no decision; BPMN gives the "+
				"element only `implementation`, so the reference comes from "+
				"there or from a recognized dialect's decisionRef — a rule task "+
				"with no decision cannot be run", id),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	opts := append(body.opts(id), p.camundaOptions(se, id)...)

	return activities.NewBusinessRuleTask(fallbackName(id, name), ref, opts...)
}

// decisionReference finds the decision a rule task evaluates.
//
// BPMN gives BusinessRuleTask only `implementation`
// (elements/activities.md:253), so a decision reference is vendor
// vocabulary by construction — the dialect's decisionRef is checked
// first, and `implementation` is accepted as the standard-shaped
// fallback when it names something other than the "unspecified" default.
func decisionReference(se xml.StartElement) string {
	if ref := strings.TrimSpace(
		nsAttrValue(se, nsCamunda, camundaDecisionRef)); ref != "" {
		return ref
	}

	impl := strings.TrimSpace(attrValue(se, observability.AttrImplementation))
	if impl == "" || strings.HasPrefix(impl, "##") {
		// "##unspecified" and its siblings name a mechanism, not a
		// decision.
		return ""
	}

	return impl
}

// buildScriptTask builds a script task, or reports why its script cannot
// be run here. The format decides that (SRD-089.B §FR-5) and is checked
// BEFORE the body, so a file carrying a language this engine has no
// engine for is told about the language rather than about a missing
// script.
func buildScriptTask(
	_ *parser, _ *assembly, se xml.StartElement, id, name string, body nodeBody,
) (flow.Node, error) {
	format := attrValue(se, "scriptFormat")

	if classifyScriptFormat(format) != scriptLua {
		return nil, refuseScriptFormat(id, format)
	}

	if strings.TrimSpace(body.script) == "" {
		return nil, errs.New(
			errs.M("bpmn: scriptTask %q declares scriptFormat %q but carries no "+
				"<script>; there is nothing to run", id, format),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	return activities.NewScriptTask(
		fallbackName(id, name), format, body.script, body.opts(id)...)
}

// buildManualTask backs both <bpmn:task> and <bpmn:manualTask>: the
// abstract task is non-operational (§13.1), which is what ManualTask
// models.
func buildManualTask(
	_ *parser, _ *assembly, _ xml.StartElement, id, name string, body nodeBody,
) (flow.Node, error) {
	return activities.NewManualTask(fallbackName(id, name), body.opts(id)...)
}

// buildGateway backs the exclusive and parallel gateways, which differ
// only in their constructor and in whether a default flow is recorded.
func buildGateway(
	_ *parser, asm *assembly, se xml.StartElement, id, name string, body nodeBody,
) (flow.Node, error) {
	return parseGateway(asm, se, id, name, body)
}

// flowChildParser parses one child of <bpmn:sequenceFlow>.
type flowChildParser func(p *parser, fs *flowSpec, se xml.StartElement) error

// sequenceFlowParsers claims the children of <bpmn:sequenceFlow>.
var sequenceFlowParsers = map[string]flowChildParser{
	tagConditionExpr: parseConditionElem,
	tagDocumentation: parseFlowDocElem,
}

// parseFlowDocElem records one <documentation> child of a sequence flow.
func parseFlowDocElem(p *parser, fs *flowSpec, se xml.StartElement) error {
	d, err := p.parseDoc(se)
	if err != nil {
		return err
	}

	fs.docs = append(fs.docs, d)

	return nil
}

// parseConditionElem records a sequence flow's condition as inert text.
func parseConditionElem(p *parser, fs *flowSpec, se xml.StartElement) error {
	body, err := p.readText(se)
	if err != nil {
		return err
	}

	if body = strings.TrimSpace(body); body != "" {
		fs.condID = attrValue(se, "id")
		fs.condLang = attrValue(se, "language")
		fs.condBody = body
		fs.hasCond = true
	}

	return nil
}

// ifaceChildParser parses one child of <bpmn:interface>.
type ifaceChildParser func(p *parser, interfaceID string, se xml.StartElement) error

// interfaceParsers claims the children of <bpmn:interface>.
var interfaceParsers = map[string]ifaceChildParser{
	tagOperation: parseOperationElem,
}

// parseOperationElem parses one operation under an interface.
func parseOperationElem(p *parser, interfaceID string, se xml.StartElement) error {
	return p.parseOperation(interfaceID, se)
}

// opChildParser parses one child of <bpmn:operation>.
type opChildParser func(p *parser, spec *opSpec, se xml.StartElement) error

// operationParsers claims the children of <bpmn:operation>.
var operationParsers = map[string]opChildParser{
	tagInMessageRef:  parseInMessageElem,
	tagOutMessageRef: parseOutMessageElem,
}

// parseInMessageElem records the operation's input message reference.
func parseInMessageElem(p *parser, spec *opSpec, se xml.StartElement) error {
	body, err := p.readText(se)
	if err != nil {
		return err
	}

	spec.inMsgRef = strings.TrimSpace(body)

	return nil
}

// parseOutMessageElem records the operation's output message reference.
func parseOutMessageElem(p *parser, spec *opSpec, se xml.StartElement) error {
	body, err := p.readText(se)
	if err != nil {
		return err
	}

	spec.outMsgRef = strings.TrimSpace(body)

	return nil
}
