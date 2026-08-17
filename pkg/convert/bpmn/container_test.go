package bpmn

import (
	"errors"
	"strings"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/convert"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// subProcessDoc wraps inner as the body of a <subProcess id="sub"> that
// the process flows through, so every test here starts from a document
// the model accepts.
func subProcessDoc(inner string) string {
	return `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:camunda="http://camunda.org/schema/1.0/bpmn">
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    <bpmn:subProcess id="sub" name="Inner">
` + inner + `
    </bpmn:subProcess>
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="sub"/>
    <bpmn:sequenceFlow id="f2" sourceRef="sub" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`
}

// innerGraph is the smallest inner shape the model accepts: a None start,
// a task and an end (subprocess.go's §2.3 entry rule).
const innerGraph = `      <bpmn:startEvent id="is"/>
      <bpmn:task id="it" name="Work"/>
      <bpmn:endEvent id="ie"/>
      <bpmn:sequenceFlow id="if1" sourceRef="is" targetRef="it"/>
      <bpmn:sequenceFlow id="if2" sourceRef="it" targetRef="ie"/>`

// containerOf returns the imported sub-process by id, failing the test if
// the node is not one.
func containerOf(t *testing.T, n flow.Node) *activities.SubProcess {
	t.Helper()

	sp, ok := n.(*activities.SubProcess)
	if !ok {
		t.Fatalf("%q is a %T, want a *activities.SubProcess", n.ID(), n)
	}

	return sp
}

// idsOf returns the ids of nodes, for set comparison in a message a
// reader can act on.
func idsOf(nodes []flow.Node) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.ID())
	}

	return out
}

// TestSubProcessHoldsItsOwnGraph is FR-1: the inner elements land in the
// sub-process, and the process holds the container rather than its
// contents.
//
// The failure this guards is the flat import: with no containment, `is`,
// `it` and `ie` would sit beside `s1` in the process, and the imported
// graph would run the inner nodes as if the sub-process were not there.
func TestSubProcessHoldsItsOwnGraph(t *testing.T) {
	res, err := importEventDoc(t, subProcessDoc(innerGraph))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	proc := res.Processes[0]

	outer := idsOf(proc.Nodes())
	if len(outer) != 3 {
		t.Errorf("process holds %v, want exactly s1, sub and e1", outer)
	}

	for _, inner := range []string{"is", "it", "ie"} {
		for _, got := range outer {
			if got == inner {
				t.Errorf("%q leaked into the process — containment is the "+
					"whole point of this stage", inner)
			}
		}
	}

	sub := containerOf(t, nodeByID(t, res, "sub"))

	held := idsOf(sub.Nodes())
	if len(held) != 3 {
		t.Fatalf("sub-process holds %v, want is, it and ie", held)
	}
}

// TestInnerFlowsFollowTheirSource is FR-1 for edges: a flow between two
// inner nodes belongs to the sub-process, and the process keeps only its
// own two.
//
// Nothing in the converter puts it there. flow.Link adds a new flow to
// its SOURCE node's container, so the flow follows the node it leaves —
// which is why a flowSpec carries no container of its own.
func TestInnerFlowsFollowTheirSource(t *testing.T) {
	res, err := importEventDoc(t, subProcessDoc(innerGraph))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	proc := res.Processes[0]

	if got := len(proc.Flows()); got != 2 {
		t.Errorf("process holds %d flows, want f1 and f2 only", got)
	}

	sub := containerOf(t, nodeByID(t, res, "sub"))

	if got := len(sub.Flows()); got != 2 {
		t.Errorf("sub-process holds %d flows, want if1 and if2", got)
	}
}

// TestNestedContainers covers a container inside a container: the parser
// recurses through the same table, so depth needs no separate mechanism.
func TestNestedContainers(t *testing.T) {
	res, err := importEventDoc(t, subProcessDoc(
		`      <bpmn:subProcess id="deep" name="Deeper">
`+innerGraph+`
      </bpmn:subProcess>`))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	sub := containerOf(t, nodeByID(t, res, "sub"))

	held := idsOf(sub.Nodes())
	if len(held) != 1 || held[0] != "deep" {
		t.Fatalf("sub holds %v, want just the nested container", held)
	}

	var deep *activities.SubProcess

	for _, n := range sub.Nodes() {
		if n.ID() == "deep" {
			deep = containerOf(t, n)
		}
	}

	if got := len(deep.Nodes()); got != 3 {
		t.Errorf("the nested container holds %d nodes, want 3", got)
	}
}

// TestDuplicateIDAcrossContainers is FR-8. BPMN ids are unique per
// DOCUMENT, not per container, and the duplicate guard has to keep saying
// so now that there is more than one container to be confused about.
func TestDuplicateIDAcrossContainers(t *testing.T) {
	_, err := importEventDoc(t, subProcessDoc(
		`      <bpmn:startEvent id="is"/>
      <bpmn:task id="s1" name="Reused"/>
      <bpmn:sequenceFlow id="if1" sourceRef="is" targetRef="s1"/>`))
	if err == nil {
		t.Fatal("an id reused inside a sub-process must still be refused")
	}

	if !strings.Contains(err.Error(), "duplicate id") {
		t.Errorf("error = %v, want the duplicate-id refusal", err)
	}
}

// TestContainerIDIsClaimedBeforeItsChildren pins the ordering half of the
// guard: the container reserves its id before its body is read, so a
// child reusing the CONTAINER's id is caught rather than overwriting it.
func TestContainerIDIsClaimedBeforeItsChildren(t *testing.T) {
	_, err := importEventDoc(t, subProcessDoc(
		`      <bpmn:startEvent id="sub"/>`))
	if err == nil {
		t.Fatal("a child reusing its container's id must be refused")
	}

	if !strings.Contains(err.Error(), "duplicate id") {
		t.Errorf("error = %v, want the duplicate-id refusal", err)
	}
}

// TestImportableElementKeepsItsSectionElsewhere pins why an element that
// imports keeps its row in `sections`.
//
// The tables claim an element in a CONTEXT. <subProcess> is claimed
// inside a container and refused anywhere else, so the § is still
// reachable — and a modeler who put a sub-process somewhere it does not
// belong is exactly who needs the spec reference.
func TestImportableElementKeepsItsSectionElsewhere(t *testing.T) {
	doc := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1">
      <bpmn:subProcess id="wrong"/>
    </bpmn:startEvent>
  </bpmn:process>
</bpmn:definitions>`

	_, err := importEventDoc(t, doc)
	if err == nil {
		t.Fatal("a <subProcess> inside a start event must be refused")
	}

	var uee *convert.UnsupportedElementError
	if !errors.As(err, &uee) {
		t.Fatalf("error = %v, want an UnsupportedElementError", err)
	}

	if uee.Section != "§13.3.4" {
		t.Errorf("Section = %q, want §13.3.4 — the row survives the element "+
			"becoming importable elsewhere", uee.Section)
	}
}

// TestDialectAttributeOnAContainer is FR-8 for the report: a container is
// a node, so the funnel that reports every node's dialect attributes
// covers it without knowing what a container is.
func TestDialectAttributeOnAContainer(t *testing.T) {
	doc := strings.Replace(subProcessDoc(innerGraph),
		`<bpmn:subProcess id="sub" name="Inner">`,
		`<bpmn:subProcess id="sub" name="Inner" camunda:asyncBefore="true">`, 1)

	res, err := importEventDoc(t, doc)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	for _, d := range res.Dropped {
		if d.Element == "sub" && strings.Contains(d.Construct, "asyncBefore") {
			return
		}
	}

	t.Errorf("dropped = %v, want the container's dialect attribute reported",
		res.Dropped)
}

// TestContainerToleratesForeignChildren covers the container parser's
// skip branch, for the same reason the lane parser has one: a modeler's
// export carries its layout inside the element it draws.
func TestContainerToleratesForeignChildren(t *testing.T) {
	res, err := importEventDoc(t, subProcessDoc(
		`      <bpmndi:BPMNShape xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI" id="sh1"/>
      <bpmn:documentation>what it does</bpmn:documentation>
`+innerGraph))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	sub := containerOf(t, nodeByID(t, res, "sub"))

	if got := len(sub.Nodes()); got != 3 {
		t.Errorf("the container holds %d nodes, want its inner graph", got)
	}

	if got := len(sub.Docs()); got != 1 {
		t.Errorf("Docs() = %d, want the one the file wrote — a container's "+
			"body children still reach it", got)
	}
}

// TestTruncatedContainer covers the container parser's token-error path.
func TestTruncatedContainer(t *testing.T) {
	_, err := importEventDoc(t, `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="P" name="P">
    <bpmn:subProcess id="sub" name="Inner">
      <bpmn:startEvent id="is"/>`)
	if err == nil {
		t.Fatal("a document ending inside a container must fail the import")
	}
}

// TestContainerWithoutAnIDIsRefused covers the container parser's own
// id guard, which runs before its body is read.
func TestContainerWithoutAnIDIsRefused(t *testing.T) {
	_, err := importEventDoc(t, `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="P" name="P">
    <bpmn:subProcess name="Inner">
` + innerGraph + `
    </bpmn:subProcess>
  </bpmn:process>
</bpmn:definitions>`)
	if err == nil {
		t.Fatal("a <subProcess> with no id must be refused")
	}

	if !strings.Contains(err.Error(), "has no id") {
		t.Errorf("error = %v, want the missing-id refusal", err)
	}
}

// TestContainerReusingADeclaredIDIsRefused is the duplicate guard from the
// CONTAINER's side. The existing cases reuse an id on a node inside a
// container; this one reuses it on the container itself, which is a
// different guard in a different parser.
func TestContainerReusingADeclaredIDIsRefused(t *testing.T) {
	_, err := importEventDoc(t, `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    <bpmn:subProcess id="s1" name="Inner">
` + innerGraph + `
    </bpmn:subProcess>
  </bpmn:process>
</bpmn:definitions>`)
	if err == nil {
		t.Fatal("a container reusing a declared id must be refused")
	}

	if !strings.Contains(err.Error(), "duplicate id") {
		t.Errorf("error = %v, want the duplicate-id refusal", err)
	}
}

// TestBadLaneSetInsideAContainer covers the container child parser's
// laneSet branch failing: the same refusal a process-level lane set gets,
// reached through the container's own reader.
func TestBadLaneSetInsideAContainer(t *testing.T) {
	_, err := importEventDoc(t, subProcessDoc(
		`      <bpmn:laneSet id="ls1">
        <bpmn:lane name="Unnamed"/>
      </bpmn:laneSet>
`+innerGraph))
	if err == nil {
		t.Fatal("a <lane> with no id inside a container must be refused")
	}
}

// TestTruncatedLaneSetInsideAContainer covers the container child
// parser's laneSet branch failing while PARSING, which is a different
// path from the same lane set failing to build.
func TestTruncatedLaneSetInsideAContainer(t *testing.T) {
	_, err := importEventDoc(t, `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="P" name="P">
    <bpmn:subProcess id="sub" name="Inner">
      <bpmn:laneSet id="ls1">
        <bpmn:lane id="l1" name="Finance">`)
	if err == nil {
		t.Fatal("a document ending inside a container's <laneSet> must fail")
	}
}
