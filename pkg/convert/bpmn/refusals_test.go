package bpmn

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/convert"
)

// TestGlobalTaskFamilyIsRefusedAsADeferral covers SRD-089.C §FR-5.
//
// The distinction from an ordinary refusal is the point: a global task is
// waiting on the server tier's definition registry, so the file is fine
// and the reader should wait — where an unsupported element means the
// converter does not map that shape at all.
func TestGlobalTaskFamilyIsRefusedAsADeferral(t *testing.T) {
	family := []string{
		"globalTask", "globalUserTask", "globalManualTask",
		"globalScriptTask", "globalBusinessRuleTask",
	}

	for _, tag := range family {
		t.Run(tag, func(t *testing.T) {
			doc := fmt.Sprintf(`<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="%s">
  <bpmn:%s id="g1" name="Reusable"/>
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`, nsBPMN, tag)

			_, err := importer{}.Import(context.Background(), strings.NewReader(doc))
			if err == nil {
				t.Fatalf("<%s> imported; it must be refused", tag)
			}

			if !strings.Contains(err.Error(), "not supported yet") {
				t.Errorf("refusal %q does not say the support is pending", err)
			}

			// NOT an UnsupportedElementError: a host branching on that type
			// to mean "this shape is not mapped" would draw the wrong
			// conclusion about a file that is perfectly fine.
			var uee *convert.UnsupportedElementError
			if errors.As(err, &uee) {
				t.Errorf("<%s> refused as UnsupportedElementError; a deferral is "+
					"a different claim about the file", tag)
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

// TestTheThreeRefusalsAreDistinguishable is the invariant NFR-3 asks for:
// a host must be able to tell "wait", "rewrite" and "check the roadmap"
// apart, because they lead to three different actions.
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
		"waiting on a subsystem": {
			doc("", `<bpmn:globalTask id="g" name="R"/>`), false, "not supported yet",
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
