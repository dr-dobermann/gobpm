package bpmn

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"slices"

	"github.com/dr-dobermann/gobpm/pkg/convert"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
)

// exporter serializes a *process.Process into BPMN 2.0 XML over the same
// executable-core MVP subset the importer accepts (SRD-051 §FR-6/§FR-8). It
// is registered in the convert seam under convert.BPMN by this package's
// init.
type exporter struct{}

// Export marshals p as a <bpmn:definitions>/<bpmn:process> document and
// writes it to w with an XML header and two-space indentation. No Diagram
// Interchange is emitted (SRD-051 §FR-6).
//
// Every model node must map into the MVP subset; a node of any other type
// (sub-process, inclusive gateway, boundary event, ...) aborts the export
// with *convert.UnsupportedElementError — the export-side half of the
// "clear feedback on unsupported elements" requirement (SRD-051 §FR-3).
//
// Sequence-flow conditions are written back only when the condition carries
// its source text (the converter's own *formalExpression does, via Body);
// a compiled condition without source text aborts the export with a
// classified error rather than silently dropping the condition
// (SRD-051 §FR-5, open question 2).
func (exporter) Export(ctx context.Context, w io.Writer, p *process.Process) error {
	if ctx == nil {
		return errs.New(
			errs.M("bpmn.Export: ctx is nil"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if w == nil {
		return errs.New(
			errs.M("bpmn.Export: w is nil"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if p == nil {
		return errs.New(
			errs.M("bpmn.Export: p is nil"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	// Propagate cancellation as-is so callers can use errors.Is(err,
	// context.Canceled) — do not wrap context errors (k8s convention).
	if err := ctx.Err(); err != nil {
		return err
	}

	defs, err := buildDefinitions(ctx, p)
	if err != nil {
		// UnsupportedElementError and other ownError values pass through
		// unchanged so errors.As keeps working.
		return wrapErr(
			fmt.Sprintf("bpmn.Export: couldn't build definitions for process %q", p.ID()),
			errs.BulidingFailed,
			err)
	}

	if _, err := io.WriteString(w, xml.Header); err != nil {
		return errs.New(
			errs.M("bpmn.Export: couldn't write XML header"),
			errs.C(errorClass, errs.BulidingFailed),
			errs.E(err))
	}

	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")

	// Encode flushes the encoder's buffer itself before returning, so no
	// separate Flush is needed — an extra one could only ever drain an empty
	// buffer and return nil.
	if err := enc.Encode(defs); err != nil {
		return errs.New(
			errs.M("bpmn.Export: couldn't encode process %q", p.ID()),
			errs.C(errorClass, errs.BulidingFailed),
			errs.E(err))
	}

	return nil
}

// xmlDefinitions is the root document element. The xmlns:bpmn declaration
// makes every bpmn:-prefixed child resolve to the BPMN 2.0 model namespace
// on re-import. Interfaces (service catalog) are emitted before the process
// when ServiceTask.Operation() is available (gobpm ≥ Operation getter).
type xmlDefinitions struct {
	XMLName         xml.Name `xml:"bpmn:definitions"`
	XMLNS           string   `xml:"xmlns:bpmn,attr"`
	ID              string   `xml:"id,attr"`
	TargetNamespace string   `xml:"targetNamespace,attr"`
	Interfaces      []xmlInterface
	Process         xmlProcess
}

// xmlInterface is a definitions-level <bpmn:interface> wrapping operations
// referenced by serviceTasks (SRD-051 §4.6).
type xmlInterface struct {
	XMLName    xml.Name
	ID         string         `xml:"id,attr"`
	Name       string         `xml:"name,attr,omitempty"`
	Operations []xmlOperation `xml:"bpmn:operation"`
}

// xmlOperation is a <bpmn:operation> under an interface. Message refs are
// not re-emitted in this slice (nil messages on import).
type xmlOperation struct {
	XMLName xml.Name
	ID      string `xml:"id,attr"`
	Name    string `xml:"name,attr,omitempty"`
}

// xmlProcess holds the process attributes and its flow elements. Elements
// is an []any so nodes and flows marshal each with their own tag.
//
// IsExecutable trails Elements for govet/fieldalignment; encoding/xml
// collects attributes in a pass of their own, so the emitted attribute
// order is unaffected by where the bool sits.
type xmlProcess struct {
	XMLName      xml.Name `xml:"bpmn:process"`
	ID           string   `xml:"id,attr"`
	Name         string   `xml:"name,attr,omitempty"`
	Docs         []xmlDoc `xml:"bpmn:documentation,omitempty"`
	Elements     []any
	IsExecutable bool `xml:"isExecutable,attr"`
}

// xmlDoc is a <bpmn:documentation>: the text as character data plus the
// mime type of that text. textFormat is omitted at its text/plain default
// (BPMN elements/foundation.md:277-278), so a document that never named a
// format does not acquire one on the way out.
type xmlDoc struct {
	XMLName    xml.Name
	TextFormat string `xml:"textFormat,attr,omitempty"`
	Text       string `xml:",chardata"`
}

// docsXML renders an element's documentation. Documentation precedes an
// element's other content, which is where BaseElement puts it.
func docsXML(docs []*foundation.Documentation) []xmlDoc {
	if len(docs) == 0 {
		return nil
	}

	out := make([]xmlDoc, 0, len(docs))

	for _, d := range docs {
		if d == nil {
			continue
		}

		xd := xmlDoc{
			XMLName: xml.Name{Local: "bpmn:" + tagDocumentation},
			Text:    d.Text(),
		}

		if f := d.Format(); f != defaultDocFormat {
			xd.TextFormat = f
		}

		out = append(out, xd)
	}

	return out
}

// xmlNode is any flow node; Tag selects the concrete element name
// ("startEvent", "task", ...). The bpmn: prefix is written literally — the
// namespace is declared on the root element.
type xmlNode struct {
	XMLName        xml.Name
	ID             string `xml:"id,attr"`
	Name           string `xml:"name,attr,omitempty"`
	Direction      string `xml:"gatewayDirection,attr,omitempty"`
	Default        string `xml:"default,attr,omitempty"`
	Implementation string `xml:"implementation,attr,omitempty"`
	OperationRef   string `xml:"operationRef,attr,omitempty"`
	// Docs trails the attributes for govet/fieldalignment. Only the
	// element fields' order reaches the document — encoding/xml collects
	// attributes in a pass of their own — and Docs is the only one here.
	Docs []xmlDoc `xml:"bpmn:documentation,omitempty"`
}

// xmlSequenceFlow is a <bpmn:sequenceFlow> with optional documentation and
// an optional condition.
//
// The two child fields encode a document contract, not a memory layout:
// encoding/xml writes children in FIELD order, and BaseElement's
// documentation precedes an element's own content, so Docs must stay ahead
// of Condition. The aligner's preferred order emits an out-of-order
// document — which the first attempt here did, caught by
// TestDocumentationPrecedesTheCondition. Sixteen bytes on a transient
// marshaling struct do not buy a wrong file.
//
//nolint:govet // fieldalignment: see above — element order is the contract
type xmlSequenceFlow struct {
	XMLName   xml.Name
	ID        string `xml:"id,attr"`
	Name      string `xml:"name,attr,omitempty"`
	SourceRef string `xml:"sourceRef,attr"`
	TargetRef string `xml:"targetRef,attr"`
	// The two child elements trail the attributes for
	// govet/fieldalignment, and Docs must stay ahead of Condition:
	// encoding/xml writes children in FIELD order, and BaseElement's
	// documentation precedes an element's own content. Pinned by
	// TestDocumentationPrecedesTheCondition — reordering these two to
	// please the aligner silently emits an out-of-order document.
	Docs      []xmlDoc      `xml:"bpmn:documentation,omitempty"`
	Condition *xmlCondition `xml:"bpmn:conditionExpression,omitempty"`
}

// xmlCondition is a <bpmn:conditionExpression>: the expression text as
// character data plus its id and language URI.
type xmlCondition struct {
	ID       string `xml:"id,attr,omitempty"`
	Language string `xml:"language,attr,omitempty"`
	Body     string `xml:",chardata"`
}

// bodyCarrier is implemented by conditions that keep their source text —
// the converter's own formalExpression and any compatible expression.
type bodyCarrier interface {
	Body() string
}

// buildDefinitions maps the process model into the XML struct tree.
// ctx is checked between elements so a canceled export aborts without
// walking the whole graph (SRD-051 open question on per-element ctx checks —
// entry-only was fine at MVP sizes; this closes it for large models).
func buildDefinitions(ctx context.Context, p *process.Process) (*xmlDefinitions, error) {
	proc := xmlProcess{
		ID:           p.ID(),
		Name:         p.Name(),
		Docs:         docsXML(p.Docs()),
		IsExecutable: true,
		Elements:     make([]any, 0, len(p.Nodes())+len(p.Flows())),
	}

	// opsByID collects the ServiceTask operations for the definitions-level
	// interface catalog.
	opsByID := map[string]service.Operation{}

	for _, n := range orderNodes(p.Nodes(), p.Flows()) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		xn, err := nodeXML(n, opsByID)
		if err != nil {
			return nil, err
		}

		proc.Elements = append(proc.Elements, *xn)
	}

	for _, f := range orderFlows(p.Flows()) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		xf, err := flowXML(f)
		if err != nil {
			return nil, err
		}

		proc.Elements = append(proc.Elements, *xf)
	}

	return &xmlDefinitions{
		XMLNS:           nsBPMN,
		ID:              p.ID() + "-definitions",
		TargetNamespace: "http://bpmn.io/schema/bpmn",
		Interfaces:      interfacesXML(p.ID(), opsByID),
		Process:         proc,
	}, nil
}

// interfacesXML groups discovered operations under a single synthetic
// interface so re-import can resolve operationRef. One interface per process
// is enough for this slice (message bindings deferred).
func interfacesXML(processID string, ops map[string]service.Operation) []xmlInterface {
	if len(ops) == 0 {
		return nil
	}

	ifaceID := processID + "-services"
	opsXML := make([]xmlOperation, 0, len(ops))

	// Stable order by id for deterministic export.
	ids := make([]string, 0, len(ops))
	for id := range ops {
		ids = append(ids, id)
	}

	slices.Sort(ids)

	for _, id := range ids {
		op := ops[id]
		opsXML = append(opsXML, xmlOperation{
			XMLName: xml.Name{Local: "bpmn:" + tagOperation},
			ID:      op.ID(),
			Name:    op.Name(),
		})
	}

	return []xmlInterface{{
		XMLName:    xml.Name{Local: "bpmn:" + tagInterface},
		ID:         ifaceID,
		Name:       ifaceID,
		Operations: opsXML,
	}}
}

// nodeXML maps one model node to its BPMN element. Nodes outside the MVP
// subset yield *convert.UnsupportedElementError (SRD-051 §FR-3).
// opsByID is filled from every ServiceTask's Operation().
func nodeXML(n flow.Node, opsByID map[string]service.Operation) (*xmlNode, error) {
	xn := &xmlNode{ID: n.ID(), Name: n.Name(), Docs: docsXML(n.Docs())}

	var tag string

	switch v := n.(type) {
	case *events.StartEvent:
		tag = tagStartEvent

	case *events.EndEvent:
		tag = tagEndEvent

	case *activities.ManualTask:
		// <bpmn:task> and <bpmn:manualTask> both import as ManualTask; the
		// generic spelling is written back (SRD-051 §NFR-3).
		tag = tagTask

	case *activities.UserTask:
		tag = tagUserTask

	case *activities.ServiceTask:
		tag = tagServiceTask
		setServiceTaskAttrs(xn, v, opsByID)

	case *gateways.ExclusiveGateway:
		tag = tagExclusiveGateway
		setGatewayAttrs(xn, &v.Gateway)
		setDefaultFlow(xn, &v.Gateway)

	case *gateways.ParallelGateway:
		tag = tagParallelGateway
		// No setDefaultFlow: BPMN §13.4.1 gives the parallel gateway no
		// default attribute — it takes one token from each incoming flow
		// and puts one on each outgoing, so there is no condition to fall
		// through. The model lets any gateway carry a default flow
		// (UpdateDefaultFlow is on the shared base), so writing it for
		// every kind emitted a document no BPMN schema accepts.
		setGatewayAttrs(xn, &v.Gateway)

	default:
		return nil, &convert.UnsupportedElementError{
			Tag: fmt.Sprintf("%T", n),
			ID:  n.ID(),
		}
	}

	xn.XMLName = xml.Name{Local: "bpmn:" + tag}

	return xn, nil
}

// setServiceTaskAttrs fills implementation and, when gobpm exposes
// Operation(), operationRef plus the definitions-level catalog entry.
func setServiceTaskAttrs(
	xn *xmlNode,
	st *activities.ServiceTask,
	opsByID map[string]service.Operation,
) {
	if impl := st.Implementation(); impl != "" && impl != service.UnspecifiedImplementation {
		xn.Implementation = impl
	}

	op := st.Operation()
	if op == nil {
		return
	}

	if id := op.ID(); id != "" {
		xn.OperationRef = id
		opsByID[id] = op
	}
}

// setGatewayAttrs fills gatewayDirection, omitted when Unspecified — the
// schema default. Every gateway kind carries a direction.
func setGatewayAttrs(xn *xmlNode, g *gateways.Gateway) {
	if dir := g.Direction(); dir != gateways.Unspecified {
		xn.Direction = string(dir)
	}
}

// setDefaultFlow fills the default attribute. It is called only for the
// gateway kinds the standard gives one — the exclusive gateway here, and
// the inclusive and complex gateways when they land — never for the
// parallel gateway (BPMN §13.4.1).
func setDefaultFlow(xn *xmlNode, g *gateways.Gateway) {
	if df := g.DefaultFlow(); df != nil {
		xn.Default = df.ID()
	}
}

// flowXML maps one sequence flow, inlining its condition when present.
// Nil source/target are hard errors — never call .ID() on a nil endpoint
// (would panic and silence the real defect).
func flowXML(f *flow.SequenceFlow) (*xmlSequenceFlow, error) {
	if f == nil {
		return nil, errs.New(
			errs.M("bpmn.Export: sequenceFlow is nil"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	id := f.ID()
	src := f.Source()
	trg := f.Target()

	if err := validateFlowEndpoints(id, src, trg); err != nil {
		return nil, err
	}

	xf := &xmlSequenceFlow{
		Docs:      docsXML(f.Docs()),
		ID:        id,
		Name:      f.Name(),
		SourceRef: src.ID(),
		TargetRef: trg.ID(),
	}

	if c := f.Condition(); c != nil {
		bc, ok := c.(bodyCarrier)
		if !ok {
			// Classified failure, not a silent drop (SRD-051 open Q2 /
			// §FR-5): a compiled condition without source text cannot be
			// serialized faithfully.
			return nil, errs.New(
				errs.M("bpmn.Export: condition %q of sequenceFlow %q has no source text to export", c.ID(), f.ID()),
				errs.C(errorClass, errs.InvalidObject))
		}

		xf.Condition = &xmlCondition{
			ID:       c.ID(),
			Language: c.Language(),
			Body:     bc.Body(),
		}
	}

	xf.XMLName = xml.Name{Local: "bpmn:" + tagSequenceFlow}

	return xf, nil
}

// validateFlowEndpoints checks the endpoint invariants before flowXML
// dereferences them. Keeping this as a pure helper makes both defensive guards
// testable without constructing an impossible half-linked SequenceFlow.
func validateFlowEndpoints(
	id string,
	src flow.SequenceSource,
	trg flow.SequenceTarget,
) error {
	if src == nil {
		return errs.New(
			errs.M("bpmn.Export: sequenceFlow %q has nil source", id),
			errs.C(errorClass, errs.InvalidObject))
	}

	if trg == nil {
		return errs.New(
			errs.M("bpmn.Export: sequenceFlow %q has nil target", id),
			errs.C(errorClass, errs.InvalidObject))
	}

	return nil
}
