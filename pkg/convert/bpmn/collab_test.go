package bpmn

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// twoProcDoc wraps two runnable processes, decls between them at the
// definitions level.
func twoProcDoc(decls, p1Attrs, p2Attrs string) string {
	return `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:xsd="http://www.w3.org/2001/XMLSchema">
` + decls + `
  <bpmn:process id="P1" name="first"` + p1Attrs + `>
    <bpmn:startEvent id="a1"/>
    <bpmn:endEvent id="a2"/>
    <bpmn:sequenceFlow id="af" sourceRef="a1" targetRef="a2"/>
  </bpmn:process>
  <bpmn:process id="P2" name="second"` + p2Attrs + `>
    <bpmn:startEvent id="b1"/>
    <bpmn:endEvent id="b2"/>
    <bpmn:sequenceFlow id="bf" sourceRef="b1" targetRef="b2"/>
  </bpmn:process>
</bpmn:definitions>`
}

// TestTwoProcessesImportAndRun is SRD-089.I T-1 (FR-1): the document's
// set, in document order, and both register and run on one engine.
func TestTwoProcessesImportAndRun(t *testing.T) {
	ctx := context.Background()

	res, err := importEventDoc(t, twoProcDoc("", "", ""))
	if err != nil {
		t.Fatalf("ImportDocument: %v", err)
	}

	if len(res.Processes) != 2 || res.Processes[0].ID() != "P1" ||
		res.Processes[1].ID() != "P2" {
		t.Fatalf("Processes = %v, want P1 then P2, document order",
			res.Processes)
	}

	engine, err := thresher.New("two-proc-engine")
	if err != nil {
		t.Fatalf("thresher.New: %v", err)
	}

	for _, proc := range res.Processes {
		if _, err := engine.RegisterProcess(proc); err != nil {
			t.Fatalf("RegisterProcess(%s): %v", proc.ID(), err)
		}
	}

	if err := engine.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, proc := range res.Processes {
		h, err := engine.StartLatest(proc.ID())
		if err != nil {
			t.Fatalf("StartLatest(%s): %v", proc.ID(), err)
		}

		if _, err := h.WaitCompletion(ctx); err != nil {
			t.Fatalf("WaitCompletion(%s): %v", proc.ID(), err)
		}
	}
}

// TestProcessWorldsDoNotBleed is T-2: each process's elements build in
// its own assembly; the ledger still refuses a cross-process duplicate.
func TestProcessWorldsDoNotBleed(t *testing.T) {
	res, err := importEventDoc(t, `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:xsd="http://www.w3.org/2001/XMLSchema">
  <bpmn:itemDefinition id="idOrder" structureRef="xsd:string"/>
  <bpmn:process id="P1" name="first">
    <bpmn:dataObject id="do1" name="order" itemSubjectRef="idOrder"/>
    <bpmn:startEvent id="a1"/>
  </bpmn:process>
  <bpmn:process id="P2" name="second">
    <bpmn:startEvent id="b1"/>
  </bpmn:process>
</bpmn:definitions>`)
	if err != nil {
		t.Fatalf("ImportDocument: %v", err)
	}

	if n := len(res.Processes[0].DataObjects()); n != 1 {
		t.Errorf("P1 data objects = %d, want its own 1", n)
	}

	if n := len(res.Processes[1].DataObjects()); n != 0 {
		t.Errorf("P2 data objects = %d, want none — no bleed", n)
	}

	_, err = importEventDoc(t, twoProcDoc("", "", ""))
	if err != nil {
		t.Fatalf("clean two-proc doc: %v", err)
	}

	_, err = importEventDoc(t, strings.Replace(
		twoProcDoc("", "", ""), `id="b1"`, `id="a1"`, 1))
	if err == nil || !strings.Contains(err.Error(), "duplicate id") {
		t.Fatalf("error = %v, want the document-wide ledger refusal", err)
	}

	// FR-5: a sequence flow reaching into ANOTHER process's assembly
	// refuses — resolution is per-assembly, so P1's node is not there.
	_, err = importEventDoc(t, strings.Replace(
		twoProcDoc("", "", ""), `targetRef="b2"`, `targetRef="a2"`, 1))
	if err == nil || !strings.Contains(err.Error(), "a2") {
		t.Fatalf("error = %v, want the cross-process flow refused naming a2",
			err)
	}
}

// TestImportSelectionRule is T-3/T-4/T-5 (§4.2): the singular entry's
// three rows.
func TestImportSelectionRule(t *testing.T) {
	importOne := func(doc string) (string, error) {
		p, err := importer{}.Import(context.Background(), strings.NewReader(doc))
		if err != nil {
			return "", err
		}

		return p.ID(), nil
	}

	t.Run("one process, no flag", func(t *testing.T) {
		id, err := importOne(propDoc("", ""))
		if err != nil || id != "P" {
			t.Fatalf("Import = %q/%v, want the one process, flag or none",
				id, err)
		}
	})

	t.Run("two, one executable", func(t *testing.T) {
		id, err := importOne(twoProcDoc("", "", ` isExecutable="true"`))
		if err != nil || id != "P2" {
			t.Fatalf("Import = %q/%v, want the one marked executable", id, err)
		}
	})

	t.Run("two, none executable", func(t *testing.T) {
		_, err := importOne(twoProcDoc("", "", ""))
		if err == nil || !strings.Contains(err.Error(), "use ImportDocument") ||
			!strings.Contains(err.Error(), "2 processes, 0 marked") {
			t.Fatalf("error = %v, want the counts and the pointer", err)
		}
	})

	t.Run("two, both executable", func(t *testing.T) {
		_, err := importOne(twoProcDoc("",
			` isExecutable="true"`, ` isExecutable="true"`))
		if err == nil || !strings.Contains(err.Error(), "2 marked") {
			t.Fatalf("error = %v, want the ambiguous-count refusal", err)
		}
	})
}

// collabDoc wraps a collaboration beside the two processes.
func collabDoc(collab string) string {
	return twoProcDoc(`  <bpmn:message id="m1" name="orderPlaced"/>
  <bpmn:collaboration id="c1">
`+collab+`
  </bpmn:collaboration>`, ` isExecutable="true"`, "")
}

// TestCollaborationIsConsumed is T-7 (FR-3): participants validated and
// consumed, nothing reported for them, nothing built.
func TestCollaborationIsConsumed(t *testing.T) {
	res, err := importEventDoc(t, collabDoc(
		`    <bpmn:participant id="pa1" name="Sales" processRef="P1"/>
    <bpmn:participant id="pa2" name="Fulfilment" processRef="P2"/>
    <bpmn:participant id="pa3" name="Customer"/>`))
	if err != nil {
		t.Fatalf("ImportDocument: %v — a black-box pool (pa3) is legal", err)
	}

	if len(res.Processes) != 2 {
		t.Fatalf("Processes = %d, want the two the participants name",
			len(res.Processes))
	}

	if len(res.Dropped) != 0 {
		t.Errorf("Dropped = %v, want nothing for a flow-less collaboration",
			res.Dropped)
	}
}

// TestParticipantRefErrors is T-8: a present processRef must resolve.
func TestParticipantRefErrors(t *testing.T) {
	tests := map[string]struct {
		ref  string
		want string
	}{
		"dangling":   {ref: "ghost", want: "no such element is declared"},
		"wrong kind": {ref: "m1", want: `"m1" is a message`},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := importEventDoc(t, collabDoc(
				`    <bpmn:participant id="pa1" processRef="`+tc.ref+`"/>`))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

// TestMessageFlowIsReported is T-9 (FR-4, §4.4): one Dropped entry, the
// mechanism named, the graph untouched.
func TestMessageFlowIsReported(t *testing.T) {
	res, err := importEventDoc(t, collabDoc(
		`    <bpmn:participant id="pa1" processRef="P1"/>
    <bpmn:participant id="pa2" processRef="P2"/>
    <bpmn:messageFlow id="mf1" sourceRef="a1" targetRef="b1"
                      messageRef="m1"/>`))
	if err != nil {
		t.Fatalf("ImportDocument: %v", err)
	}

	if len(res.Dropped) != 1 {
		t.Fatalf("Dropped = %v, want exactly the flow", res.Dropped)
	}

	d := res.Dropped[0]
	if d.Element != "mf1" || d.Construct != tagMessageFlow {
		t.Errorf("Dropped = %+v, want mf1's messageFlow entry", d)
	}

	for _, want := range []string{"message name", "correlation"} {
		if !strings.Contains(d.Reason, want) {
			t.Errorf("Reason = %q, want the mechanism named (%q)", d.Reason, want)
		}
	}
}

// TestMessageFlowRefErrors is T-10: a dangling messageRef refuses — the
// report must not launder a broken reference.
func TestMessageFlowRefErrors(t *testing.T) {
	tests := map[string]struct {
		ref  string
		want string
	}{
		"dangling":   {ref: "ghost", want: "no such element is declared"},
		"wrong kind": {ref: "P1", want: `"P1" is a process`},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := importEventDoc(t, collabDoc(
				`    <bpmn:messageFlow id="mf1" sourceRef="a1" targetRef="b1"
                      messageRef="`+tc.ref+`"/>`))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

// TestCollabFamilyIDsJoinTheLedger is T-11.
func TestCollabFamilyIDsJoinTheLedger(t *testing.T) {
	_, err := importEventDoc(t, collabDoc(
		`    <bpmn:participant id="P1" processRef="P1"/>`))
	if err == nil || !strings.Contains(err.Error(), "duplicate id") {
		t.Fatalf("error = %v, want the ledger's refusal", err)
	}
}

// TestParticipantOutsideACollaboration is T-12 (§4.5): still refused,
// still no § — the extract pins none (#334).
func TestParticipantOutsideACollaboration(t *testing.T) {
	_, err := importEventDoc(t, propDoc("",
		`    <bpmn:participant id="pa1" processRef="P"/>`))
	if err == nil || !strings.Contains(err.Error(), `unsupported element "participant"`) {
		t.Fatalf("error = %v, want the plain refusal", err)
	}

	if strings.Contains(err.Error(), "§") {
		t.Errorf("error = %v carries a §; the extract pins none (#334)", err)
	}
}

// TestCollabStrangersAndTolerance is T-13: documentation skipped by
// declaration, foreign namespaces skipped, in-namespace strangers
// settled.
func TestCollabStrangersAndTolerance(t *testing.T) {
	t.Run("documentation and foreign child", func(t *testing.T) {
		_, err := importEventDoc(t, collabDoc(
			`    <bpmn:documentation>who talks to whom</bpmn:documentation>
    <x:pool xmlns:x="http://x"/>
    <bpmn:participant id="pa1" processRef="P1"/>`))
		if err != nil {
			t.Fatalf("import: %v", err)
		}
	})

	t.Run("stranger inside", func(t *testing.T) {
		_, err := importEventDoc(t, collabDoc(
			`    <bpmn:task id="t9"/>`))
		if err == nil || !strings.Contains(err.Error(), `unsupported element "task"`) {
			t.Fatalf("error = %v, want the stranger refused", err)
		}
	})

	t.Run("truncated", func(t *testing.T) {
		_, err := importEventDoc(t, `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:collaboration id="c1"><bpmn:participant id="pa1"`)
		if err == nil {
			t.Fatal("a truncated collaboration must fail")
		}
	})
}

// TestTwoPoolsMessageOnAThresher is T-14, the stage's DoD run: one
// document, two pools, the engine's own mechanism. The producer's end
// event throws the message; the consumer's message start event — with
// no incoming flow — is registered for auto-instantiation, so the
// throw spawns it. The collaboration and its messageFlow are consumed
// and reported; the exchange itself needs neither.
func TestTwoPoolsMessageOnAThresher(t *testing.T) {
	doc := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:message id="m1" name="orderPlaced"/>
  <bpmn:collaboration id="c1">
    <bpmn:participant id="pa1" name="Sales" processRef="P1"/>
    <bpmn:participant id="pa2" name="Fulfilment" processRef="P2"/>
    <bpmn:messageFlow id="mf1" sourceRef="send1" targetRef="start2"
                      messageRef="m1"/>
  </bpmn:collaboration>
  <bpmn:process id="P1" name="sales" isExecutable="true">
    <bpmn:startEvent id="s1"/>
    <bpmn:endEvent id="send1">
      <bpmn:messageEventDefinition id="md1" messageRef="m1"/>
    </bpmn:endEvent>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="send1"/>
  </bpmn:process>
  <bpmn:process id="P2" name="fulfilment">
    <bpmn:startEvent id="start2">
      <bpmn:messageEventDefinition id="md2" messageRef="m1"/>
    </bpmn:startEvent>
    <bpmn:endEvent id="e2"/>
    <bpmn:sequenceFlow id="f2" sourceRef="start2" targetRef="e2"/>
  </bpmn:process>
</bpmn:definitions>`

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := importer{}.ImportDocument(ctx, strings.NewReader(doc))
	if err != nil {
		t.Fatalf("ImportDocument: %v", err)
	}

	if len(res.Dropped) != 1 || res.Dropped[0].Element != "mf1" {
		t.Fatalf("Dropped = %v, want the messageFlow report alone", res.Dropped)
	}

	engine, err := thresher.New("two-pools-engine")
	if err != nil {
		t.Fatalf("thresher.New: %v", err)
	}

	// The consumer first, so its instance-starter is armed before the
	// producer throws.
	if _, err := engine.RegisterProcess(res.Processes[1]); err != nil {
		t.Fatalf("RegisterProcess(consumer): %v", err)
	}

	if _, err := engine.RegisterProcess(res.Processes[0]); err != nil {
		t.Fatalf("RegisterProcess(producer): %v", err)
	}

	if err := engine.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	h, err := engine.StartLatest(res.Processes[0].ID())
	if err != nil {
		t.Fatalf("StartLatest(producer): %v", err)
	}

	if _, err := h.WaitCompletion(ctx); err != nil {
		t.Fatalf("producer WaitCompletion: %v", err)
	}

	// The consumer's instance is spawned BY the message, so it is
	// observed through discovery — and PER PROCESS: a raw settled count
	// of 2 would also be satisfied by a producer that spawned twice with
	// the consumer never instantiated.
	settledOf := func(processID string) int {
		ids, err := engine.Instances(thresher.InstanceQuery{
			ProcessID: processID, Stage: thresher.StageSettled})
		if err != nil {
			t.Fatalf("Instances(%s): %v", processID, err)
		}

		return len(ids)
	}

	for {
		p1, p2 := settledOf("P1"), settledOf("P2")

		if p1 == 1 && p2 == 1 {
			return
		}

		select {
		case <-ctx.Done():
			t.Fatalf("timed out with %d settled P1 and %d settled P2 "+
				"instances, want exactly one of each; the throw never "+
				"instantiated the consumer", p1, p2)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// TestDocumentReportsFireOnce is T-6 (§4.3): the document-level reports
// — a store obligation, an unused import — arrive once for a
// two-process document, not once per process.
func TestDocumentReportsFireOnce(t *testing.T) {
	res, err := importEventDoc(t, twoProcDoc(
		`  <bpmn:dataStore id="S1" name="Store"/>
  <bpmn:import importType="x" location="a.xsd" namespace="http://unused"/>`,
		"", ""))
	if err != nil {
		t.Fatalf("ImportDocument: %v", err)
	}

	counts := map[string]int{}
	for _, d := range res.Dropped {
		counts[d.Construct]++
	}

	if counts[tagDataStore] != 1 || counts[tagImport] != 1 {
		t.Fatalf("Dropped = %v, want each document report exactly once",
			res.Dropped)
	}
}
