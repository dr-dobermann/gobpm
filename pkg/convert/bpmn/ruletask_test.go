package bpmn

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
)

// ruleDoc is a linear process whose one task is the rule task under test.
func ruleDoc(attrs string) string {
	return fmt.Sprintf(`<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="%s" xmlns:camunda="%s">
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    <bpmn:businessRuleTask id="t1" name="Decide" %s/>
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="t1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t1" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`, nsBPMN, nsCamunda, attrs)
}

// ruleTaskOf imports doc and returns its rule task.
func ruleTaskOf(t *testing.T, doc string) *activities.BusinessRuleTask {
	t.Helper()

	p, err := importer{}.Import(context.Background(), strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	for _, n := range p.Nodes() {
		if rt, ok := n.(*activities.BusinessRuleTask); ok {
			return rt
		}
	}

	t.Fatal("no business rule task after import")

	return nil
}

// TestImportBusinessRuleTask covers SRD-089.C §FR-2. The reference is
// carried OPAQUELY: no DMN is parsed here, and whether the decision
// exists is the host rule engine's question, not the converter's.
func TestImportBusinessRuleTask(t *testing.T) {
	t.Run("from the dialect's decisionRef", func(t *testing.T) {
		rt := ruleTaskOf(t, ruleDoc(`camunda:decisionRef="discount"`))

		if got := rt.DecisionRef(); got != "discount" {
			t.Errorf("DecisionRef() = %q, want discount", got)
		}
	})

	t.Run("from implementation, the standard-shaped fallback", func(t *testing.T) {
		// BPMN gives the element only `implementation`, so a file that
		// never met Camunda has nowhere else to put a decision name.
		rt := ruleTaskOf(t, ruleDoc(`implementation="pricing-table"`))

		if got := rt.DecisionRef(); got != "pricing-table" {
			t.Errorf("DecisionRef() = %q, want pricing-table", got)
		}
	})

	t.Run("the dialect wins over implementation", func(t *testing.T) {
		rt := ruleTaskOf(t, ruleDoc(
			`camunda:decisionRef="discount" implementation="##DMN"`))

		if got := rt.DecisionRef(); got != "discount" {
			t.Errorf("DecisionRef() = %q, want the dialect's discount", got)
		}
	})
}

// TestBusinessRuleTaskNeedsADecision covers the refusal — including the
// case that looks like a reference and is not.
func TestBusinessRuleTaskNeedsADecision(t *testing.T) {
	tests := map[string]string{
		"nothing at all":                    "",
		"an unspecified mechanism":          `implementation="##unspecified"`,
		"a named mechanism, not a decision": `implementation="##DMN"`,
		"a blank dialect reference":         `camunda:decisionRef="   "`,
	}

	for name, attrs := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := importer{}.Import(context.Background(),
				strings.NewReader(ruleDoc(attrs)))
			if err == nil {
				t.Fatal("Import: a rule task with no decision must be refused")
			}

			for _, want := range []string{`"t1"`, "names no decision"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal %q does not mention %q", err, want)
				}
			}
		})
	}
}

// TestDecisionRefIsNotReportedAsDropped pins the invariant the whole
// report rests on: a construct is either mapped or reported, never both
// and never neither. decisionRef is consumed here, so it must not also
// appear as something the converter failed to map.
func TestDecisionRefIsNotReportedAsDropped(t *testing.T) {
	_, report := reportOf(t, ruleDoc(`camunda:decisionRef="discount"`))

	if d, reported := report["camunda:decisionRef"]; reported {
		t.Errorf("decisionRef reported as unmapped (%q) — it was consumed",
			d.Reason)
	}
}
