package bpmn

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// branchDoc is a process whose exclusive gateway fans out to two ends —
// enough shape for both the determinism and the ordering assertions.
const branchDoc = `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1" name="start"/>
    <bpmn:userTask id="u1" name="approve"/>
    <bpmn:exclusiveGateway id="g1" default="f4"/>
    <bpmn:endEvent id="e1" name="yes"/>
    <bpmn:endEvent id="e2" name="no"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="u1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="u1" targetRef="g1"/>
    <bpmn:sequenceFlow id="f3" sourceRef="g1" targetRef="e1"/>
    <bpmn:sequenceFlow id="f4" sourceRef="g1" targetRef="e2"/>
  </bpmn:process>
</bpmn:definitions>`

// exportOnce imports doc and exports it, returning the XML.
func exportOnce(t *testing.T, doc string) string {
	t.Helper()

	p, err := importer{}.Import(context.Background(), strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	var buf bytes.Buffer
	if err := (exporter{}).Export(context.Background(), &buf, p); err != nil {
		t.Fatalf("Export: %v", err)
	}

	return buf.String()
}

// TestExportIsDeterministic covers SRD-089.A §6 T-1 (FR-3). The model
// holds nodes and flows in maps, and ranging over a map is randomized, so
// before the ordering landed two exports of ONE process differed —
// observed as two orderings in five runs of examples/bpmn-convert.
//
// Twenty runs, because the failure is probabilistic: with five elements a
// single re-export reproduces it often but not reliably.
func TestExportIsDeterministic(t *testing.T) {
	p, err := importer{}.Import(context.Background(), strings.NewReader(branchDoc))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	var first string

	for i := range 20 {
		var buf bytes.Buffer
		if err := (exporter{}).Export(context.Background(), &buf, p); err != nil {
			t.Fatalf("Export run %d: %v", i, err)
		}

		if i == 0 {
			first = buf.String()

			continue
		}

		if got := buf.String(); got != first {
			t.Fatalf("export run %d differs from run 0:\n--- run 0 ---\n%s\n--- run %d ---\n%s",
				i, first, i, got)
		}
	}
}

// TestExportOrderFollowsTheGraph pins the reading order: the walk starts
// at the start event and follows outgoing flows in flow-id order, so a
// reader meets the process the way a token travels it.
func TestExportOrderFollowsTheGraph(t *testing.T) {
	out := exportOnce(t, branchDoc)

	// f3 (→ e1) sorts before f4 (→ e2), so the gateway's branches are
	// emitted in that order on every run.
	want := []string{`id="s1"`, `id="u1"`, `id="g1"`, `id="e1"`, `id="e2"`}

	at := make([]int, len(want))

	for i, w := range want {
		at[i] = strings.Index(out, w)
		if at[i] < 0 {
			t.Fatalf("export does not contain %s:\n%s", w, out)
		}
	}

	for i := 1; i < len(at); i++ {
		if at[i] < at[i-1] {
			t.Errorf("%s precedes %s; want graph order %v\n%s",
				want[i], want[i-1], want, out)
		}
	}
}

// TestOrderNodesFallsBackToID covers the two shapes the graph walk cannot
// reach: a node no flow leads to, and a process with no start event at
// all. Neither may drop a node from the export.
func TestOrderNodesFallsBackToID(t *testing.T) {
	t.Run("an unreachable node is exported last, by id", func(t *testing.T) {
		p := importedLinear(t)

		orphan, err := activities.NewManualTask("orphan", foundation.WithID("aaa"))
		if err != nil {
			t.Fatalf("orphan: %v", err)
		}

		if err := p.Add(orphan); err != nil {
			t.Fatalf("add orphan: %v", err)
		}

		got := ids(orderNodes(p.Nodes(), p.Flows()))
		want := []string{"s1", "t1", "e1", "aaa"}

		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("orderNodes = %v, want %v — the orphan sorts first "+
				"alphabetically but is unreachable, so it trails the walk", got, want)
		}
	})

	t.Run("no start event degrades to id order", func(t *testing.T) {
		p, err := process.New("P", foundation.WithID("P"))
		if err != nil {
			t.Fatalf("process: %v", err)
		}

		for _, id := range []string{"t2", "t1"} {
			task, err := activities.NewManualTask(id, foundation.WithID(id))
			if err != nil {
				t.Fatalf("task %s: %v", id, err)
			}

			if err := p.Add(task); err != nil {
				t.Fatalf("add %s: %v", id, err)
			}
		}

		got := ids(orderNodes(p.Nodes(), p.Flows()))
		if fmt.Sprint(got) != fmt.Sprint([]string{"t1", "t2"}) {
			t.Errorf("orderNodes = %v, want [t1 t2]", got)
		}
	})

	t.Run("a single node needs no ordering", func(t *testing.T) {
		p, err := process.New("P", foundation.WithID("P"))
		if err != nil {
			t.Fatalf("process: %v", err)
		}

		only, err := activities.NewManualTask("only", foundation.WithID("only"))
		if err != nil {
			t.Fatalf("task: %v", err)
		}

		if err := p.Add(only); err != nil {
			t.Fatalf("add: %v", err)
		}

		if got := ids(orderNodes(p.Nodes(), p.Flows())); len(got) != 1 {
			t.Errorf("orderNodes = %v, want one node", got)
		}
	})
}

// TestExportParallelGatewayHasNoDefault covers SRD-089.A §6 T-4 (FR-6).
// UpdateDefaultFlow lives on the shared Gateway base, so the model lets a
// parallel gateway carry a default flow — but BPMN §13.4.1 gives the
// element no default attribute, and emitting one produced a document no
// schema accepts.
func TestExportParallelGatewayHasNoDefault(t *testing.T) {
	doc := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    <bpmn:parallelGateway id="g1"/>
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="g1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="g1" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`

	p, err := importer{}.Import(context.Background(), strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	for _, n := range p.Nodes() {
		pg, ok := n.(*gateways.ParallelGateway)
		if !ok {
			continue
		}

		for _, f := range p.Flows() {
			if f.ID() == "f2" {
				if err := pg.UpdateDefaultFlow(f); err != nil {
					t.Fatalf("UpdateDefaultFlow: %v", err)
				}
			}
		}
	}

	var buf bytes.Buffer
	if err := (exporter{}).Export(context.Background(), &buf, p); err != nil {
		t.Fatalf("Export: %v", err)
	}

	if strings.Contains(buf.String(), "default=") {
		t.Errorf("parallelGateway exported with a default attribute (BPMN §13.4.1 defines none):\n%s",
			buf.String())
	}

	// The exclusive gateway must still carry its default, so the fix is a
	// narrowing and not a removal.
	if out := exportOnce(t, branchDoc); !strings.Contains(out, `default="f4"`) {
		t.Errorf("exclusiveGateway lost its default attribute:\n%s", out)
	}
}

// importedLinear returns the s1 → t1 → e1 process, so a test may add a
// deliberately unreachable node to it.
func importedLinear(t *testing.T) *process.Process {
	t.Helper()

	p, err := importer{}.Import(context.Background(), strings.NewReader(runnableLinear))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	return p
}

// ids maps nodes to their BPMN ids for readable assertions.
func ids(nn []flow.Node) []string {
	out := make([]string, 0, len(nn))
	for _, n := range nn {
		out = append(out, n.ID())
	}

	return out
}

// TestOrderingGuardsAgainstNilFlows exercises the defensive guards in the
// ordering helpers directly, the way validateFlowEndpoints is tested: a
// nil flow or a flow with a nil endpoint cannot arise from a completed
// import, but the ordering runs BEFORE flowXML's own nil check, so a
// panic here would pre-empt that classified error with a stack trace.
func TestOrderingGuardsAgainstNilFlows(t *testing.T) {
	half := &flow.SequenceFlow{} // no endpoints

	t.Run("successors skips nil flows and half-linked ones", func(t *testing.T) {
		got := successors([]*flow.SequenceFlow{nil, half})
		if len(got) != 0 {
			t.Errorf("successors = %v, want no edges", got)
		}
	})

	t.Run("orderFlows tolerates a nil entry", func(t *testing.T) {
		got := orderFlows([]*flow.SequenceFlow{nil, half})
		if len(got) != 2 {
			t.Errorf("orderFlows returned %d flows, want 2 — nothing may be dropped", len(got))
		}
	})
}
