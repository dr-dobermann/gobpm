package bpmn

import (
	"context"
	"strings"
	"testing"

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
