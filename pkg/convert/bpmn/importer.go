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
	"github.com/dr-dobermann/gobpm/pkg/model/artifacts"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/model/lanes"
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

	p, procs, err := imp.parse(ctx, r)
	if err != nil {
		return nil, err
	}

	// THE process of the document (ADR-024 §2.15, SRD-089.I §4.2): a
	// single-process document returns it whatever isExecutable says —
	// nothing existing breaks on a flag nobody sets — and a multi-process
	// one returns the single process marked executable. Anything else is
	// the ambiguity the document-level call exists for.
	if len(procs) == 1 {
		return procs[0], nil
	}

	var chosen *process.Process

	executable := 0

	for i, asm := range p.asms {
		if asm.spec.executable {
			executable++
			chosen = procs[i]
		}
	}

	if executable == 1 {
		return chosen, nil
	}

	return nil, errs.New(
		errs.M("bpmn: the document carries %d processes, %d marked "+
			"isExecutable; Import returns THE process of a document — use "+
			"ImportDocument for the set", len(procs), executable),
		errs.C(errorClass, errs.InvalidObject))
}

// parse is the one parse both entry points share. It returns the parser
// alongside the processes so a caller can read the document facts —
// Import the executable flags, ImportDocument the report.
func (importer) parse(
	ctx context.Context, r io.Reader,
) (*parser, []*process.Process, error) {
	p := newParser(ctx, r)

	procs, err := p.parse()
	if err != nil {
		return nil, nil, err
	}

	return p, procs, nil
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
	// laneSets are a container's <laneSet> children. They reach the model
	// as a CONSTRUCTION option, which is why they are collected with the
	// rest of the body and not added afterwards.
	laneSets []laneSetSpec
	// props are the node's <property> children, reaching their owner the
	// same way a laneSet does — as a construction option (§4.6).
	props []propSpec
	// io is the node's <ioSpecification>, at most one (SRD-089.G FR-1).
	io *ioSpec
	// loop is the node's loop marker, at most one of either kind
	// (SRD-089.H FR-5).
	loop *loopSpec
	// dataAssocs are the node's data associations, wired in the pass
	// after the data elements exist (SRD-089.G §4.1).
	dataAssocs []dataAssocSpec
	// extra are options read from the element's own attributes by the one
	// funnel every node passes through, rather than by each builder that
	// remembers to. A builder that forgets is how a documented attribute
	// goes missing (SRD-089.D §4.13).
	extra []options.Option
}

// opts renders the body as construction options, with the element's id
// always first.
func (b nodeBody) opts(id string) []options.Option {
	opts := append([]options.Option{foundation.WithID(id)}, docOptions(b.docs)...)

	return append(opts, b.extra...)
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
	p, procs, err := imp.parse(ctx, r)
	if err != nil {
		return nil, err
	}

	return &convert.Result{
		Processes: procs,
		Dropped:   p.dropped,
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
	// container is the id of the container that holds this node, "" for
	// the process. Read from the parser at parse time, because by pass 2
	// the element's position in the file is gone (SRD-089.E §4.1).
	container string
	body      nodeBody
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
	// dataElems are the built data elements by id, recorded by
	// buildDataElements for the association pass that wires them
	// (SRD-089.G §4.1). A collapsed reference contributes no entry — the
	// pass retargets through the SPEC (rule 2), then looks the object up
	// here.
	dataElems map[string]flow.Element
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
	// items are the document's item definitions, built by id at the start
	// of pass 2 — after every <import> has been read, since one may
	// follow the <itemDefinition> naming its namespace (§4.8).
	items map[string]*data.ItemDefinition
	// declared is the pass-1 id table: which element claimed each id.
	// byID cannot serve, because the nodes it maps to do not exist until
	// pass 2 (see nodeSpec).
	declared map[string]string
	specs    []nodeSpec // document order
	flows    []flowSpec
	// refs are the forward references pass 1 could not resolve because
	// their target had not been parsed yet (SRD-089.A §FR-2).
	refs []pendingRef
	// assocs are the <association> elements read in pass 1, consumed in
	// pass 2 by the compensation boundaries that name their handlers
	// through them (SRD-089.E §4.7).
	assocs []assocSpec
	// places are the lane placements waiting on the id table: a lane is
	// built with its container, and the nodes it names are built after
	// (SRD-089.E §4.3).
	places []placement
	// datas are the item-aware flow elements read in pass 1 — a data
	// object, or a reference to one — built after the nodes, since the
	// container holding one IS a node (SRD-089.F §4.4).
	datas []dataSpec
	// annots and groups are the carried artifacts read in pass 1, built
	// by buildCarriedArtifacts once their containers exist (SRD-092
	// FR-7/FR-8).
	annots []annotationSpec
	groups []groupSpec
	// artsByID are the built carrier artifacts by their DECLARED ids —
	// the ids an association end can reference. One without a declared id
	// is unreferencable and contributes no entry.
	artsByID map[string]artifacts.Artifact
	// lanesByID are the built lanes and lane sets by declared id, for the
	// same reason: a lane is model-held, so an association may end on it
	// (ADR-039 §2.6 degrades only what the model does NOT hold).
	lanesByID map[string]foundation.Identifyer
	// spec is the buffered <process> itself, built first in pass 2 — see
	// procSpec.
	spec procSpec
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
	// items holds the document's <itemDefinition> declarations, the
	// <import>s they name and the xmlns prefixes that connect the two
	// (SRD-089.F §4.1, §4.8).
	items *items
	// stores holds the document's <dataStore> declarations, kept only to
	// be reported as host obligations (SRD-089.F §4.5).
	stores []dataStoreSpec
	// rootDefs are the definitions-level event definitions, by id — the
	// position a Multi-Instance behavior ref resolves against
	// (SRD-089.H §4.7).
	rootDefs map[string]defSpec
	// categoryValues is the document's categoryValue id → value lookup,
	// read from the definitions-level <category> declarations. It is
	// resolution input, not model state (ADR-039 §2.3): a group embeds
	// the value it resolves to, and nothing else ever reads a category.
	// Document-level, like the catalog: a <category> may follow the
	// <process> whose group refers to it.
	categoryValues map[string]string
	// asms are the document's processes, one assembly each, in document
	// order (SRD-089.I §4.1).
	asms []*assembly
	// collabs are the document's collaborations, consumed definitionally
	// once the whole document is read (SRD-089.I FR-3, FR-4).
	collabs []collabSpec
	// ids is the document's one id ledger: every element that declares an
	// id claims it here, whatever per-kind table it lands in afterwards —
	// see claimID.
	ids map[string]string
	// exprLanguage is <definitions expressionLanguage>, the default an
	// expression that declares none inherits (ADR-024 §2.10).
	exprLanguage string
	// owner is the id of the element currently being parsed, so a report
	// about its <extensionElements> — which carries no id of its own —
	// names the element a reader can find in the file.
	owner string
	// container is the id of the FlowElementsContainer whose children are
	// being parsed — "" for the process itself. A flow node records it, so
	// pass 2 knows which container to add the node to (SRD-089.E §4.1).
	//
	// Sequence flows need no equivalent: flow.Link puts a new flow in its
	// SOURCE node's container (`sequenceflow.go:139`), so an inner flow
	// follows the node it leaves, and a flow drawn across a container edge
	// is the model's rule to refuse rather than the converter's.
	container string
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
		dec:            xml.NewDecoder(r),
		ctx:            ctx,
		newProcess:     process.New,
		interfaces:     make(map[string]string),
		ops:            make(map[string]opSpec),
		cat:            newCatalog(),
		items:          newItems(),
		ids:            make(map[string]string),
		rootDefs:       make(map[string]defSpec),
		categoryValues: make(map[string]string),
	}
}

// parse decodes <bpmn:definitions> and every <bpmn:process> it carries
// (SRD-089.I FR-1), returning the processes in document order.
func (p *parser) parse() ([]*process.Process, error) {
	root, err := p.rootElement()
	if err != nil {
		return nil, err
	}

	err = p.parseDefinitions(root)
	if err != nil {
		return nil, err
	}

	if len(p.asms) == 0 {
		return nil, errs.New(
			errs.M("bpmn: no <process> element found in <definitions>"),
			errs.C(errorClass, errs.ObjectNotFound))
	}

	// The document-level work, once — not once per process (§4.3): the
	// default states, the items every process's elements copy from, and
	// (after the builds) the store obligations.
	//
	// CreateDefaultStates is unreachable-failing: it builds three
	// SrcStates from three non-empty package constants, and is idempotent
	// for a host that configured its own. Said in the form the coverage
	// gate reads.
	// The collaborations first: a participant's processRef and a
	// message flow's messageRef resolve against the now-complete ledger,
	// and the flows report (SRD-089.I §4.4).
	err = resolveCollaborations(p)
	if err != nil {
		return nil, err
	}

	err = data.CreateDefaultStates()
	if err != nil {
		return nil, errs.Invariant(
			"the model's default data states could not be created: %w", err)
	}

	items, err := buildItems(p)
	if err != nil {
		return nil, err
	}

	procs := make([]*process.Process, 0, len(p.asms))

	for _, asm := range p.asms {
		asm.items = items

		proc, err := build(p, asm)
		if err != nil {
			return nil, err
		}

		procs = append(procs, proc)
	}

	p.reportDataStores()

	return procs, nil
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

		// The root is where a document declares its namespaces, and an
		// <itemDefinition>'s structureRef prefix is resolved against them
		// (§4.8). The decoder resolves element and attribute NAMES; a
		// prefix inside an attribute VALUE is ours to resolve.
		p.items.declareNamespaces(se)

		// <definitions> carries dialect attributes too (a modeler's own
		// bookkeeping); they are reported against the document.
		p.reportUnmappedAttrs(se, attrValue(se, "id"), nil)

		return se, nil
	}
}

// parseDefinitions walks the children of <bpmn:definitions>, collecting
// every <bpmn:process> into p.asms in document order (SRD-089.I §4.1);
// any other unmapped in-namespace element is reported per SRD-051 §FR-7.
// Foreign-namespace subtrees and non-executable annotations
// (documentation, extensionElements) are skipped.
func (p *parser) parseDefinitions(root xml.StartElement) error {
	for {
		tok, err := p.token()
		if err != nil {
			return err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			next, err := p.handleDefinitionsChild(t)
			if err != nil {
				return err
			}

			if next != nil {
				p.asms = append(p.asms, next)
			}

		case xml.EndElement:
			if t.Name == root.Name {
				return nil
			}
		}
	}
}

// handleDefinitionsChild processes one start element under <definitions>.
// Returns a non-nil assembly when a <process> was parsed.
func (p *parser) handleDefinitionsChild(
	se xml.StartElement,
) (*assembly, error) {
	if se.Name.Space != nsBPMN {
		// Foreign namespace — diagram interchange, a vendor dialect — is
		// out of execution scope and skipped whole (ADR-024 §2.7).
		return nil, p.skipElement()
	}

	if parse, ok := definitionsParsers[se.Name.Local]; ok {
		return parse(p, se)
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

	// The process's own id joins the one ledger too: a node reusing it
	// was as silent as any other cross-table duplicate (§4.11).
	if err := p.claimID(id, tagProcess); err != nil {
		return nil, err
	}

	// process.New demands a non-empty name; fall back to the id.
	name := fallbackName(id, attrValue(se, "name"))

	// isExecutable disambiguates Import on a multi-process document
	// (SRD-089.I §4.2); the engine itself runs whatever it is handed.
	executable := attrBool(se, "isExecutable", false)

	// The process is built LAZILY — see procBuild.
	p.owner = id

	// The process element carries dialect attributes of its own —
	// versionTag, historyTimeToLive, the starter authorizations — and
	// nothing else scans them, so they would vanish exactly as the
	// coverage audit found them vanishing.
	p.reportUnmappedAttrs(se, id, nil)

	pb := &procBuild{p: p, id: id, name: name, executable: executable}

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
	// laneSets are buffered for the same reason docs are: they reach the
	// process as a construction option, and BPMN serializes a container's
	// laneSets ahead of its flowElements.
	laneSets []laneSetSpec
	// props are buffered for the same reason again (§4.6): a property is
	// a construction option, and BPMN serializes a process's properties
	// ahead of its flowElements too.
	props []propSpec
	// executable rides through to procSpec — see there.
	executable bool
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

	if se.Name.Local == tagLaneSet {
		return pb.laneSet(se)
	}

	if se.Name.Local == tagProperty {
		return pb.property(se)
	}

	if pb.asm == nil {
		pb.build()
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

// laneSet records a <laneSet>, or refuses one that arrives after the
// process has been built. Both halves match doc(): the option cannot be
// applied to a process that already exists, and dropping it silently is
// what the feedback contract exists to prevent.
func (pb *procBuild) laneSet(se xml.StartElement) error {
	if pb.asm != nil {
		return errs.New(
			errs.M("bpmn: <process> %q carries <laneSet> after its flow "+
				"elements; a container's laneSets precede its flowElements",
				pb.id),
			errs.C(errorClass, errs.InvalidObject))
	}

	ls, err := pb.p.parseLaneSet(se)
	if err != nil {
		return err
	}

	pb.laneSets = append(pb.laneSets, ls)

	return nil
}

// property records a <property>, or refuses one that arrives after the
// process's flow elements — the same two halves as laneSet(), for the
// same reasons (§4.6).
func (pb *procBuild) property(se xml.StartElement) error {
	if pb.asm != nil {
		return errs.New(
			errs.M("bpmn: <process> %q carries <property> after its flow "+
				"elements; a process's properties precede its flowElements",
				pb.id),
			errs.C(errorClass, errs.InvalidObject))
	}

	spec, err := pb.p.parsePropertySpec(se)
	if err != nil {
		return err
	}

	pb.props = append(pb.props, spec)

	return nil
}

// build creates the pass-1 state; the process itself is built in pass 2.
func (pb *procBuild) build() {
	pb.asm = pb.p.newAssembly(procSpec{
		id:         pb.id,
		name:       pb.name,
		docs:       pb.docs,
		laneSets:   pb.laneSets,
		props:      pb.props,
		executable: pb.executable,
	})
}

// finish returns the assembly, building an empty process when the element
// carried no flow elements at all.
func (pb *procBuild) finish() (*assembly, error) {
	if pb.asm == nil {
		pb.build()
	}

	return pb.asm, nil
}

// procSpec is the <process> as pass 1 read it: everything its
// construction options need. The process was built when its first flow
// element arrived until properties landed; a property's itemSubjectRef
// may name an <itemDefinition> declared AFTER the </process>, so the
// construction moved to pass 2 with the nodes — the same document-order
// freedom that defers them defers it (§4.6).
type procSpec struct {
	id, name string
	docs     []docSpec
	laneSets []laneSetSpec
	props    []propSpec
	// executable is <process isExecutable>, read only by Import's
	// selection rule (SRD-089.I §4.2).
	executable bool
}

// newAssembly builds the pass-1 state the flow elements are parsed into.
func (p *parser) newAssembly(spec procSpec) *assembly {
	return &assembly{
		spec:         spec,
		byID:         make(map[string]flow.Node),
		declared:     make(map[string]string),
		dataElems:    make(map[string]flow.Element),
		artsByID:     make(map[string]artifacts.Artifact),
		lanesByID:    make(map[string]foundation.Identifyer),
		interfaces:   p.interfaces,
		ops:          p.ops,
		cat:          p.cat,
		exprLanguage: p.exprLanguage,
	}
}

// constructProcess builds the process from its buffered spec. It runs
// first in pass 2 — after buildItems, so a property can resolve its item,
// and before buildNodes, which adds every node to it.
func constructProcess(p *parser, asm *assembly) error {
	spec := asm.spec

	sets, err := buildLaneSets(asm, spec.laneSets)
	if err != nil {
		return err
	}

	opts := append(
		[]options.Option{foundation.WithID(spec.id)}, docOptions(spec.docs)...)
	if len(sets) != 0 {
		opts = append(opts, lanes.WithLaneSets(sets...))
	}

	if len(spec.props) != 0 {
		props, propErr := buildProperties(p, asm, spec.props)
		if propErr != nil {
			return propErr
		}

		opts = append(opts, data.WithProperties(props...))
	}

	proc, err := p.newProcess(spec.name, opts...)
	if err != nil {
		return errs.New(
			errs.M("bpmn: couldn't create process %q", spec.id),
			errs.C(errorClass, errs.BulidingFailed),
			errs.E(err))
	}

	asm.proc = proc

	return nil
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

	err = p.claimID(id, se.Name.Local)
	if err != nil {
		return err
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
		se:        se,
		id:        id,
		name:      attrValue(se, "name"),
		container: p.container,
		body:      body,
	})
	asm.declared[id] = se.Name.Local

	return nil
}

// parseContainer reads a FlowElementsContainer element — a <subProcess>
// and, from the next milestone, its variants — whose children are flow
// elements of its own rather than a flow node's body.
//
// Its own spec is recorded in the container that holds IT (nesting is
// just this function calling itself through the process table), and the
// slot is reserved BEFORE the children are read, so the id is claimed
// against duplicates and document order survives a container whose
// children are parsed first.
func (p *parser) parseContainer(asm *assembly, se xml.StartElement) error {
	id, err := requiredID(se)
	if err != nil {
		return err
	}

	err = p.claimID(id, se.Name.Local)
	if err != nil {
		return err
	}

	idx := len(asm.specs)

	asm.specs = append(asm.specs, nodeSpec{
		se:        se,
		id:        id,
		name:      attrValue(se, "name"),
		container: p.container,
	})
	asm.declared[id] = se.Name.Local

	outerOwner, outerContainer := p.owner, p.container
	p.owner, p.container = id, id

	body, err := p.parseContainerBody(asm, se)

	p.owner, p.container = outerOwner, outerContainer

	if err != nil {
		return err
	}

	// Re-indexed rather than held as a pointer: parsing the children
	// appends to the same slice, which may move it.
	asm.specs[idx].body = body

	return nil
}

// parseContainerBody reads the children of a container: its flow
// elements through the SAME table <process> uses, and everything else as
// an ordinary node body.
//
// One table for both containers is the point. A sub-process holds what a
// process holds — BPMN says so through FlowElementsContainer — and a
// second table would be a second answer to "which elements are flow
// elements", diverging on the first element added to one of them.
func (p *parser) parseContainerBody(
	asm *assembly, se xml.StartElement,
) (nodeBody, error) {
	var body nodeBody

	for {
		tok, err := p.token()
		if err != nil {
			return nodeBody{}, err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			if err := p.parseContainerChild(asm, &body, t); err != nil {
				return nodeBody{}, err
			}

		case xml.EndElement:
			if t.Name == se.Name {
				return body, nil
			}
		}
	}
}

// parseContainerChild routes one child of a container.
func (p *parser) parseContainerChild(
	asm *assembly, body *nodeBody, se xml.StartElement,
) error {
	if se.Name.Space != nsBPMN {
		return p.skipElement()
	}

	if se.Name.Local == tagLaneSet {
		ls, err := p.parseLaneSet(se)
		if err != nil {
			return err
		}

		body.laneSets = append(body.laneSets, ls)

		return nil
	}

	if parse, ok := processParsers[se.Name.Local]; ok {
		return parse(p, asm, se)
	}

	return p.parseNodeChild(body, se)
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
	//
	// Every node exists by now, so a container is always available to the
	// nodes it holds — which is why adding is a second pass over `built`
	// rather than part of building.
	for i, node := range built {
		owner, err := containerFor(asm, asm.specs[i].container)
		if err != nil {
			return err
		}

		if err := owner.Add(node); err != nil {
			return errs.New(
				errs.M("bpmn: couldn't add node %q to %q",
					node.ID(), owner.ID()),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(err))
		}
	}

	return nil
}

// elementContainer is the half of a BPMN FlowElementsContainer this
// converter needs: something a flow element can be added to, that can
// name itself in an error. Both *process.Process and
// *activities.SubProcess satisfy it.
type elementContainer interface {
	ID() string
	Add(flow.Element) error
}

// containerFor resolves a nodeSpec's container id to the thing that holds
// it, the process for "".
func containerFor(asm *assembly, id string) (elementContainer, error) {
	if id == "" {
		return asm.proc, nil
	}

	// Both guards below are unreachable from any document, and say so in
	// the form the coverage gate recognizes rather than in a comment it
	// cannot read. A container id reaches here only from a nodeSpec whose
	// container field parseContainer set, and parseContainer sets it only
	// for <subProcess> and <transaction> — which the same pass builds,
	// and builds into a *activities.SubProcess. Reaching either means the
	// converter wired the container field to something else.
	n, ok := asm.byID[id]
	if !ok {
		return nil, errs.Invariant(
			"container %q holds nodes but was never built", id)
	}

	c, ok := n.(elementContainer)
	if !ok {
		return nil, errs.Invariant(
			"container %q is a %T, which holds no flow elements", id, n)
	}

	return c, nil
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

	// isForCompensation is an Activity attribute, so it belongs to no
	// single builder — reading it in each one is the shape that lost the
	// dialect report until §4.13. An element that cannot take the option
	// is refused by the model, which is the correct answer for a document
	// carrying it on a gateway.
	if attrBool(s.se, attrForCompensation, false) {
		s.body.extra = append(s.body.extra, activities.WithCompensation())
	}

	// Properties ride the same funnel and the same rule: BPMN gives them
	// to a process, an activity and an event, which are exactly the model
	// types that take the option — anything else refuses it itself, with
	// this node's id wrapped around the refusal (SRD-089.F FR-6).
	if len(s.body.props) != 0 {
		props, err := buildProperties(p, asm, s.body.props)
		if err != nil {
			return nil, err
		}

		s.body.extra = append(s.body.extra, data.WithProperties(props...))
	}

	// An ioSpecification is refused here, not at parse, for the node
	// kinds that cannot hold one: the child table is deliberately
	// uniform, and only the build knows the owner's kind (SRD-089.G
	// §4.7/§4.7a).
	if s.body.io != nil {
		if !paramOwners[s.se.Name.Local] {
			return nil, ioSpecMisplaced(s)
		}

		ioOpts, err := buildIOParams(p, asm, s)
		if err != nil {
			return nil, err
		}

		s.body.extra = append(s.body.extra, ioOpts...)
	}

	// A loop marker rides the same funnel: every activity kind takes
	// WithLoop, and anything else refuses the option itself with this
	// node's id wrapped around the refusal (SRD-089.H §4.1).
	if s.body.loop != nil {
		loopOpt, err := buildLoopOption(p, asm, s)
		if err != nil {
			return nil, err
		}

		s.body.extra = append(s.body.extra, loopOpt)
	}

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

	err = p.claimID(id, se.Name.Local)
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

	err = p.claimID(id, se.Name.Local)
	if err != nil {
		return err
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
	// Unreachable from any document: gatewayTakesDefault already limited
	// the callers to the gateway kinds whose constructors return a type
	// with this method. Reaching it means that table and the model
	// disagree about which gateways carry a default.
	defaulter, ok := gw.(interface {
		UpdateDefaultFlow(*flow.SequenceFlow) error
	})
	if !ok {
		return errs.Invariant(
			"%s %q takes a default flow, but %T cannot hold one", local, id, gw)
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

	// A flow's id was unguarded before the ledger (SRD-089.F §4.11): two
	// flows sharing one silently overwrote each other in pass 2's
	// id→flow table, and a flow reusing a node's id poisoned every
	// reference to that node.
	err = p.claimID(id, se.Name.Local)
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
	// The default states and the item map are the DOCUMENT's, prepared
	// once by parse() before the per-process builds (SRD-089.I §4.3);
	// asm.items arrives set. A direct caller (a test) sets it itself.
	//
	// The process first: everything else is added TO it, and its own
	// properties resolve against the document's items (§4.6).
	if err := constructProcess(p, asm); err != nil {
		return nil, err
	}

	if err := buildNodes(p, asm); err != nil {
		return nil, err
	}

	// After the nodes, because a container IS a node: the sub-process
	// holding a data object does not exist until the node pass built it
	// (SRD-089.F §4.4).
	if err := buildDataElements(p, asm); err != nil {
		return nil, err
	}

	// After both families exist: the data associations, wired through
	// the elements' own Associate* (SRD-089.G §4.1). The store report is
	// the document's, fired once by parse() (SRD-089.I §4.3).
	if err := wireDataAssociations(p, asm); err != nil {
		return nil, err
	}

	// After the nodes exist and before Validate: a lane names nodes, and
	// the container's own validation checks that what a lane holds is what
	// the container holds (§4.3).
	if err := placeLaneNodes(asm); err != nil {
		return nil, err
	}

	// The carried artifacts, once their containers exist: annotations,
	// then groups with their category values resolved (SRD-092 FR-7/FR-8).
	if err := buildCarriedArtifacts(p, asm); err != nil {
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

	// The plain associations, last among the artifacts: an end may be
	// anything pass 2 built — a node, a data element, a sequence flow,
	// or a carried artifact (SRD-092 FR-9/FR-10).
	if err := buildAssociations(p, asm, flowByID); err != nil {
		return nil, err
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
// in-namespace element (SRD-051 §FR-3/§FR-7). An element whose refusal
// has a record beyond itself — a capability row, or the right position —
// additionally names it, per ADR-038 §2.2's rule that the record IS the
// refusal's content.
func unsupported(se xml.StartElement) error {
	return &convert.UnsupportedElementError{
		Tag:     se.Name.Local,
		ID:      attrValue(se, "id"),
		Section: sections[se.Name.Local],
		Planned: plannedNotes[se.Name.Local],
	}
}

// dataParamNote explains a bare parameter or set outside an
// <ioSpecification>. It covers both positions one refusal can fire from:
// on a task the element belongs inside the spec (the standard's
// structure), and on an event the bare form is legal BPMN awaiting the
// model's attachment capability (#329) — the settle path cannot tell the
// two owners apart, so the note carries both truths.
const dataParamNote = "on a task this element lives inside its " +
	"<ioSpecification> (§10.4.1) — write it there; an event's bare I/O " +
	"awaits the event data attachment capability, #329"

// dataAssocNote explains an association outside an activity's body — its
// only importable position since SRD-089.G.
const dataAssocNote = "a data association lives on the activity whose " +
	"parameter it wires (§10.4.1); write it inside that task"

// plannedNotes names the record behind each refused data-family tag —
// after SRD-089.G none of them is STAGED: a task's family imports, and
// what remains is a capability row (ADR-038 §2.3) or a position the
// standard reserves. A table rather than per-site wording so one family
// reads as one answer.
var plannedNotes = map[string]string{
	tagIOSpecification: "a task's <ioSpecification> imports; the " +
		"process-level I/O carrier is the missing capability, #330 " +
		"(ADR-011 §2.5's planned work)",
	tagDataInput:       dataParamNote,
	tagDataOutput:      dataParamNote,
	tagInputSet:        dataParamNote,
	tagOutputSet:       dataParamNote,
	tagDataInputAssoc:  dataAssocNote,
	tagDataOutputAssoc: dataAssocNote,
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

// claimID claims id in the document's one id ledger, or says who holds it.
//
// The parser keeps several per-kind tables — the flow elements, the
// catalog objects, the item definitions, the stores, the operations — but
// the ids they hold are one vocabulary: every reference attribute in a
// document (a dataObjectRef, an itemSubjectRef, an operationRef, an
// attachedToRef) resolves by id, and resolution probes those tables in a
// fixed order. Two elements sharing an id would collide in no table and
// would instead make every reference to that id silently ambiguous — the
// resolver finding whichever of the two its probe order reaches first.
// So uniqueness is enforced here, once, at declaration, whatever table
// the element lands in afterwards.
func (p *parser) claimID(id, local string) error {
	if kind, dup := p.ids[id]; dup {
		return errs.New(
			errs.M("bpmn: duplicate id %q on <%s>; <%s> already declared it",
				id, local, kind),
			errs.C(errorClass, errs.DuplicateObject))
	}

	p.ids[id] = local

	return nil
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
