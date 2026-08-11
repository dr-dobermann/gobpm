package bpmn

import (
	"context"
	"strings"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// docDoc carries documentation at every level the model can hold it, plus
// a non-default textFormat, so one round-trip covers the whole feature.
const docDoc = `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="P" name="P">
    <bpmn:documentation>what the process is for</bpmn:documentation>
    <bpmn:startEvent id="s1" name="start">
      <bpmn:documentation textFormat="text/html">&lt;b&gt;go&lt;/b&gt;</bpmn:documentation>
    </bpmn:startEvent>
    <bpmn:endEvent id="e1" name="done"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="e1">
      <bpmn:documentation>the only path</bpmn:documentation>
    </bpmn:sequenceFlow>
  </bpmn:process>
</bpmn:definitions>`

// TestDocumentationRoundTrips covers SRD-089.A §6 T-5 (FR-7). Before this,
// <documentation> was skipped on import and never written on export, so a
// modeller's notes did not survive a trip through the converter in either
// direction — while foundation.Documentation existed the whole time.
func TestDocumentationRoundTrips(t *testing.T) {
	p, err := importer{}.Import(context.Background(), strings.NewReader(docDoc))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	if docs := p.Docs(); len(docs) != 1 || docs[0].Text() != "what the process is for" {
		t.Errorf("process docs = %v, want the process documentation", docs)
	}

	byID := map[string]flow.Node{}
	for _, n := range p.Nodes() {
		byID[n.ID()] = n
	}

	s1 := byID["s1"]
	if s1 == nil {
		t.Fatal("start event missing after import")
	}

	docs := s1.Docs()
	if len(docs) != 1 {
		t.Fatalf("start-event docs = %v, want one", docs)
	}

	if got := docs[0].Format(); got != "text/html" {
		t.Errorf("start-event textFormat = %q, want text/html", got)
	}

	if got := docs[0].Text(); got != "<b>go</b>" {
		t.Errorf("start-event doc text = %q, want the unescaped markup", got)
	}

	for _, f := range p.Flows() {
		if f.ID() == "f1" && len(f.Docs()) != 1 {
			t.Errorf("sequence-flow docs = %v, want one", f.Docs())
		}
	}

	out := exportOnce(t, docDoc)

	for _, want := range []string{
		"<bpmn:documentation>what the process is for</bpmn:documentation>",
		`<bpmn:documentation textFormat="text/html">`,
		"<bpmn:documentation>the only path</bpmn:documentation>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("export does not contain %s:\n%s", want, out)
		}
	}

	// text/plain is the standard's default, so an unmarked documentation
	// must not acquire a textFormat on the way out.
	if strings.Contains(out, `textFormat="text/plain"`) {
		t.Errorf("export writes the default textFormat explicitly:\n%s", out)
	}
}

// TestProcessDocumentationMustPrecedeFlowElements pins the one shape lazy
// process construction cannot serve. Documentation is inherited from
// BaseElement, whose properties serialize ahead of a Process's own
// flowElements, so a schema-valid file always presents it first — and a
// file that does not is refused rather than having its documentation
// silently dropped.
func TestProcessDocumentationMustPrecedeFlowElements(t *testing.T) {
	late := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    <bpmn:documentation>too late</bpmn:documentation>
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`

	_, err := importer{}.Import(context.Background(), strings.NewReader(late))
	if err == nil {
		t.Fatal("Import with late process documentation: want an error, not a silent drop")
	}

	if !strings.Contains(err.Error(), "after its flow elements") {
		t.Errorf("error %q does not explain the ordering requirement", err)
	}
}

// TestUnnamedElementsImport covers SRD-089.A §6 T-2 (FR-4). BPMN makes
// name 0..1 on every flow element and modelers emit unlabelled boxes
// routinely, but three of gobpm's constructors demand a non-empty name, so
// such a file was refused outright.
func TestUnnamedElementsImport(t *testing.T) {
	doc := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="P">
    <bpmn:startEvent id="s1"/>
    <bpmn:task id="t1"/>
    <bpmn:manualTask id="m1"/>
    <bpmn:userTask id="u1"/>
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="t1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t1" targetRef="m1"/>
    <bpmn:sequenceFlow id="f3" sourceRef="m1" targetRef="u1"/>
    <bpmn:sequenceFlow id="f4" sourceRef="u1" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`

	p, err := importer{}.Import(context.Background(), strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Import of unnamed elements: %v", err)
	}

	// The process itself is unnamed too, and already fell back to its id.
	if p.Name() != "P" {
		t.Errorf("process name = %q, want the id", p.Name())
	}

	for _, n := range p.Nodes() {
		switch n.ID() {
		case "t1", "m1", "u1":
			if n.Name() != n.ID() {
				t.Errorf("node %q name = %q, want the id as fallback", n.ID(), n.Name())
			}

		case "s1", "e1":
			// These constructors accept an empty name, so nothing is
			// synthesized for them — a start event must not acquire a
			// name it never had.
			if n.Name() != "" {
				t.Errorf("node %q name = %q, want it left empty", n.ID(), n.Name())
			}
		}
	}
}

// TestDocumentationPrecedesTheCondition pins the child-element order of a
// sequence flow. encoding/xml writes children in FIELD order, so the
// layout of xmlSequenceFlow is what puts documentation ahead of the
// condition — as BaseElement requires — and a reordering made to please
// govet/fieldalignment emits an out-of-order document with every test
// still green. This one is not.
func TestDocumentationPrecedesTheCondition(t *testing.T) {
	doc := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    <bpmn:exclusiveGateway id="g1"/>
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f0" sourceRef="s1" targetRef="g1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="g1" targetRef="e1">
      <bpmn:documentation>note</bpmn:documentation>
      <bpmn:conditionExpression>x &gt; 1</bpmn:conditionExpression>
    </bpmn:sequenceFlow>
  </bpmn:process>
</bpmn:definitions>`

	out := exportOnce(t, doc)

	at := strings.Index(out, "<bpmn:documentation>note")
	cond := strings.Index(out, "<bpmn:conditionExpression")

	if at < 0 || cond < 0 {
		t.Fatalf("export lost documentation or condition:\n%s", out)
	}

	if at > cond {
		t.Errorf("documentation is emitted after conditionExpression:\n%s", out)
	}
}

// TestDocumentationStreamFailures covers the error paths of the
// documentation parsers: a truncated stream inside <documentation> must
// surface as an import error at every level that reads one, not as a
// half-built process.
func TestDocumentationStreamFailures(t *testing.T) {
	cases := map[string]string{
		"truncated inside a node's documentation": `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"><bpmn:documentation>unterminated`,

		"truncated inside the process documentation": `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="P" name="P">
    <bpmn:documentation>unterminated`,

		"truncated inside a sequence flow's documentation": `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="e1">
      <bpmn:documentation>unterminated`,

		"truncated inside a foreign-namespace child of the process": `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:x="urn:x">
  <bpmn:process id="P" name="P">
    <x:anything><x:inner>`,
	}

	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			p, err := importer{}.Import(context.Background(), strings.NewReader(doc))
			if err == nil {
				t.Fatalf("Import of a truncated document returned a process: %v", p)
			}
		})
	}
}

// TestDocsXMLSkipsNilEntries exercises the export-side guard directly, the
// way the ordering helpers' guards are tested: Docs() is built by the
// model and should never hold a nil, but rendering runs before any
// endpoint check could catch one, so a panic here would pre-empt every
// classified error the exporter has.
func TestDocsXMLSkipsNilEntries(t *testing.T) {
	if got := docsXML([]*foundation.Documentation{nil}); len(got) != 0 {
		t.Errorf("docsXML = %v, want nothing rendered for a nil entry", got)
	}

	if got := docsXML(nil); got != nil {
		t.Errorf("docsXML(nil) = %v, want nil", got)
	}
}
