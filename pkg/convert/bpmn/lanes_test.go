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
