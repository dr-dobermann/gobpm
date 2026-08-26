package bpmn

import (
	"strings"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/lanes"
)

// laneDoc puts laneSets at the head of a process holding a start, a task
// and an end — the order BPMN serializes a container in.
func laneDoc(laneSets string) string {
	return `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="P" name="P">
` + laneSets + `
    <bpmn:startEvent id="s1"/>
    <bpmn:task id="t1" name="Work"/>
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="t1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t1" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`
}

// laneNamed finds a lane by name anywhere in the sets, including nested
// ones.
func laneNamed(sets []*lanes.LaneSet, name string) *lanes.Lane {
	for _, s := range sets {
		for _, l := range s.Lanes() {
			if l.Name() == name {
				return l
			}

			if c := l.ChildLaneSet(); c != nil {
				if got := laneNamed([]*lanes.LaneSet{c}, name); got != nil {
					return got
				}
			}
		}
	}

	return nil
}

// TestLanesImportAndPlaceTheirNodes is FR-5: the partition is carried,
// and <flowNodeRef> puts the named node on its lane.
func TestLanesImportAndPlaceTheirNodes(t *testing.T) {
	res, err := importEventDoc(t, laneDoc(
		`    <bpmn:laneSet id="ls1" name="Roles">
      <bpmn:lane id="l1" name="Finance">
        <bpmn:flowNodeRef>t1</bpmn:flowNodeRef>
      </bpmn:lane>
      <bpmn:lane id="l2" name="Ops">
        <bpmn:flowNodeRef>s1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>e1</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>`))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	sets := res.Processes[0].LaneSets()
	if len(sets) != 1 {
		t.Fatalf("LaneSets() = %d, want the one the file declared", len(sets))
	}

	if got := sets[0].Name(); got != "Roles" {
		t.Errorf("lane set name = %q, want Roles", got)
	}

	fin := laneNamed(sets, "Finance")
	if fin == nil {
		t.Fatal("the Finance lane is missing")
	}

	held := idsOf(fin.FlowNodes())
	if len(held) != 1 || held[0] != "t1" {
		t.Errorf("Finance holds %v, want just t1", held)
	}

	if got := len(laneNamed(sets, "Ops").FlowNodes()); got != 2 {
		t.Errorf("Ops holds %d nodes, want s1 and e1", got)
	}
}

// TestLanesCarryNoBehaviour is the other half of "model-only"
// (conformance.md line 173): the graph runs exactly as if no lane had
// been drawn, so the nodes and flows are untouched by the partition.
func TestLanesCarryNoBehaviour(t *testing.T) {
	withLanes, err := importEventDoc(t, laneDoc(
		`    <bpmn:laneSet id="ls1" name="Roles">
      <bpmn:lane id="l1" name="Finance">
        <bpmn:flowNodeRef>t1</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>`))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	without, err := importEventDoc(t, laneDoc(""))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	a, b := withLanes.Processes[0], without.Processes[0]

	if len(a.Nodes()) != len(b.Nodes()) || len(a.Flows()) != len(b.Flows()) {
		t.Errorf("the lane changed the graph: %d/%d nodes, %d/%d flows",
			len(a.Nodes()), len(b.Nodes()), len(a.Flows()), len(b.Flows()))
	}
}

// TestNestedLaneSet covers <childLaneSet>.
func TestNestedLaneSet(t *testing.T) {
	res, err := importEventDoc(t, laneDoc(
		`    <bpmn:laneSet id="ls1" name="Outer">
      <bpmn:lane id="l1" name="Department">
        <bpmn:childLaneSet id="ls2" name="Inner">
          <bpmn:lane id="l2" name="Team">
            <bpmn:flowNodeRef>t1</bpmn:flowNodeRef>
          </bpmn:lane>
        </bpmn:childLaneSet>
      </bpmn:lane>
    </bpmn:laneSet>`))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	sets := res.Processes[0].LaneSets()

	team := laneNamed(sets, "Team")
	if team == nil {
		t.Fatal("the nested Team lane is missing")
	}

	if got := idsOf(team.FlowNodes()); len(got) != 1 || got[0] != "t1" {
		t.Errorf("Team holds %v, want t1 — a nested lane places nodes too", got)
	}
}

// TestLaneSetOnASubProcess is FR-5's second container: a sub-process
// holds its own partition, and the process does not acquire it.
func TestLaneSetOnASubProcess(t *testing.T) {
	res, err := importEventDoc(t, subProcessDoc(
		`      <bpmn:laneSet id="ls1" name="Inner roles">
        <bpmn:lane id="l1" name="Clerk">
          <bpmn:flowNodeRef>it</bpmn:flowNodeRef>
        </bpmn:lane>
      </bpmn:laneSet>`+"\n"+innerGraph))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	if got := len(res.Processes[0].LaneSets()); got != 0 {
		t.Errorf("the process took %d lane sets, want none — they are the "+
			"sub-process's", got)
	}

	sub := containerOf(t, nodeByID(t, res, "sub"))

	sets := sub.LaneSets()
	if len(sets) != 1 {
		t.Fatalf("the sub-process holds %d lane sets, want 1", len(sets))
	}

	if got := idsOf(laneNamed(sets, "Clerk").FlowNodes()); len(got) != 1 ||
		got[0] != "it" {
		t.Errorf("Clerk holds %v, want the inner task", got)
	}
}

// TestFlowNodeRefNamingNothing is the refusal the converter must own: the
// model's Place takes nodes, so an id resolving to nothing would simply
// not be placed and the lane would come out quietly smaller than the file
// drew.
func TestFlowNodeRefNamingNothing(t *testing.T) {
	_, err := importEventDoc(t, laneDoc(
		`    <bpmn:laneSet id="ls1" name="Roles">
      <bpmn:lane id="l1" name="Finance">
        <bpmn:flowNodeRef>ghost</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>`))
	if err == nil {
		t.Fatal("a flowNodeRef naming no node must be refused")
	}

	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error = %v, want it to name the id that resolved to nothing",
			err)
	}
}

// TestLanePlacingANodeFromAnotherContainer is the refusal the MODEL owns.
// The node exists, so the converter places it; ValidateLaneSets is what
// knows the container it landed in is not the lane's.
func TestLanePlacingANodeFromAnotherContainer(t *testing.T) {
	doc := strings.Replace(subProcessDoc(innerGraph),
		`  <bpmn:process id="P" name="P">`,
		`  <bpmn:process id="P" name="P">
    <bpmn:laneSet id="ls1" name="Roles">
      <bpmn:lane id="l1" name="Finance">
        <bpmn:flowNodeRef>it</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>`, 1)

	_, err := importEventDoc(t, doc)
	if err == nil {
		t.Fatal("a lane placing a node from another container must be refused")
	}

	if !strings.Contains(err.Error(), "isn't in the container") {
		t.Errorf("error = %v, want the model's own container-membership "+
			"message", err)
	}
}

// TestLaneSetAfterFlowElements refuses what cannot be applied: a lane set
// reaches the process as a construction option, so one arriving after the
// process was built could only be dropped.
func TestLaneSetAfterFlowElements(t *testing.T) {
	doc := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    <bpmn:laneSet id="ls1" name="Late">
      <bpmn:lane id="l1" name="Finance"/>
    </bpmn:laneSet>
  </bpmn:process>
</bpmn:definitions>`

	_, err := importEventDoc(t, doc)
	if err == nil {
		t.Fatal("a laneSet after the flow elements must be refused, not dropped")
	}

	if !strings.Contains(err.Error(), "precede") {
		t.Errorf("error = %v, want the ordering refusal", err)
	}
}

// TestLaneSetTolerBeratesForeignAndUnknownChildren covers the skip
// branches of the lane parsers.
//
// A modeler's export puts diagram-interchange elements everywhere, and a
// lane set is no exception. Refusing a file for carrying its own layout
// next to a lane would reject documents whose flow graph is entirely
// supported — the same argument that made the visual artifacts skipped.
func TestLaneSetToleratesForeignAndUnknownChildren(t *testing.T) {
	res, err := importEventDoc(t, laneDoc(
		`    <bpmn:laneSet id="ls1" name="Roles">
      <bpmndi:BPMNShape xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI" id="sh1"/>
      <bpmn:lane id="l1" name="Finance">
        <bpmn:documentation>who pays</bpmn:documentation>
        <bpmndi:BPMNLabel xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI" id="lb1"/>
        <bpmn:flowNodeRef>t1</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>`))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	sets := res.Processes[0].LaneSets()

	if got := idsOf(laneNamed(sets, "Finance").FlowNodes()); len(got) != 1 {
		t.Errorf("Finance holds %v, want t1 — the foreign children are noise "+
			"around the one child that matters", got)
	}
}

// TestEmptyFlowNodeRefIsNotAPlacement covers the blank-text branch: an
// empty ref names no node, and treating "" as an id would fail the import
// with a message about an element the file never mentioned.
func TestEmptyFlowNodeRefIsNotAPlacement(t *testing.T) {
	res, err := importEventDoc(t, laneDoc(
		`    <bpmn:laneSet id="ls1" name="Roles">
      <bpmn:lane id="l1" name="Finance">
        <bpmn:flowNodeRef>  </bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>`))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	if got := laneNamed(res.Processes[0].LaneSets(), "Finance").FlowNodes(); len(got) != 0 {
		t.Errorf("Finance holds %v, want nothing", idsOf(got))
	}
}

// TestTruncatedLaneSubtrees covers the token-error paths: a document that
// ends inside a lane set, a lane, or a flowNodeRef must fail rather than
// return a half-read partition.
func TestTruncatedLaneSubtrees(t *testing.T) {
	const head = `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="P" name="P">
    <bpmn:laneSet id="ls1" name="Roles">`

	for name, doc := range map[string]string{
		"inside the lane set": head,
		"inside a lane":       head + `<bpmn:lane id="l1" name="Finance">`,
		"inside a flowNodeRef": head +
			`<bpmn:lane id="l1"><bpmn:flowNodeRef>t1`,
		"inside a child lane set": head +
			`<bpmn:lane id="l1"><bpmn:childLaneSet id="ls2">`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := importEventDoc(t, doc); err == nil {
				t.Fatal("a truncated lane subtree must fail the import")
			}
		})
	}
}

// TestLaneIDJoinsTheLedger: a declared lane or lane-set id lives in the
// document's one ledger (SRD-089.F §4.11) — before SRD-092 M5, a lane
// could silently reuse a task's id.
func TestLaneIDJoinsTheLedger(t *testing.T) {
	_, err := importEventDoc(t, laneDoc(
		`    <bpmn:laneSet id="ls1">
      <bpmn:lane id="t1" name="Finance"/>
    </bpmn:laneSet>`))
	if err == nil {
		t.Fatal("a lane reusing a task's id must be refused")
	}

	if !strings.Contains(err.Error(), `"t1"`) {
		t.Errorf("error = %v, want it naming the duplicated id", err)
	}
}

// TestTruncatedForeignChildInALaneSet covers the skip branch's own error
// path: the document ends inside the subtree being stepped over.
func TestTruncatedForeignChildInALaneSet(t *testing.T) {
	_, err := importEventDoc(t, `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI">
  <bpmn:process id="P" name="P">
    <bpmn:laneSet id="ls1">
      <bpmndi:BPMNShape id="sh1"><bpmndi:Bounds`)
	if err == nil {
		t.Fatal("a document ending inside a skipped child must fail the import")
	}
}

// TestLaneWithoutIDImports: a lane's (and a lane set's) id is 0..1, and
// one without it used to refuse the file — buildLaneSet applied WithID("")
// unconditionally, and an empty explicit id is an error (SRD-092 M5).
// SRD-089.E pinned that refusal on the premise that "the model does not"
// make ids optional; it does — an id-less element is carried under a
// generated one (foundation.NewBaseElement), the same convention the
// artifacts use, and it is the SRD-076 FR-2 cardinality argument one
// attribute over. Unreferencable, it joins no lookup. The nested set
// exercises the recursion.
func TestLaneWithoutIDImports(t *testing.T) {
	res, err := importEventDoc(t, laneDoc(`    <bpmn:laneSet>
      <bpmn:lane name="sales">
        <bpmn:flowNodeRef>t1</bpmn:flowNodeRef>
        <bpmn:childLaneSet>
          <bpmn:lane name="inside"/>
        </bpmn:childLaneSet>
      </bpmn:lane>
    </bpmn:laneSet>`))
	if err != nil {
		t.Fatalf("an id-less lane must import: %v", err)
	}

	sets := res.Processes[0].LaneSets()
	if len(sets) != 1 || len(sets[0].Lanes()) != 1 {
		t.Fatalf("lane sets = %v, want one set with one lane", sets)
	}

	lane := sets[0].Lanes()[0]
	if lane.Name() != "sales" || lane.ID() == "" {
		t.Errorf("lane = %q/%q, want sales under a generated id",
			lane.Name(), lane.ID())
	}

	if lane.ChildLaneSet() == nil ||
		len(lane.ChildLaneSet().Lanes()) != 1 {
		t.Error("the id-less nested lane set must be carried too")
	}
}
