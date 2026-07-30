package bpmn

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/convert"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
)

// importer converts BPMN 2.0 XML into a *process.Process over the
// executable-core MVP subset (SRD-051 §FR-5). It is registered in the
// convert seam under convert.BPMN by this package's init.
type importer struct{}

// Import parses BPMN 2.0 XML from r (namespace
// http://www.omg.org/spec/BPMN/20100524/MODEL) and returns the assembled
// process.
//
// The algorithm is the two-pass one of SRD-051 §3.3: a namespace-aware
// token-stream decoder collects nodes and flows, skipping foreign-namespace
// subtrees (diagram interchange etc.) silently and failing on unmapped
// in-BPMN-namespace elements with *convert.UnsupportedElementError
// (SRD-051 §FR-7); nodes are built first (with foundation.WithID — ids are
// never auto-generated, ADR-019), then flows are linked, exclusive-gateway
// defaults re-resolved, and the graph validated before returning.
func (importer) Import(ctx context.Context, r io.Reader) (*process.Process, error) {
	if ctx == nil {
		return nil, errs.New(
			errs.M("bpmn.Import: ctx is nil"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if r == nil {
		return nil, errs.New(
			errs.M("bpmn.Import: r is nil"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	p := &parser{
		dec:        xml.NewDecoder(r),
		ctx:        ctx,
		interfaces: make(map[string]string),
		ops:        make(map[string]opSpec),
	}

	return p.parse()
}

// flowSpec is the pass-1 record of a <bpmn:sequenceFlow>.
type flowSpec struct {
	id, name         string
	srcRef, trgRef   string
	condID, condLang string
	condBody         string // set only when a non-empty conditionExpression was seen
	hasCond          bool
}

// opSpec is a definitions-level <bpmn:operation> collected before process
// wiring so serviceTask operationRef can resolve (SRD-051 §4.6).
type opSpec struct {
	id, name    string
	interfaceID string
	inMsgRef    string // reserved for a later message-binding slice
	outMsgRef   string
}

// assembly is the pass-1 result: everything needed to wire the process.
//
// The slices trail the pointer-and-map fields for govet/fieldalignment: a
// slice header carries its pointer first and two non-pointer words after it.
type assembly struct {
	proc       *process.Process
	byID       map[string]flow.Node
	gwDefaults map[*gateways.ExclusiveGateway]string // gateway → default flow id
	// interfaces is the definitions-level catalog (id → name) for export
	// reconstruction when ServiceTask.Operation() is available.
	interfaces map[string]string
	// ops indexes every operation under those interfaces by operation id.
	ops   map[string]opSpec
	nodes []flow.Node // document order
	flows []flowSpec
}

// parser wraps the xml.Decoder token stream with import state.
type parser struct {
	dec *xml.Decoder
	ctx context.Context
	// definitions-level catalogs accumulate across children of <definitions>
	// before/while the process is parsed.
	interfaces map[string]string
	ops        map[string]opSpec
}

// parse decodes <bpmn:definitions> and its (single) <bpmn:process>.
func (p *parser) parse() (*process.Process, error) {
	root, err := p.rootElement()
	if err != nil {
		return nil, err
	}

	asm, err := p.parseDefinitions(root)
	if err != nil {
		return nil, err
	}

	return build(asm)
}

// rootElement advances the stream to the root start element and checks it is
// <bpmn:definitions> in the BPMN 2.0 model namespace.
func (p *parser) rootElement() (xml.StartElement, error) {
	for {
		tok, err := p.token()
		if err != nil {
			return xml.StartElement{}, err
		}

		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}

		if se.Name.Space != nsBPMN || se.Name.Local != tagDefinitions {
			return xml.StartElement{}, errs.New(
				errs.M("bpmn: root element is {%s}%s, want {%s}%s",
					se.Name.Space, se.Name.Local, nsBPMN, tagDefinitions),
				errs.C(errorClass, errs.InvalidObject))
		}

		return se, nil
	}
}

// parseDefinitions walks the children of <bpmn:definitions> and parses the
// first <bpmn:process>; a second process and any other unmapped in-namespace
// element are reported per SRD-051 §FR-7. Foreign-namespace subtrees and
// non-executable annotations (documentation, extensionElements) are skipped.
func (p *parser) parseDefinitions(root xml.StartElement) (*assembly, error) {
	var asm *assembly

	for {
		tok, err := p.token()
		if err != nil {
			return nil, err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			next, err := p.handleDefinitionsChild(t, asm)
			if err != nil {
				return nil, err
			}

			if next != nil {
				asm = next
			}

		case xml.EndElement:
			if t.Name == root.Name {
				if asm == nil {
					return nil, errs.New(
						errs.M("bpmn: no <process> element found in <definitions>"),
						errs.C(errorClass, errs.ObjectNotFound))
				}

				return asm, nil
			}
		}
	}
}

// handleDefinitionsChild processes one start element under <definitions>.
// Returns a non-nil assembly when the first <process> is parsed.
func (p *parser) handleDefinitionsChild(
	se xml.StartElement,
	asm *assembly,
) (*assembly, error) {
	if se.Name.Space != nsBPMN || isSkippableAnnotation(se.Name.Local) {
		// Foreign-namespace (bpmndi/dc/di, SRD-051 §FR-7 §4.5) or
		// non-executable annotation — skip the whole subtree.
		return nil, p.skipElement()
	}

	switch se.Name.Local {
	case tagInterface:
		// Definitions-level service catalog (SRD-051 §4.6). Collected before
		// process wiring so serviceTask@operationRef resolves.
		return nil, p.parseInterface(se)

	case tagProcess:
		if asm != nil {
			return nil, unsupported(se)
		}

		return p.parseProcess(se)

	default:
		return nil, unsupported(se)
	}
}

// parseProcess parses one <bpmn:process> element into an assembly.
func (p *parser) parseProcess(se xml.StartElement) (*assembly, error) {
	id := strings.TrimSpace(attrValue(se, "id"))
	if id == "" {
		return nil, errs.New(
			errs.M("bpmn: <process> has no id (ids are never auto-generated, ADR-019)"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	// process.New demands a non-empty name; fall back to the id.
	name := attrValue(se, "name")
	if strings.TrimSpace(name) == "" {
		name = id
	}

	proc, err := process.New(name, foundation.WithID(id))
	if err != nil {
		return nil, errs.New(
			errs.M("bpmn: couldn't create process %q", id),
			errs.C(errorClass, errs.BulidingFailed),
			errs.E(err))
	}

	asm := &assembly{
		proc:       proc,
		byID:       make(map[string]flow.Node),
		gwDefaults: make(map[*gateways.ExclusiveGateway]string),
		interfaces: p.interfaces,
		ops:        p.ops,
	}

	for {
		tok, err := p.token()
		if err != nil {
			return nil, err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Space != nsBPMN {
				if err := p.skipElement(); err != nil {
					return nil, err
				}

				continue
			}

			if err := p.parseFlowElement(asm, t); err != nil {
				return nil, err
			}

		case xml.EndElement:
			if t.Name == se.Name {
				return asm, nil
			}
		}
	}
}

// parseFlowElement dispatches one child of <bpmn:process> over the SRD-051
// §FR-8 element set.
func (p *parser) parseFlowElement(asm *assembly, se xml.StartElement) error {
	switch se.Name.Local {
	case tagStartEvent, tagEndEvent, tagTask, tagManualTask, tagUserTask,
		tagServiceTask, tagExclusiveGateway, tagParallelGateway:
		return p.parseNode(asm, se)

	case tagSequenceFlow:
		fs, err := p.parseSequenceFlow(se)
		if err != nil {
			return err
		}

		asm.flows = append(asm.flows, *fs)

		return nil

	case tagDocumentation, tagExtensionElems:
		// non-executable annotations — skipped (see package doc /
		// isSkippableAnnotation)
		return p.skipElement()

	default:
		return unsupported(se)
	}
}

// parseNode parses a single flow node element (event, task or gateway),
// builds the corresponding model node with its BPMN id and records it in the
// assembly.
func (p *parser) parseNode(asm *assembly, se xml.StartElement) error {
	id, err := requiredID(se)
	if err != nil {
		return err
	}

	if _, dup := asm.byID[id]; dup {
		return errs.New(
			errs.M("bpmn: duplicate flow-element id %q on <%s>", id, se.Name.Local),
			errs.C(errorClass, errs.DuplicateObject))
	}

	name := attrValue(se, "name")

	var node flow.Node

	switch se.Name.Local {
	case tagStartEvent:
		node, err = events.NewStartEvent(name, foundation.WithID(id))

	case tagEndEvent:
		node, err = events.NewEndEvent(name, foundation.WithID(id))

	case tagTask, tagManualTask:
		node, err = activities.NewManualTask(name, foundation.WithID(id))

	case tagUserTask:
		// gobpm's UserTask demands at least one output resource parameter
		// (bpmncommon.NewResource rejects an empty parameter list), while the
		// MVP subset carries no ioSpecification — so import synthesizes one
		// optional placeholder output; it is model plumbing, not BPMN
		// content, and is not written back on export (SRD-051 §FR-8).
		node, err = activities.NewUserTask(name,
			foundation.WithID(id),
			activities.WithoutParams(),
			activities.WithOutput("result", "bool", false))

	case tagServiceTask:
		node, err = p.parseServiceTask(asm, se, id, name)

	case tagExclusiveGateway, tagParallelGateway:
		node, err = p.parseGateway(asm, se, id, name)
	}

	if err != nil {
		// Do not re-wrap already-classified converter errors (unknown
		// operationRef, invalid gatewayDirection, …) — preserve class for
		// errors.As / HasClass (k8s/docker-style root-cause visibility).
		return wrapErr(
			fmt.Sprintf("bpmn: couldn't create %s %q", se.Name.Local, id),
			errs.BulidingFailed,
			err)
	}

	if node == nil {
		return errs.New(
			errs.M("bpmn: no constructor mapping for %q", se.Name.Local),
			errs.C(errorClass, errs.InvalidObject))
	}

	// node bodies: wiring duplicates (incoming/outgoing) and non-executable
	// annotations are skipped; anything else in the BPMN namespace — event
	// definitions, io specifications, loop characteristics — is not in the
	// subset (SRD-051 §FR-7).
	if err := p.consumeNodeBody(se); err != nil {
		return err
	}

	asm.nodes = append(asm.nodes, node)
	asm.byID[id] = node

	return nil
}

// parseInterface parses a definitions-level <bpmn:interface> and records its
// operations in the parser catalogs (SRD-051 §4.6). Message refs are stored
// for a later slice; this slice only needs id/name for NewOperation.
func (p *parser) parseInterface(se xml.StartElement) error {
	id, err := requiredID(se)
	if err != nil {
		return err
	}

	name := attrValue(se, "name")
	if strings.TrimSpace(name) == "" {
		name = id
	}

	p.interfaces[id] = name

	for {
		tok, err := p.token()
		if err != nil {
			return err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			if err := p.parseInterfaceChild(id, t); err != nil {
				return err
			}

		case xml.EndElement:
			if t.Name == se.Name {
				return nil
			}
		}
	}
}

// parseInterfaceChild handles one child of <bpmn:interface>.
func (p *parser) parseInterfaceChild(interfaceID string, se xml.StartElement) error {
	if se.Name.Space != nsBPMN {
		return p.skipElement()
	}

	switch se.Name.Local {
	case tagOperation:
		return p.parseOperation(interfaceID, se)
	default:
		if isSkippableAnnotation(se.Name.Local) {
			return p.skipElement()
		}

		return unsupported(se)
	}
}

// parseOperation parses one <bpmn:operation> under an interface.
func (p *parser) parseOperation(interfaceID string, se xml.StartElement) error {
	id, err := requiredID(se)
	if err != nil {
		return err
	}

	if _, dup := p.ops[id]; dup {
		return errs.New(
			errs.M("bpmn: duplicate operation id %q", id),
			errs.C(errorClass, errs.DuplicateObject))
	}

	name := attrValue(se, "name")
	if strings.TrimSpace(name) == "" {
		name = id
	}

	spec := opSpec{id: id, name: name, interfaceID: interfaceID}

	for {
		tok, err := p.token()
		if err != nil {
			return err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			if err := p.parseOperationChild(&spec, t); err != nil {
				return err
			}

		case xml.EndElement:
			if t.Name == se.Name {
				p.ops[id] = spec

				return nil
			}
		}
	}
}

// parseOperationChild handles one child of <bpmn:operation>. Unknown
// in-namespace children (errorRef, …) are skipped as catalog detail rather
// than aborting the import — only id/name matter for this slice.
func (p *parser) parseOperationChild(spec *opSpec, se xml.StartElement) error {
	if se.Name.Space != nsBPMN {
		return p.skipElement()
	}

	switch se.Name.Local {
	case tagInMessageRef:
		body, err := p.readText(se)
		if err != nil {
			return err
		}

		spec.inMsgRef = strings.TrimSpace(body)

		return nil

	case tagOutMessageRef:
		body, err := p.readText(se)
		if err != nil {
			return err
		}

		spec.outMsgRef = strings.TrimSpace(body)

		return nil

	default:
		// documentation / extensionElements / errorRef / …
		return p.skipElement()
	}
}

// parseServiceTask builds a ServiceTask bound to a definitions-level
// operation (or a synthetic operation when operationRef is absent).
// The operation has no Implementor — the converter is not an execution
// engine; the host supplies a real implementor (or gooper) after import
// (SRD-051 §4.6).
func (p *parser) parseServiceTask(
	_ *assembly,
	se xml.StartElement,
	id, name string,
) (flow.Node, error) {
	if strings.TrimSpace(name) == "" {
		name = id
	}

	op, err := p.resolveOperation(se, id, name)
	if err != nil {
		return nil, err
	}

	return activities.NewServiceTask(name, op,
		foundation.WithID(id),
		activities.WithoutParams())
}

// resolveOperation looks up operationRef in the definitions catalog, or mints
// a synthetic operation keyed by the serviceTask id when the attribute is
// missing (some modelers emit serviceTask without a catalog).
func (p *parser) resolveOperation(
	se xml.StartElement,
	taskID, taskName string,
) (service.Operation, error) {
	opRef := strings.TrimSpace(attrValue(se, "operationRef"))

	if opRef == "" {
		// Synthetic catalog entry so the model still carries a named
		// Operation with a stable id (taskID:operation).
		return service.NewOperation(taskName, nil, nil, nil,
			foundation.WithID(taskID+":operation"))
	}

	spec, ok := p.ops[opRef]
	if !ok {
		return nil, errs.New(
			errs.M("bpmn: serviceTask %q: unknown operationRef %q", taskID, opRef),
			errs.C(errorClass, errs.ObjectNotFound))
	}

	return service.NewOperation(spec.name, nil, nil, nil,
		foundation.WithID(spec.id))
}

// parseGateway builds an exclusive or parallel gateway node, applying the
// name, gatewayDirection and (exclusive only) recording the default flow id
// for pass 2.
func (*parser) parseGateway(
	asm *assembly,
	se xml.StartElement,
	id, name string,
) (flow.Node, error) {
	opts := []options.Option{foundation.WithID(id)}

	if name != "" {
		opts = append(opts, options.WithName(name))
	}

	if dir := attrValue(se, "gatewayDirection"); dir != "" {
		gd := gateways.GDirection(dir)

		if err := gd.Validate(); err != nil {
			return nil, errs.New(
				errs.M("bpmn: gateway %q has invalid gatewayDirection %q", id, dir),
				errs.C(errorClass, errs.InvalidParameter),
				errs.E(err))
		}

		opts = append(opts, gateways.WithDirection(gd))
	}

	if se.Name.Local == tagParallelGateway {
		return gateways.NewParallelGateway(opts...)
	}

	gw, err := gateways.NewExclusiveGateway(opts...)
	if err != nil {
		return nil, err
	}

	if def := attrValue(se, "default"); def != "" {
		asm.gwDefaults[gw] = def
	}

	return gw, nil
}

// parseSequenceFlow parses a <bpmn:sequenceFlow> into a flowSpec for pass 2.
func (p *parser) parseSequenceFlow(se xml.StartElement) (*flowSpec, error) {
	id, err := requiredID(se)
	if err != nil {
		return nil, err
	}

	fs := &flowSpec{
		id:     id,
		name:   attrValue(se, "name"),
		srcRef: attrValue(se, "sourceRef"),
		trgRef: attrValue(se, "targetRef"),
	}

	if fs.srcRef == "" || fs.trgRef == "" {
		return nil, errs.New(
			errs.M("bpmn: sequenceFlow %q needs both sourceRef and targetRef (got %q/%q)",
				fs.id, fs.srcRef, fs.trgRef),
			errs.C(errorClass, errs.InvalidParameter))
	}

	for {
		tok, err := p.token()
		if err != nil {
			return nil, err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			if err := p.parseSequenceFlowChild(fs, t); err != nil {
				return nil, err
			}

		case xml.EndElement:
			if t.Name == se.Name {
				return fs, nil
			}
		}
	}
}

// parseSequenceFlowChild handles one child of <bpmn:sequenceFlow>: a
// conditionExpression (kept as inert text), documentation (skipped), or
// foreign-namespace content (skipped). Anything else in the BPMN namespace
// is unsupported.
func (p *parser) parseSequenceFlowChild(fs *flowSpec, se xml.StartElement) error {
	if se.Name.Space != nsBPMN {
		return p.skipElement()
	}

	switch se.Name.Local {
	case tagConditionExpr:
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

	default:
		if isSkippableAnnotation(se.Name.Local) {
			return p.skipElement()
		}

		return unsupported(se)
	}
}

// consumeNodeBody swallows a node's children: incoming/outgoing (redundant
// with sequenceFlow wiring) and non-executable annotations are skipped; any
// other in-namespace element is an UnsupportedElementError (SRD-051 §FR-7).
func (p *parser) consumeNodeBody(se xml.StartElement) error {
	for {
		tok, err := p.token()
		if err != nil {
			return err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			if err := p.consumeNodeChild(t); err != nil {
				return err
			}

		case xml.EndElement:
			if t.Name == se.Name {
				return nil
			}
		}
	}
}

// consumeNodeChild handles one child start tag of a flow node.
func (p *parser) consumeNodeChild(se xml.StartElement) error {
	if se.Name.Space != nsBPMN {
		return p.skipElement()
	}

	switch se.Name.Local {
	case tagIncoming, tagOutgoing:
		return p.skipElement()
	default:
		if isSkippableAnnotation(se.Name.Local) {
			return p.skipElement()
		}

		return unsupported(se)
	}
}

// build is pass 2 of SRD-051 §3.3: nodes are added to the process, flows are
// linked through the complete id→node table, exclusive-gateway defaults are
// re-resolved by flow id, and the graph is validated.
func build(asm *assembly) (*process.Process, error) {
	for _, n := range asm.nodes {
		if err := asm.proc.Add(n); err != nil {
			return nil, errs.New(
				errs.M("bpmn: couldn't add node %q to process %q", n.ID(), asm.proc.ID()),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(err))
		}
	}

	flowByID := make(map[string]*flow.SequenceFlow, len(asm.flows))

	for _, fs := range asm.flows {
		sf, err := linkFlow(asm, fs)
		if err != nil {
			return nil, err
		}

		flowByID[fs.id] = sf
	}

	if err := applyGatewayDefaults(asm, flowByID); err != nil {
		return nil, err
	}

	if err := asm.proc.Validate(); err != nil {
		return nil, errs.New(
			errs.M("bpmn: process %q is invalid", asm.proc.ID()),
			errs.C(errorClass, errs.InvalidObject),
			errs.E(err))
	}

	return asm.proc, nil
}

// linkFlow resolves one flowSpec against the assembly's id→node table and
// calls flow.Link (with optional condition).
func linkFlow(asm *assembly, fs flowSpec) (*flow.SequenceFlow, error) {
	src, ok := asm.byID[fs.srcRef]
	if !ok {
		return nil, errs.New(
			errs.M("bpmn: sequenceFlow %q: unknown sourceRef %q", fs.id, fs.srcRef),
			errs.C(errorClass, errs.ObjectNotFound))
	}

	trg, ok := asm.byID[fs.trgRef]
	if !ok {
		return nil, errs.New(
			errs.M("bpmn: sequenceFlow %q: unknown targetRef %q", fs.id, fs.trgRef),
			errs.C(errorClass, errs.ObjectNotFound))
	}

	opts := []options.Option{foundation.WithID(fs.id)}

	if fs.name != "" {
		opts = append(opts, options.WithName(fs.name))
	}

	if fs.hasCond {
		condID := fs.condID
		if condID == "" {
			condID = fs.id + ":condition"
		}

		opts = append(opts, flow.WithCondition(
			newFormalExpression(condID, fs.condLang, fs.condBody)))
	}

	ss, ok := src.(flow.SequenceSource)
	if !ok {
		return nil, errs.New(
			errs.M("bpmn: sequenceFlow %q: source %q is not a sequence source",
				fs.id, fs.srcRef),
			errs.C(errorClass, errs.TypeCastingError))
	}

	st, ok := trg.(flow.SequenceTarget)
	if !ok {
		return nil, errs.New(
			errs.M("bpmn: sequenceFlow %q: target %q is not a sequence target",
				fs.id, fs.trgRef),
			errs.C(errorClass, errs.TypeCastingError))
	}

	sf, err := flow.Link(ss, st, opts...)
	if err != nil {
		return nil, errs.New(
			errs.M("bpmn: couldn't link sequenceFlow %q (%s → %s)",
				fs.id, fs.srcRef, fs.trgRef),
			errs.C(errorClass, errs.BulidingFailed),
			errs.E(err))
	}

	return sf, nil
}

// applyGatewayDefaults re-resolves each exclusive gateway's default attribute
// to a linked SequenceFlow by id (pass 2 of SRD-051 §3.3).
func applyGatewayDefaults(
	asm *assembly,
	flowByID map[string]*flow.SequenceFlow,
) error {
	for gw, flowID := range asm.gwDefaults {
		df, ok := flowByID[flowID]
		if !ok {
			return errs.New(
				errs.M("bpmn: exclusiveGateway %q: unknown default flow %q", gw.ID(), flowID),
				errs.C(errorClass, errs.ObjectNotFound))
		}

		if err := gw.UpdateDefaultFlow(df); err != nil {
			return errs.New(
				errs.M("bpmn: exclusiveGateway %q: couldn't set default flow %q", gw.ID(), flowID),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(err))
		}
	}

	return nil
}

// token reads the next token, honoring ctx cancellation and converting
// stream errors into classified import errors.
//
// context.Canceled / DeadlineExceeded are returned as-is (errors.Is), not
// reclassified — same convention as k8s API machinery and the Docker CLI.
func (p *parser) token() (xml.Token, error) {
	if err := p.ctx.Err(); err != nil {
		return nil, err
	}

	tok, err := p.dec.Token()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errs.New(
				errs.M("bpmn: unexpected end of XML stream"),
				errs.C(errorClass, errs.InvalidObject))
		}

		// Preserve context errors that the decoder may surface.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}

		return nil, errs.New(
			errs.M("bpmn: XML syntax error"),
			errs.C(errorClass, errs.InvalidObject),
			errs.E(err))
	}

	return tok, nil
}

// skipElement swallows the remainder of the element whose start tag was just
// consumed, including all nested content.
//
// It deliberately does not use (*xml.Decoder).Skip: reading through p.token
// keeps per-token ctx cancellation and classified stream errors even inside
// skipped subtrees.
func (p *parser) skipElement() error {
	depth := 1

	for depth > 0 {
		tok, err := p.token()
		if err != nil {
			return err
		}

		switch tok.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		}
	}

	return nil
}

// readText reads the character data of a simple text element (used for
// <bpmn:conditionExpression>).
func (p *parser) readText(se xml.StartElement) (string, error) {
	var sb strings.Builder

	for {
		tok, err := p.token()
		if err != nil {
			return "", err
		}

		switch t := tok.(type) {
		case xml.CharData:
			sb.Write(t)

		case xml.StartElement:
			if t.Name.Space != nsBPMN {
				if err := p.skipElement(); err != nil {
					return "", err
				}

				continue
			}

			return "", unsupported(t)

		case xml.EndElement:
			if t.Name == se.Name {
				return sb.String(), nil
			}
		}
	}
}

// unsupported builds the *convert.UnsupportedElementError for an unmapped
// in-namespace element (SRD-051 §FR-3/§FR-7).
func unsupported(se xml.StartElement) error {
	return &convert.UnsupportedElementError{
		Tag:     se.Name.Local,
		ID:      attrValue(se, "id"),
		Section: sectionFor(se.Name.Local),
	}
}

// sectionFor pins BPMN 2.0 spec sections for known unmapped elements so
// UnsupportedElementError carries actionable modeler feedback (SRD-051
// §FR-3 / SAD-001 §5). Empty when the tag is not in the pin table.
func sectionFor(tag string) string {
	switch tag {
	case "sendTask", "receiveTask", "scriptTask", "businessRuleTask", "callActivity":
		return "§13.3.3"
	case "subProcess", "adHocSubProcess", "transaction":
		return "§13.3.4"
	case "inclusiveGateway":
		return "§13.4.3"
	case "eventBasedGateway", "complexGateway":
		return "§13.4"
	case "intermediateCatchEvent", "intermediateThrowEvent":
		return "§13.5"
	case "boundaryEvent":
		return "§13.5.5"
	case "messageEventDefinition", "timerEventDefinition",
		"signalEventDefinition", "errorEventDefinition",
		"escalateEventDefinition", "compensateEventDefinition",
		"conditionalEventDefinition", "linkEventDefinition",
		"terminateEventDefinition", "cancelEventDefinition":
		return "§13.5"
	case "laneSet", "lane":
		return "§10.5"
	case "dataObject", "dataObjectReference", "dataStoreReference":
		return "§10.3"
	case "collaboration", "participant", "messageFlow":
		return "§10.1"
	case "ioSpecification", "property", "dataInput", "dataOutput",
		"dataInputAssociation", "dataOutputAssociation":
		return "§10.3"
	case "multiInstanceLoopCharacteristics", "standardLoopCharacteristics":
		return "§13.3.5"
	default:
		return ""
	}
}

// requiredID extracts the mandatory id attribute of a flow element
// (SRD-051 §FR-5: a missing/blank id on a flow element is an import error).
func requiredID(se xml.StartElement) (string, error) {
	id := strings.TrimSpace(attrValue(se, "id"))
	if id == "" {
		return "", errs.New(
			errs.M("bpmn: <%s> has no id (ids are never auto-generated, ADR-019)",
				se.Name.Local),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	return id, nil
}

// attrValue returns the value of the first unprefixed attribute with the
// given local name.
func attrValue(se xml.StartElement, local string) string {
	for _, a := range se.Attr {
		if a.Name.Space == "" && a.Name.Local == local {
			return a.Value
		}
	}

	return ""
}
