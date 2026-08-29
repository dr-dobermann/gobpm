package bpmn

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/convert"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// TestGlobalTaskFamilyImports replaces the deferral SRD-089.C §FR-5 pinned.
//
// That refusal said the family was waiting on a registry of callable
// definitions. The registry it was waiting for turned out to be the process
// registry, which has always existed — ADR-023 v.5 §2.7 decides a global task
// IS a callable process — so each member now imports as one (SRD-096 FR-6),
// and the file that used to be refused is read whole.
func TestGlobalTaskFamilyImports(t *testing.T) {
	for _, tag := range []string{
		"globalTask", "globalManualTask", "globalUserTask",
	} {
		t.Run(tag, func(t *testing.T) {
			doc := fmt.Sprintf(`<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="%s">
  <bpmn:%s id="g1" name="Reusable"/>
  <bpmn:process id="P" name="P" isExecutable="true">
    <bpmn:startEvent id="s1"/>
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`, nsBPMN, tag)

			res, err := importer{}.ImportDocument(
				context.Background(), strings.NewReader(doc))
			if err != nil {
				t.Fatalf("ImportDocument: %v — the family imports now", err)
			}

			if len(res.Processes) != 2 {
				t.Fatalf("Processes = %d, want 2: the declared one and the "+
					"callable the global task became", len(res.Processes))
			}

			// In DOCUMENT order, which is what Result.Processes promises:
			// this fixture declares the global task first.
			if got := res.Processes[0].ID(); got != "g1" {
				t.Errorf("Processes[0] = %q, want g1 — the set is in "+
					"document order, and the callable is declared first", got)
			}

			// The callable is registered under the global task's OWN id, which
			// is the key a callActivity names it by.
			var callable *process.Process

			for _, pr := range res.Processes {
				if pr.ID() == "g1" {
					callable = pr
				}
			}

			if callable == nil {
				t.Fatalf("no process with id g1; got %v",
					[]string{res.Processes[0].ID(), res.Processes[1].ID()})
			}

			if n := len(callable.Nodes()); n != 3 {
				t.Errorf("the callable holds %d nodes, want 3 — a None start, "+
					"the task, a None end (§13.3.4 enters a called process by "+
					"its None start)", n)
			}
		})
	}
}

// TestComplexGatewayIsRefusedAsInexpressible covers §FR-4 — the one
// element the engine executes that a document cannot reach.
func TestComplexGatewayIsRefusedAsInexpressible(t *testing.T) {
	withCondition := `<bpmn:complexGateway id="g1">
      <bpmn:activationCondition>2</bpmn:activationCondition>
    </bpmn:complexGateway>`

	for name, gw := range map[string]string{
		"with an activationCondition": withCondition,
		"without one":                 `<bpmn:complexGateway id="g1"/>`,
	} {
		t.Run(name, func(t *testing.T) {
			doc := fmt.Sprintf(`<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="%s">
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    %s
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="g1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="g1" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`, nsBPMN, gw)

			_, err := importer{}.Import(context.Background(), strings.NewReader(doc))
			if err == nil {
				t.Fatal("<complexGateway> imported; §4.1 says it cannot be")
			}

			// The reason must name BOTH forms and the way out. "Unsupported"
			// alone would invite someone to add it, and they would find the
			// two forms do not correspond.
			for _, want := range []string{
				"activationCondition",
				"token counts",
				"WithActivationThreshold",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal %q does not mention %q", err, want)
				}
			}

			var uee *convert.UnsupportedElementError
			if errors.As(err, &uee) {
				t.Error("refused as UnsupportedElementError; this is not a shape " +
					"awaiting a later slice")
			}
		})
	}
}

// TestTheThreeRefusalsAreDistinguishable is the invariant NFR-3 asks for: a
// host must be able to tell the refusal kinds apart, because they lead to
// different actions.
//
// "Wait" is no longer one of them — SRD-096 retired the notYet disposition
// with the last refusal that used it — so what remains is "this shape is not
// mapped", "these two forms do not correspond" and "the engine will not take
// a Go value from a document".
func TestTheThreeRefusalsAreDistinguishable(t *testing.T) {
	doc := func(inner, defs string) string {
		return fmt.Sprintf(`<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="%s">%s
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>%s
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`, nsBPMN, defs, inner)
	}

	cases := map[string]struct {
		doc    string
		uee    bool
		phrase string
	}{
		"not in the subset": {
			doc(`<bpmn:participant id="part"/>`, ""), true, "unsupported element",
		},
		"forms do not correspond": {
			doc(`<bpmn:complexGateway id="cg"/>`, ""), false, "cannot be imported",
		},
		"a Go value no document carries": {
			doc(`<bpmn:adHocSubProcess id="sub"/>`, ""), false, "cannot be imported",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := importer{}.Import(context.Background(), strings.NewReader(tc.doc))
			if err == nil {
				t.Fatal("Import: want a refusal")
			}

			var uee *convert.UnsupportedElementError
			if got := errors.As(err, &uee); got != tc.uee {
				t.Errorf("UnsupportedElementError = %v, want %v", got, tc.uee)
			}

			if !strings.Contains(err.Error(), tc.phrase) {
				t.Errorf("refusal %q does not carry %q", err, tc.phrase)
			}
		})
	}
}

// TestNotExpressibleFallsBackToATruthfulReason covers the generic arm: an
// element marked inexpressible with no reason recorded still says
// something true rather than nothing.
func TestNotExpressibleFallsBackToATruthfulReason(t *testing.T) {
	err := notExpressibleHere(bpmnStartElement("someElement", "x"))

	if !strings.Contains(err.Error(), "do not correspond") {
		t.Errorf("fallback reason %q says nothing useful", err)
	}
}
