package bpmn

import (
	"encoding/xml"
	"slices"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
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
	// never mean silent acceptance (ADR-024 v.4 §2.9).
	refused dispositionKind = iota

	// skipped swallows the element's subtree without a word. It is only
	// correct where dropping the element leaves the imported definition
	// meaning the same thing.
	skipped
)

// annotations are the BPMN-namespace children carrying no execution
// semantics, skipped in every context that can hold one. They are
// near-universal in modeler output, so refusing them would reject files
// whose flow graph is entirely supported (ADR-024 v.4 §2.6).
var annotations = []string{tagDocumentation, tagExtensionElems}

// policy declares every non-default disposition that is not context-wide.
// Lookup order is: the context's parser table, then this table, then
// refused — so an element appears in exactly one place and the three
// cannot drift apart.
var policy = map[elementKey]dispositionKind{
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
}

// sections pins the BPMN 2.0 § for elements the converter refuses, so the
// UnsupportedElementError carries actionable modeler feedback (ADR-024
// v.4 §2.7 / SAD-001 §5). A tag absent from the table yields an error
// with no §.
var sections = map[string]string{
	"sendTask":                         "§13.3.3",
	"receiveTask":                      "§13.3.3",
	"scriptTask":                       "§13.3.3",
	"businessRuleTask":                 "§13.3.3",
	"callActivity":                     "§13.3.3",
	"subProcess":                       "§13.3.4",
	"adHocSubProcess":                  "§13.3.4",
	"transaction":                      "§13.3.4",
	"inclusiveGateway":                 "§13.4.3",
	"eventBasedGateway":                "§13.4",
	"complexGateway":                   "§13.4",
	"intermediateCatchEvent":           "§13.5",
	"intermediateThrowEvent":           "§13.5",
	"boundaryEvent":                    "§13.5.5",
	"messageEventDefinition":           "§13.5",
	"timerEventDefinition":             "§13.5",
	"signalEventDefinition":            "§13.5",
	"errorEventDefinition":             "§13.5",
	"escalateEventDefinition":          "§13.5",
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
func (p *parser) settle(ctx parseCtx, se xml.StartElement) error {
	if dispositionFor(ctx, se.Name.Local) == skipped {
		return p.skipElement()
	}

	return unsupported(se)
}

// defsParser parses one child of <bpmn:definitions>. It returns a
// non-nil assembly when the child was the document's first <process>.
type defsParser func(p *parser, asm *assembly, se xml.StartElement) (*assembly, error)

// definitionsParsers claims the children of <bpmn:definitions>.
var definitionsParsers = map[string]defsParser{
	tagInterface: parseInterfaceElem,
	tagProcess:   parseProcessElem,
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

// nodeBuilder builds the model node behind one flow-node element. id and
// name are already resolved (name falls back to the id where the model
// demands one).
type nodeBuilder func(
	p *parser, asm *assembly, se xml.StartElement, id, name string,
) (flow.Node, error)

// nodeBuilders maps a flow-node element to its model constructor — the
// heart of the import mapping, kept as a table so a new element is a row
// rather than a case.
var nodeBuilders = map[string]nodeBuilder{
	tagStartEvent: func(_ *parser, _ *assembly, _ xml.StartElement, id, name string) (flow.Node, error) {
		return events.NewStartEvent(name, foundation.WithID(id))
	},

	tagEndEvent: func(_ *parser, _ *assembly, _ xml.StartElement, id, name string) (flow.Node, error) {
		return events.NewEndEvent(name, foundation.WithID(id))
	},

	tagTask:       buildManualTask,
	tagManualTask: buildManualTask,

	tagUserTask: func(_ *parser, _ *assembly, _ xml.StartElement, id, name string) (flow.Node, error) {
		// gobpm's UserTask demands at least one output resource parameter
		// (bpmncommon.NewResource rejects an empty parameter list), while
		// this slice carries no ioSpecification — so import synthesizes one
		// optional placeholder output. It is model plumbing, not BPMN
		// content, and is not written back on export.
		return activities.NewUserTask(name,
			foundation.WithID(id),
			activities.WithoutParams(),
			activities.WithOutput("result", typeBool, false))
	},

	tagServiceTask: func(p *parser, _ *assembly, se xml.StartElement, id, name string) (flow.Node, error) {
		return p.parseServiceTask(se, id, name)
	},

	tagExclusiveGateway: buildGateway,
	tagParallelGateway:  buildGateway,
}

// buildManualTask backs both <bpmn:task> and <bpmn:manualTask>: the
// abstract task is non-operational (§13.1), which is what ManualTask
// models.
func buildManualTask(
	_ *parser, _ *assembly, _ xml.StartElement, id, name string,
) (flow.Node, error) {
	return activities.NewManualTask(name, foundation.WithID(id))
}

// buildGateway backs the exclusive and parallel gateways, which differ
// only in their constructor and in whether a default flow is recorded.
func buildGateway(
	_ *parser, asm *assembly, se xml.StartElement, id, name string,
) (flow.Node, error) {
	return parseGateway(asm, se, id, name)
}

// flowChildParser parses one child of <bpmn:sequenceFlow>.
type flowChildParser func(p *parser, fs *flowSpec, se xml.StartElement) error

// sequenceFlowParsers claims the children of <bpmn:sequenceFlow>.
var sequenceFlowParsers = map[string]flowChildParser{
	tagConditionExpr: parseConditionElem,
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
