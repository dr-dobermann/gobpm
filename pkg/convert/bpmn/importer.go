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
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/observability"
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
func (imp importer) Import(ctx context.Context, r io.Reader) (*process.Process, error) {
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

	proc, _, err := imp.parse(ctx, r)

	return proc, err
}

// parse is the one parse both entry points share. It returns the report
// alongside the process; Import discards it, ImportDocument does not.
func (importer) parse(
	ctx context.Context, r io.Reader,
) (*process.Process, []convert.Dropped, error) {
	p := newParser(ctx, r)

	proc, err := p.parse()
	if err != nil {
		return nil, nil, err
	}

	return proc, p.dropped, nil
}

// docSpec is one <bpmn:documentation>: its text and the mime type of that
// text (BPMN elements/foundation.md:277-278 — textFormat is 0..1 with a
// text/plain default).
type docSpec struct {
	text   string
	format string
}

// defaultDocFormat is the textFormat the standard assumes when the
// attribute is absent.
const defaultDocFormat = "text/plain"

// docOptions turns collected documentation into construction options —
// the only way a gobpm element can receive it, since foundation.BaseElement
// exposes Docs() and no setter.
func docOptions(docs []docSpec) []options.Option {
	opts := make([]options.Option, 0, len(docs))
	for _, d := range docs {
		opts = append(opts, foundation.WithDoc(d.text, d.format))
	}

	return opts
}

// nodeBody is what a flow node's children contributed, collected BEFORE
// the node is built. This slice only carries documentation; the later
// element stages fill it with the children that decide which node to build
// at all — event definitions, loop characteristics, io specifications.
type nodeBody struct {
	// script is <bpmn:script> — the first child that decides WHAT is
	// built rather than decorating it. A script task cannot be
	// constructed without one.
	script string
	docs   []docSpec
	// defs are the <*EventDefinition> children, which decide what KIND of
	// event is built. They are specs rather than definitions because what
	// they refer to may be declared later in the document (§4.7).
	defs []defSpec
}

// opts renders the body as construction options, with the element's id
// always first.
func (b nodeBody) opts(id string) []options.Option {
	return append([]options.Option{foundation.WithID(id)}, docOptions(b.docs)...)
}

// ImportDocument parses r and returns the process together with every
// recognized construct the import deliberately did not map.
//
// It is the same parse as Import — the report is a by-product the parser
// collects either way — so a host pays nothing for asking, and a host
// that does not ask is not silently misled: Import's contract never
// promised a report, while this one does.
func (imp importer) ImportDocument(
	ctx context.Context, r io.Reader,
) (*convert.Result, error) {
	p, dropped, err := imp.parse(ctx, r)
	if err != nil {
		return nil, err
	}

	return &convert.Result{
		Processes: []*process.Process{p},
		Dropped:   dropped,
	}, nil
}

// flowSpec is the pass-1 record of a <bpmn:sequenceFlow>.
type flowSpec struct {
	id, name         string
	srcRef, trgRef   string
	condID, condLang string
	condBody         string // set only when a non-empty conditionExpression was seen
	docs             []docSpec
	hasCond          bool
}

// nodeSpec is a flow node as pass 1 read it: its element, its id and
// name, and what its children contributed — everything its constructor
// will need.
//
// The node is not built yet, and the reason is the document's own
// freedom of order. Definitions.rootElements is an unordered 0..*
// collection and Process is itself a RootElement
// (elements/foundation.md:23), so the <message> a message start event
// refers to may be declared AFTER the <process> containing it. A
// constructor that takes that message positionally cannot run until the
// whole document has been read, and building only SOME nodes late would
// mean two construction paths and two places for an element to be
// wired differently.
type nodeSpec struct {
	se       xml.StartElement
	id, name string
	body     nodeBody
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
	proc *process.Process
	byID map[string]flow.Node
	// exprLanguage is the document's expressionLanguage, carried here so
	// pass 2 can resolve a condition's language without the parser.
	exprLanguage string
	// interfaces is the definitions-level catalog (id → name) for export
	// reconstruction when ServiceTask.Operation() is available.
	interfaces map[string]string
	// ops indexes every operation under those interfaces by operation id.
	ops map[string]opSpec
	// cat is the parser's catalog, shared by pointer rather than copied:
	// <definitions> may declare a <message> AFTER the <process> that
	// refers to it — rootElements is an unordered 0..* collection
	// (elements/foundation.md:23) — so entries keep arriving while the
	// assembly exists.
	cat *catalog
	// declared is the pass-1 id table: which element claimed each id.
	// byID cannot serve, because the nodes it maps to do not exist until
	// pass 2 (see nodeSpec).
	declared map[string]string
	specs    []nodeSpec // document order
	flows    []flowSpec
	// refs are the forward references pass 1 could not resolve because
	// their target had not been parsed yet (SRD-089.A §FR-2).
	refs []pendingRef
}

// parser wraps the xml.Decoder token stream with import state.
type parser struct {
	dec        *xml.Decoder
	ctx        context.Context
	newProcess func(string, ...options.Option) (*process.Process, error)
	// definitions-level catalogs accumulate across children of <definitions>
	// before/while the process is parsed.
	interfaces map[string]string
	ops        map[string]opSpec
	// cat holds the message/signal/error/escalation objects an event
	// definition refers to (SRD-089.D §FR-1).
	cat *catalog
	// exprLanguage is <definitions expressionLanguage>, the default an
	// expression that declares none inherits (ADR-024 §2.10).
	exprLanguage string
	// owner is the id of the element currently being parsed, so a report
	// about its <extensionElements> — which carries no id of its own —
	// names the element a reader can find in the file.
	owner string
	// claimed collects the dialect attributes that the builder of the node
	// currently under construction mapped onto model options. buildNode
	// reports whatever nobody claimed, so a node kind whose builder never
	// consults the dialect at all still reports what it cannot hold.
	claimed map[string]bool
	// dropped collects the recognized constructs the import did not map,
	// so ImportDocument can hand them to the host instead of losing them
	// (ADR-024 §2.14 rule 2).
	dropped []convert.Dropped
}

// newParser wires a parser over r with its catalogs empty and its
// process constructor bound.
//
// It is the one place the parser's state is initialized: a map left nil
// here would only fail on the file that first writes to it, which is a
// class of bug the import stages keep being in a position to introduce.
func newParser(ctx context.Context, r io.Reader) *parser {
	return &parser{
		dec:        xml.NewDecoder(r),
		ctx:        ctx,
		newProcess: process.New,
		interfaces: make(map[string]string),
		ops:        make(map[string]opSpec),
		cat:        newCatalog(),
	}
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

	return build(p, asm)
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

		p.exprLanguage = strings.TrimSpace(attrValue(se, "expressionLanguage"))

		// <definitions> carries dialect attributes too (a modeler's own
		// bookkeeping); they are reported against the document.
		p.reportUnmappedAttrs(se, attrValue(se, "id"), nil)

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
	if se.Name.Space != nsBPMN {
		// Foreign namespace — diagram interchange, a vendor dialect — is
		// out of execution scope and skipped whole (ADR-024 §2.7).
		return nil, p.skipElement()
	}

	if parse, ok := definitionsParsers[se.Name.Local]; ok {
		return parse(p, asm, se)
	}

	return nil, p.settle(ctxDefinitions, se)
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
	name := fallbackName(id, attrValue(se, "name"))

	// The process is built LAZILY — see procBuild.
	p.owner = id

	// The process element carries dialect attributes of its own —
	// versionTag, historyTimeToLive, the starter authorizations — and
	// nothing else scans them, so they would vanish exactly as the
	// coverage audit found them vanishing.
	p.reportUnmappedAttrs(se, id, nil)

	pb := &procBuild{p: p, id: id, name: name}

	for {
		tok, err := p.token()
		if err != nil {
			return nil, err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			if err := pb.child(t); err != nil {
				return nil, err
			}

		case xml.EndElement:
			if t.Name == se.Name {
				return pb.finish()
			}
		}
	}
}

// procBuild defers building the process until its first flow element.
//
// A process's own documentation is a child, and documentation can only
// reach an element through a construction option — but a process's other
// children are every flow element in the file, so buffering them all to
// construct late is not an option. Buffering the LEADING documentation is:
// documentation is inherited from BaseElement, whose properties serialize
// ahead of a Process's own flowElements, so a schema-valid file always
// presents it first.
type procBuild struct {
	p        *parser
	asm      *assembly
	id, name string
	docs     []docSpec
}

// child handles one child element of <bpmn:process>, constructing the
// process on the first one that is not documentation.
func (pb *procBuild) child(se xml.StartElement) error {
	if se.Name.Space != nsBPMN {
		return pb.p.skipElement()
	}

	if se.Name.Local == tagDocumentation {
		return pb.doc(se)
	}

	if pb.asm == nil {
		if err := pb.build(); err != nil {
			return err
		}
	}

	return pb.p.parseFlowElement(pb.asm, se)
}

// doc records a leading <documentation>, or refuses one that arrives after
// the process has already been built — dropping it silently is the failure
// this converter's feedback contract exists to prevent.
func (pb *procBuild) doc(se xml.StartElement) error {
	if pb.asm != nil {
		return errs.New(
			errs.M("bpmn: <process> %q carries <documentation> after its flow elements; "+
				"BaseElement documentation precedes a container's own content", pb.id),
			errs.C(errorClass, errs.InvalidObject))
	}

	d, err := pb.p.parseDoc(se)
	if err != nil {
		return err
	}

	pb.docs = append(pb.docs, d)

	return nil
}

// build constructs the process and the pass-1 state around it.
func (pb *procBuild) build() error {
	asm, err := pb.p.newAssembly(pb.id, pb.name, pb.docs)
	if err != nil {
		return err
	}

	pb.asm = asm

	return nil
}

// finish returns the assembly, building an empty process when the element
// carried no flow elements at all.
func (pb *procBuild) finish() (*assembly, error) {
	if pb.asm == nil {
		if err := pb.build(); err != nil {
			return nil, err
		}
	}

	return pb.asm, nil
}

// newAssembly builds the process and the pass-1 state around it.
func (p *parser) newAssembly(id, name string, docs []docSpec) (*assembly, error) {
	proc, err := p.newProcess(name,
		append([]options.Option{foundation.WithID(id)}, docOptions(docs)...)...)
	if err != nil {
		return nil, errs.New(
			errs.M("bpmn: couldn't create process %q", id),
			errs.C(errorClass, errs.BulidingFailed),
			errs.E(err))
	}

	return &assembly{
		proc:         proc,
		byID:         make(map[string]flow.Node),
		declared:     make(map[string]string),
		interfaces:   p.interfaces,
		ops:          p.ops,
		cat:          p.cat,
		exprLanguage: p.exprLanguage,
	}, nil
}

// parseFlowElement dispatches one child of <bpmn:process> through the
// process parser table (SRD-089.A §FR-1).
func (p *parser) parseFlowElement(asm *assembly, se xml.StartElement) error {
	if parse, ok := processParsers[se.Name.Local]; ok {
		return parse(p, asm, se)
	}

	return p.settle(ctxProcess, se)
}

// parseNode reads a single flow node element (event, task or gateway)
// and records what its constructor will need. The node itself is built in
// pass 2 — see nodeSpec.
func (p *parser) parseNode(asm *assembly, se xml.StartElement) error {
	id, err := requiredID(se)
	if err != nil {
		return err
	}

	if kind, dup := asm.declared[id]; dup {
		return errs.New(
			errs.M("bpmn: duplicate flow-element id %q on <%s>; <%s> already "+
				"declared it", id, se.Name.Local, kind),
			errs.C(errorClass, errs.DuplicateObject))
	}

	if _, ok := nodeBuilders[se.Name.Local]; !ok {
		// Unreachable through the process table, which only routes names
		// this table also carries — a guard against the two drifting.
		return errs.New(
			errs.M("bpmn: no constructor mapping for %q", se.Name.Local),
			errs.C(errorClass, errs.InvalidObject))
	}

	// The body is read BEFORE the node is built: documentation can only
	// reach an element through a construction option, and the children
	// decide which node to construct at all.
	outer := p.owner
	p.owner = id

	body, err := p.parseNodeBody(se)

	p.owner = outer

	if err != nil {
		return err
	}

	asm.specs = append(asm.specs, nodeSpec{
		se:   se,
		id:   id,
		name: attrValue(se, "name"),
		body: body,
	})
	asm.declared[id] = se.Name.Local

	return nil
}

// buildNodes is the first half of pass 2: every flow node the document
// declared, constructed and added to the process in document order.
//
// It builds in two sweeps. A compensation event definition names the
// activity it compensates, and BPMN does not order a process's flow
// elements any more than it orders its root ones — so that activity may
// be declared after the event naming it. The nodes that name another node
// are therefore built last, once the rest are in the index.
//
// Two sweeps are enough, and a third could never fire: an activityRef
// points at an Activity, and no Activity carries an event definition, so
// nothing built in the second sweep is named by anything built there.
func buildNodes(p *parser, asm *assembly) error {
	built := make([]flow.Node, len(asm.specs))

	for _, deferred := range []bool{false, true} {
		// Indexed rather than ranged by value: a nodeSpec carries an
		// element, a body and two names, and copying it per node buys
		// nothing.
		for i := range asm.specs {
			s := &asm.specs[i]
			if s.namesANode() != deferred {
				continue
			}

			node, err := buildNode(p, asm, s)
			if err != nil {
				return err
			}

			built[i] = node
			asm.byID[s.id] = node
		}
	}

	// Added in DOCUMENT order regardless of which sweep built them: the
	// order a process replays its nodes in is the file's, not the
	// converter's scheduling.
	for _, node := range built {
		if err := asm.proc.Add(node); err != nil {
			return errs.New(
				errs.M("bpmn: couldn't add node %q to process %q",
					node.ID(), asm.proc.ID()),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(err))
		}
	}

	return nil
}

// namesANode reports whether building this node needs another node to
// exist first: a boundary event is attached to its host activity, and a
// compensation definition names the activity it compensates.
func (s nodeSpec) namesANode() bool {
	if s.se.Name.Local == tagBoundaryEvent {
		return true
	}

	for _, d := range s.body.defs {
		if d.local == tagCompensateEventDef && d.ref != "" {
			return true
		}
	}

	return false
}

// buildNode constructs one flow node from its spec.
//
// The owner is set around the construction so a report raised while
// building — a dialect attribute the model cannot hold, say — names the
// element that carried it rather than whatever was parsed last.
//
// The dialect report is raised HERE rather than inside the builders,
// because only three of them consult the dialect at all: a Camunda
// attribute on a <task>, a <manualTask>, a <scriptTask>, a gateway or an
// event was neither mapped nor reported, which is the silent loss the
// report contract exists to prevent. Reporting at the one funnel every
// node passes through means a node kind added later cannot forget it.
func buildNode(p *parser, asm *assembly, s *nodeSpec) (flow.Node, error) {
	// Presence was checked in pass 1, when refusing still had the
	// element's position in the file to point at.
	build := nodeBuilders[s.se.Name.Local]

	outer := p.owner
	p.owner = s.id
	p.claimed = map[string]bool{}

	node, err := build(p, asm, s.se, s.id, s.name, s.body)

	if err == nil {
		// Only for a node that exists: a failed build aborts the import,
		// and a report about an element the host never receives is noise.
		p.reportUnmappedAttrs(s.se, s.id, p.claimed)
	}

	p.claimed = nil
	p.owner = outer

	if err != nil {
		// Do not re-wrap already-classified converter errors (unknown
		// operationRef, invalid gatewayDirection, …) — preserve class for
		// errors.As / HasClass (k8s/docker-style root-cause visibility).
		return nil, wrapErr(
			fmt.Sprintf("bpmn: couldn't create %s %q", s.se.Name.Local, s.id),
			errs.BulidingFailed,
			err)
	}

	return node, nil
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

	if parse, ok := interfaceParsers[se.Name.Local]; ok {
		return parse(p, interfaceID, se)
	}

	return p.settle(ctxInterface, se)
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

// parseOperationChild handles one child of <bpmn:operation>. The
// standard gives the element exactly three children — inMessageRef,
// outMessageRef and errorRef — so every one of them is declared: the
// first two are parsed, errorRef is a skip this slice does not bind, and
// anything else is refused rather than swallowed by a lenient default.
func (p *parser) parseOperationChild(spec *opSpec, se xml.StartElement) error {
	if se.Name.Space != nsBPMN {
		return p.skipElement()
	}

	if parse, ok := operationParsers[se.Name.Local]; ok {
		return parse(p, spec, se)
	}

	return p.settle(ctxOperation, se)
}

// parseServiceTask builds a ServiceTask bound to a definitions-level
// operation (or a synthetic operation when operationRef is absent).
// The operation has no Implementor — the converter is not an execution
// engine; the host supplies a real implementor (or gooper) after import
// (SRD-051 §4.6).
func (p *parser) parseServiceTask(
	se xml.StartElement,
	id, name string,
	body nodeBody,
) (flow.Node, error) {
	name = fallbackName(id, name)

	op, err := p.resolveOperation(se, id, name)
	if err != nil {
		return nil, err
	}

	opts := append(body.opts(id), activities.WithoutParams())
	opts = append(opts, p.camundaOptions(se, id)...)

	// BPMN carries `implementation` on the serviceTask itself. Without the
	// carrier the attribute had nowhere to land, so export wrote a value
	// import could never read back.
	//
	// The name comes from the observability vocabulary because the two
	// collide by spelling and the repo enforces the constant
	// (internal/lintcfg TestNoLiteralAttrKeys). They are NOT the same thing
	// — BPMN fixes this attribute name, a log key can be renamed — so
	// TestImplementationAttrNameMatchesTheStandard pins the equality; a
	// vocabulary rename fails there instead of silently reading an
	// attribute no document carries.
	if impl := strings.TrimSpace(
		attrValue(se, observability.AttrImplementation),
	); impl != "" {
		opts = append(opts, activities.WithImplementation(impl))
	}

	return activities.NewServiceTask(name, op, opts...)
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
func parseGateway(
	asm *assembly,
	se xml.StartElement,
	id, name string,
	body nodeBody,
) (flow.Node, error) {
	opts := body.opts(id)

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

	gw, err := newGatewayOfKind(se.Name.Local, opts)
	if err != nil {
		return nil, err
	}

	// The default names a sequence flow that almost never exists yet —
	// modelers emit gateways before the flows leaving them — so it is
	// deferred to pass 2 like every other forward reference.
	//
	// Only the kinds the standard gives a default get one; the parallel
	// and event-based gateways have none, so the attribute is not read
	// for them at all (BPMN §13.4.1, §13.4.4).
	def := attrValue(se, "default")
	if def == "" || !gatewayTakesDefault(se.Name.Local) {
		return gw, nil
	}

	if err := deferDefaultFlow(asm, se.Name.Local, id, def, gw); err != nil {
		return nil, err
	}

	return gw, nil
}

// deferDefaultFlow records the gateway's default as a pass-2 reference.
//
// The type assertion is a guard rather than a formality: every gateway the
// model has today embeds the shared Gateway and so can hold a default, but
// nothing in the type system says a future one must — and a gateway that
// silently kept no default would route tokens by a rule the file did not
// describe.
func deferDefaultFlow(
	asm *assembly, local, id, def string, gw flow.Node,
) error {
	defaulter, ok := gw.(interface {
		UpdateDefaultFlow(*flow.SequenceFlow) error
	})
	if !ok {
		return errs.New(
			errs.M("bpmn: %s %q carries a default, which %T cannot hold",
				local, id, gw),
			errs.C(errorClass, errs.InvalidParameter))
	}

	asm.refs = append(asm.refs, flowRef{
		refSite: refSite{from: local + " " + id, attr: "default", target: def},
		apply:   defaulter.UpdateDefaultFlow,
	})

	return nil
}

// gatewayKinds maps a gateway element to its constructor. The complex
// gateway is deliberately absent — see SRD-089.C §4.1.
var gatewayKinds = map[string]func(...options.Option) (flow.Node, error){
	tagExclusiveGateway: func(o ...options.Option) (flow.Node, error) {
		return gateways.NewExclusiveGateway(o...)
	},
	tagParallelGateway: func(o ...options.Option) (flow.Node, error) {
		return gateways.NewParallelGateway(o...)
	},
	tagInclusiveGateway: func(o ...options.Option) (flow.Node, error) {
		return gateways.NewInclusiveGateway(o...)
	},
	tagEventBasedGtw: func(o ...options.Option) (flow.Node, error) {
		return gateways.NewEventBasedGateway(o...)
	},
}

// gatewaysWithDefault are the kinds BPMN gives a default sequence flow:
// the ones that CHOOSE among their outgoing flows by condition. A parallel
// gateway takes every outgoing flow and an event-based one is decided by
// its events, so neither has a condition to fall through.
var gatewaysWithDefault = map[string]bool{
	tagExclusiveGateway: true,
	tagInclusiveGateway: true,
	tagComplexGateway:   true,
}

// gatewayTakesDefault reports whether the element kind has a default.
func gatewayTakesDefault(local string) bool {
	return gatewaysWithDefault[local]
}

// newGatewayOfKind builds the gateway the element names.
func newGatewayOfKind(
	local string, opts []options.Option,
) (flow.Node, error) {
	build, ok := gatewayKinds[local]
	if !ok {
		return nil, errs.New(
			errs.M("bpmn: no gateway constructor for %q", local),
			errs.C(errorClass, errs.InvalidObject))
	}

	return build(opts...)
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
// conditionExpression (kept as inert text), an annotation (skipped), or
// foreign-namespace content (skipped). Anything else in the BPMN namespace
// is unsupported.
func (p *parser) parseSequenceFlowChild(fs *flowSpec, se xml.StartElement) error {
	if se.Name.Space != nsBPMN {
		return p.skipElement()
	}

	if parse, ok := sequenceFlowParsers[se.Name.Local]; ok {
		return parse(p, fs, se)
	}

	return p.settle(ctxSequenceFlow, se)
}

// parseNodeBody reads a flow node's children and returns what they
// contributed to its construction. incoming/outgoing duplicate the
// sequenceFlow wiring and are skipped; anything else in the BPMN namespace
// that no child parser claims is an UnsupportedElementError.
func (p *parser) parseNodeBody(se xml.StartElement) (nodeBody, error) {
	var body nodeBody

	for {
		tok, err := p.token()
		if err != nil {
			return nodeBody{}, err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			if err := p.parseNodeChild(&body, t); err != nil {
				return nodeBody{}, err
			}

		case xml.EndElement:
			if t.Name == se.Name {
				return body, nil
			}
		}
	}
}

// parseNodeChild handles one child start tag of a flow node.
func (p *parser) parseNodeChild(body *nodeBody, se xml.StartElement) error {
	if se.Name.Space != nsBPMN {
		return p.skipElement()
	}

	if parse, ok := nodeChildParsers[se.Name.Local]; ok {
		return parse(p, body, se)
	}

	return p.settle(ctxNode, se)
}

// parseDoc reads one <bpmn:documentation> element: its text plus the mime
// type of that text, defaulting per the standard when the attribute is
// absent.
func (p *parser) parseDoc(se xml.StartElement) (docSpec, error) {
	text, err := p.readText(se)
	if err != nil {
		return docSpec{}, err
	}

	format := strings.TrimSpace(attrValue(se, "textFormat"))
	if format == "" {
		format = defaultDocFormat
	}

	return docSpec{text: strings.TrimSpace(text), format: format}, nil
}

// fallbackName returns name, or the element's id when the model demands a
// non-empty one and BPMN did not supply it — `name` is 0..1 on every flow
// element, and modelers emit unlabelled boxes routinely, so refusing them
// would reject ordinary files. The process and the serviceTask already did
// this; FR-4 makes it the rule.
//
// The cost is visible on the way out: such an element re-exports carrying
// name="<its id>", because the model has nowhere to record that the name
// was synthesized. ADR-024 §2.8 makes the round-trip semantic rather than
// byte-lossless, and a name equal to the id asserts nothing the id did not.
func fallbackName(id, name string) string {
	if strings.TrimSpace(name) == "" {
		return id
	}

	return name
}

// build is pass 2: nodes are constructed and added to the process, flows
// are linked through the complete id→node table, every deferred
// reference is resolved against the finished index, and the graph is
// validated.
func build(p *parser, asm *assembly) (*process.Process, error) {
	// A message-typed event builds an ItemAwareElement over the message's
	// payload, and that needs the model's default data states to exist
	// (data/item.go:193-201). They are host setup — every example calls
	// CreateDefaultStates before building a process — but an importer
	// that refuses an ordinary message start event until the host has run
	// an unrelated initializer is not a usable contract.
	//
	// The call is idempotent and guarded: it fills the three globals only
	// when they are unset (data/state.go:86-91), so a host that
	// configured its own states before importing keeps them.
	if err := data.CreateDefaultStates(); err != nil {
		return nil, errs.New(
			errs.M("bpmn: couldn't create the model's default data states"),
			errs.C(errorClass, errs.BulidingFailed),
			errs.E(err))
	}

	if err := buildNodes(p, asm); err != nil {
		return nil, err
	}

	flowByID := make(map[string]*flow.SequenceFlow, len(asm.flows))

	for i := range asm.flows {
		fs := asm.flows[i]

		sf, err := linkFlow(asm, fs)
		if err != nil {
			return nil, err
		}

		flowByID[fs.id] = sf
	}

	if err := resolveRefs(asm.refs, newRefIndex(asm, flowByID)); err != nil {
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

	opts := append([]options.Option{foundation.WithID(fs.id)}, docOptions(fs.docs)...)

	if fs.name != "" {
		opts = append(opts, options.WithName(fs.name))
	}

	if fs.hasCond {
		cond, err := newCondition(fs, asm.exprLanguage)
		if err != nil {
			return nil, err
		}

		opts = append(opts, flow.WithCondition(cond))
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

// skipReporting swallows a subtree like skipElement, but reports every
// element it passes that belongs to a recognized dialect.
//
// <extensionElements> is where a Camunda file keeps its listeners, its
// form data and its I/O mapping, and swallowing it whole is exactly how
// those went missing without a word. Reporting only the OUTERMOST
// recognized element keeps one report per construct: a formData with six
// formFields is one thing the converter did not map, not seven.
func (p *parser) skipReporting(element string) error {
	depth := 1

	for depth > 0 {
		tok, err := p.token()
		if err != nil {
			return err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			if depth == 1 && t.Name.Space == nsCamunda {
				p.report(element, "camunda:"+t.Name.Local,
					dialectReason(t.Name.Local))
			}

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
		Section: sections[se.Name.Local],
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
