package bpmn

import (
	"strings"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/artifacts"
)

// artifactDoc is a supported flow graph plus whatever process-level
// artifact declarations a test injects, with an optional definitions-level
// tail (a <category>, for the group tests).
func artifactDoc(processArts, defsTail string) string {
	return `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    <bpmn:task id="t1" name="work"/>
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="t1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t1" targetRef="e1"/>
` + processArts + `
  </bpmn:process>
` + defsTail + `
</bpmn:definitions>`
}

// TestAnnotationImports covers SRD-092 FR-7 for <textAnnotation>: text and
// format carried, the standard's defaults applied where the document is
// silent.
func TestAnnotationImports(t *testing.T) {
	res, err := importEventDoc(t, artifactDoc(
		`    <bpmn:textAnnotation id="note" textFormat="text/xhtml">
      <bpmn:text>Careful</bpmn:text>
    </bpmn:textAnnotation>
    <bpmn:textAnnotation id="bare"/>`, ""))
	if err != nil {
		t.Fatalf("import of an annotated file: %v", err)
	}

	arts := res.Processes[0].Artifacts()
	if len(arts) != 2 {
		t.Fatalf("artifacts = %d, want 2", len(arts))
	}

	ta, ok := arts[0].(*artifacts.TextAnnotation)
	if !ok {
		t.Fatalf("artifact 0 is a %T, want *TextAnnotation", arts[0])
	}

	if ta.ID() != "note" || ta.Text() != "Careful" ||
		ta.TextFormat() != "text/xhtml" {
		t.Errorf("annotation = %q/%q/%q, want note/Careful/text/xhtml",
			ta.ID(), ta.Text(), ta.TextFormat())
	}

	bare, ok := arts[1].(*artifacts.TextAnnotation)
	if !ok {
		t.Fatalf("artifact 1 is a %T, want *TextAnnotation", arts[1])
	}

	if bare.Text() != "" || bare.TextFormat() != "text/plain" {
		t.Errorf("bare annotation = %q/%q, want empty text and the "+
			"standard's text/plain default", bare.Text(), bare.TextFormat())
	}
}

// TestGroupImports covers SRD-092 FR-8 (T-8): a group embeds the value its
// categoryValueRef resolves to; one without a ref gets the model's
// placeholder; the <category> itself becomes no artifact.
func TestGroupImports(t *testing.T) {
	res, err := importEventDoc(t, artifactDoc(
		`    <bpmn:group id="g1" categoryValueRef="cv1"/>
    <bpmn:group id="g2"/>`,
		`  <bpmn:category id="c1" name="Ops">
    <bpmn:categoryValue id="cv1" value="urgent"/>
  </bpmn:category>`))
	if err != nil {
		t.Fatalf("import of a grouped file: %v", err)
	}

	arts := res.Processes[0].Artifacts()
	if len(arts) != 2 {
		t.Fatalf("artifacts = %d, want 2 — a category is resolution input, "+
			"not an artifact", len(arts))
	}

	g1, ok := arts[0].(*artifacts.Group)
	if !ok {
		t.Fatalf("artifact 0 is a %T, want *Group", arts[0])
	}

	if g1.ID() != "g1" || g1.CategoryValue.Value != "urgent" {
		t.Errorf("group = %q/%q, want g1 embedding \"urgent\"",
			g1.ID(), g1.CategoryValue.Value)
	}

	g2, ok := arts[1].(*artifacts.Group)
	if !ok {
		t.Fatalf("artifact 1 is a %T, want *Group", arts[1])
	}

	if g2.CategoryValue.Value != "UNDEFINED_CATEGORY_VALUE" {
		t.Errorf("ref-less group value = %q, want the model's placeholder",
			g2.CategoryValue.Value)
	}
}

// TestGroupUnresolvableRefIsReported covers SRD-092 FR-10's group half: a
// categoryValueRef naming nothing the document declares drops THAT group
// with a report — the file imports.
func TestGroupUnresolvableRefIsReported(t *testing.T) {
	res, err := importEventDoc(t, artifactDoc(
		`    <bpmn:group id="g1" categoryValueRef="missing"/>`, ""))
	if err != nil {
		t.Fatalf("a dangling categoryValueRef must not refuse the file: %v",
			err)
	}

	if got := len(res.Processes[0].Artifacts()); got != 0 {
		t.Errorf("artifacts = %d, want 0 — the broken group is dropped", got)
	}

	if len(res.Dropped) != 1 {
		t.Fatalf("Dropped = %+v, want exactly the group's entry", res.Dropped)
	}

	d := res.Dropped[0]
	if d.Element != "g1" || d.Construct != tagGroup ||
		!strings.Contains(d.Reason, `"missing"`) {
		t.Errorf("report = %+v, want it naming g1, group and the "+
			"unresolved ref", d)
	}
}

// TestArtifactsInsideSubProcess covers the carrier half of SRD-092 T-7:
// artifacts declared inside a <subProcess> land on the sub-process's own
// collection, not the process's.
func TestArtifactsInsideSubProcess(t *testing.T) {
	res, err := importEventDoc(t, subProcessDoc(innerGraph+`
      <bpmn:textAnnotation id="in-note">
        <bpmn:text>inner</bpmn:text>
      </bpmn:textAnnotation>
      <bpmn:group id="in-grp"/>`))
	if err != nil {
		t.Fatalf("import of an annotated sub-process: %v", err)
	}

	if got := len(res.Processes[0].Artifacts()); got != 0 {
		t.Errorf("process artifacts = %d, want 0", got)
	}

	sp := containerOf(t, nodeByID(t, res, "sub"))

	arts := sp.Artifacts()
	if len(arts) != 2 {
		t.Fatalf("sub-process artifacts = %d, want 2", len(arts))
	}

	if arts[0].ID() != "in-note" || arts[1].ID() != "in-grp" {
		t.Errorf("sub-process artifacts = %q/%q, want in-note/in-grp",
			arts[0].ID(), arts[1].ID())
	}
}

// TestMultiProcessCategoryResolution covers SRD-092 T-12: the category
// lookup is the DOCUMENT's, so a <category> declared after both processes
// resolves for groups in each.
func TestMultiProcessCategoryResolution(t *testing.T) {
	doc := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="P1" name="P1">
    <bpmn:startEvent id="p1s"/>
    <bpmn:endEvent id="p1e"/>
    <bpmn:sequenceFlow id="p1f" sourceRef="p1s" targetRef="p1e"/>
    <bpmn:group id="p1g" categoryValueRef="cv1"/>
  </bpmn:process>
  <bpmn:process id="P2" name="P2">
    <bpmn:startEvent id="p2s"/>
    <bpmn:endEvent id="p2e"/>
    <bpmn:sequenceFlow id="p2f" sourceRef="p2s" targetRef="p2e"/>
    <bpmn:group id="p2g" categoryValueRef="cv1"/>
  </bpmn:process>
  <bpmn:category id="c1">
    <bpmn:categoryValue id="cv1" value="shared"/>
  </bpmn:category>
</bpmn:definitions>`

	res, err := importEventDoc(t, doc)
	if err != nil {
		t.Fatalf("import of a two-process grouped document: %v", err)
	}

	for i, p := range res.Processes {
		arts := p.Artifacts()
		if len(arts) != 1 {
			t.Fatalf("process %d artifacts = %d, want 1", i, len(arts))
		}

		g, ok := arts[0].(*artifacts.Group)
		if !ok || g.CategoryValue.Value != "shared" {
			t.Errorf("process %d group = %T/%v, want a *Group embedding "+
				"\"shared\"", i, arts[0], arts[0])
		}
	}
}

// TestArtifactsWithoutDeclaredIDs: an artifact's id is 0..1 — one that
// declares none is carried anyway, under a model-generated id.
func TestArtifactsWithoutDeclaredIDs(t *testing.T) {
	res, err := importEventDoc(t, artifactDoc(
		`    <bpmn:textAnnotation><bpmn:text>anon</bpmn:text></bpmn:textAnnotation>
    <bpmn:group/>`, ""))
	if err != nil {
		t.Fatalf("import of id-less artifacts: %v", err)
	}

	arts := res.Processes[0].Artifacts()
	if len(arts) != 2 {
		t.Fatalf("artifacts = %d, want 2", len(arts))
	}

	for i, a := range arts {
		if a.ID() == "" {
			t.Errorf("artifact %d has an empty id, want a generated one", i)
		}
	}
}

// TestAnnotationNonTextChildrenAreSwallowed: an annotation may carry
// children other than <text> (extensionElements, foreign namespaces); they
// are swallowed and the text is still read.
func TestAnnotationNonTextChildrenAreSwallowed(t *testing.T) {
	res, err := importEventDoc(t, artifactDoc(
		`    <bpmn:textAnnotation id="note">
      <bpmn:extensionElements><x:foo xmlns:x="urn:x"/></bpmn:extensionElements>
      <bpmn:text>Careful</bpmn:text>
    </bpmn:textAnnotation>`, ""))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	ta, ok := res.Processes[0].Artifacts()[0].(*artifacts.TextAnnotation)
	if !ok || ta.Text() != "Careful" {
		t.Errorf("annotation = %v, want its text read past the extension",
			res.Processes[0].Artifacts()[0])
	}
}

// TestCategoryOddShapes: a category's non-value children are swallowed,
// and a <categoryValue> without an id is unreferencable — read and left
// unrecorded, refusing nothing.
func TestCategoryOddShapes(t *testing.T) {
	res, err := importEventDoc(t, artifactDoc(
		`    <bpmn:group id="g1" categoryValueRef="cv1"/>`,
		`  <bpmn:category id="c1">
    <bpmn:documentation>ops taxonomy</bpmn:documentation>
    <bpmn:categoryValue value="anonymous"/>
    <bpmn:categoryValue id="cv1" value="urgent"/>
  </bpmn:category>`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	g, ok := res.Processes[0].Artifacts()[0].(*artifacts.Group)
	if !ok || g.CategoryValue.Value != "urgent" {
		t.Errorf("group = %v, want it resolving cv1 past the odd children",
			res.Processes[0].Artifacts()[0])
	}
}

// TestAnnotationIDJoinsTheLedger: a declared artifact id lives in the
// document's one id ledger (SRD-089.F §4.11), so a collision with a flow
// node is refused like any other duplicate.
func TestAnnotationIDJoinsTheLedger(t *testing.T) {
	_, err := importEventDoc(t, artifactDoc(
		`    <bpmn:textAnnotation id="t1"/>`, ""))
	if err == nil {
		t.Fatal("an annotation reusing a task's id must be refused")
	}

	if !strings.Contains(err.Error(), `"t1"`) {
		t.Errorf("err = %v, want it naming the duplicated id", err)
	}
}
