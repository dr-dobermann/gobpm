package bpmn

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"time"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/expression/lite"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// condDoc is a two-branch process whose conditional flow carries whatever
// language the test is exercising. docLang goes on <definitions>, condLang
// on the expression — either may be empty.
func condDoc(docLang, condLang string) string {
	docAttr := ""
	if docLang != "" {
		docAttr = fmt.Sprintf(" expressionLanguage=%q", docLang)
	}

	condAttr := ""
	if condLang != "" {
		condAttr = fmt.Sprintf(" language=%q", condLang)
	}

	return fmt.Sprintf(`<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="%s"%s>
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    <bpmn:exclusiveGateway id="g1" default="f3"/>
    <bpmn:endEvent id="e1"/>
    <bpmn:endEvent id="e2"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="g1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="g1" targetRef="e1">
      <bpmn:conditionExpression id="c1"%s>total &gt; 100</bpmn:conditionExpression>
    </bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="f3" sourceRef="g1" targetRef="e2"/>
  </bpmn:process>
</bpmn:definitions>`, nsBPMN, docAttr, condAttr)
}

// TestExpressionLanguageResolution covers SRD-089.B §FR-1: the expression's
// own language wins, then the document's default, and a language the
// converter cannot evaluate is refused rather than carried inert.
//
// Carrying it inert is what the converter did before, and it is the defect:
// the file imports, the process registers, and the first decision that
// needs the condition faults — far from the import that admitted it.
func TestExpressionLanguageResolution(t *testing.T) {
	tests := map[string]struct {
		docLang, condLang string
		wantRefused       string // substring the refusal must name; "" = imports
	}{
		"the expression declares lite":                 {"", lite.Language, ""},
		"the document declares lite":                   {lite.Language, "", ""},
		"the expression overrides the document":        {nsXPath, lite.Language, ""},
		"the document cannot rescue a refused express": {lite.Language, nsXPath, nsXPath},
		"explicit XPath is refused":                    {"", nsXPath, nsXPath},
		"FEEL is refused, named":                       {"", "feel", "feel"},
		"an unknown language is refused, named":        {"", "urn:acme:expr", "urn:acme:expr"},
		"nothing declared anywhere is refused":         {"", "", "none declared"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := importer{}.Import(context.Background(),
				strings.NewReader(condDoc(tc.docLang, tc.condLang)))

			if tc.wantRefused == "" {
				if err != nil {
					t.Fatalf("Import: %v", err)
				}

				return
			}

			if err == nil {
				t.Fatal("Import: want a refusal, got a process")
			}

			if !strings.Contains(err.Error(), tc.wantRefused) {
				t.Errorf("refusal %q does not name %q", err, tc.wantRefused)
			}

			// It must also say what IS supported — a refusal that does not
			// tell the modeler what to write is half an answer.
			if !strings.Contains(err.Error(), lite.Language) {
				t.Errorf("refusal %q does not name the supported language", err)
			}

			var ae *errs.ApplicationError
			if !errors.As(err, &ae) || !ae.HasClass(errorClass) {
				t.Errorf("refusal is %T, want a converter ApplicationError", err)
			}
		})
	}
}

// TestLiteConditionIsTheModelsTextKind pins WHAT a lite condition becomes:
// the model's text-expression, which the routed engine evaluates — not the
// converter's own carrier, which never could.
func TestLiteConditionIsTheModelsTextKind(t *testing.T) {
	p, err := importer{}.Import(context.Background(),
		strings.NewReader(condDoc("", lite.Language)))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	var found bool

	for _, f := range p.Flows() {
		if f.ID() != "f2" {
			continue
		}

		found = true

		te, ok := f.Condition().(*data.TextExpression)
		if !ok {
			t.Fatalf("condition is %T, want *data.TextExpression", f.Condition())
		}

		if te.Language() != lite.Language || te.Body() != "total > 100" {
			t.Errorf("condition = %q/%q, want %s/total > 100",
				te.Language(), te.Body(), lite.Language)
		}

		if te.ID() != "c1" {
			t.Errorf("condition id = %q, want the document's c1", te.ID())
		}

		// A sequence-flow condition is boolean by definition (BPMN §13.2).
		if te.ResultType() != typeBool {
			t.Errorf("condition result type = %q, want %q", te.ResultType(), typeBool)
		}
	}

	if !found {
		t.Fatal("flow f2 missing after import")
	}
}

// TestConditionIDDefaultsToTheFlow covers the id fallback: an expression
// with no id of its own is still addressable.
func TestConditionIDDefaultsToTheFlow(t *testing.T) {
	doc := strings.Replace(condDoc("", lite.Language), ` id="c1"`, "", 1)

	p, err := importer{}.Import(context.Background(), strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	for _, f := range p.Flows() {
		if f.ID() == "f2" && f.Condition().ID() != "f2:condition" {
			t.Errorf("condition id = %q, want %q", f.Condition().ID(), "f2:condition")
		}
	}
}

// runnableCond is a process whose exclusive gateway must CHOOSE: one
// conditional flow carrying cond, and a default.
//
// Two flows, not one, and that detail is load-bearing. With a single
// outgoing flow the gateway has no decision to make and never consults the
// condition at all — measured, not assumed: the first version of this test
// used one flow, and a condition over an undefined datum completed cleanly
// through it.
func runnableCond(cond string) string {
	return fmt.Sprintf(`<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="%s">
  <bpmn:process id="cond-run" name="cond-run" isExecutable="true">
    <bpmn:startEvent id="s1"/>
    <bpmn:exclusiveGateway id="g1" default="f3"/>
    <bpmn:endEvent id="e1"/>
    <bpmn:endEvent id="e2"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="g1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="g1" targetRef="e1">
      <bpmn:conditionExpression language="%s">%s</bpmn:conditionExpression>
    </bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="f3" sourceRef="g1" targetRef="e2"/>
  </bpmn:process>
</bpmn:definitions>`, nsBPMN, lite.Language, cond)
}

// TestImportedConditionIsRunnable is SRD-089.B §9.3 — the milestone's whole
// point, demonstrated rather than restated.
//
// Before this stage every imported condition was the converter's own
// carrier, whose Evaluate returned an error unconditionally. A file
// imported, registered and ran until the first decision, then faulted with
// "converter does not evaluate expressions" — a defect the import had no
// way to warn about. Both cases below would have failed identically then;
// they differ now, which is the evidence.
func TestImportedConditionIsRunnable(t *testing.T) {
	if err := data.CreateDefaultStates(); err != nil {
		t.Fatalf("CreateDefaultStates: %v", err)
	}

	run := func(t *testing.T, cond string, wait time.Duration) (thresher.InstanceState, error) {
		t.Helper()

		ctx, cancel := context.WithTimeout(context.Background(), wait)
		defer cancel()

		p, err := importer{}.Import(ctx, strings.NewReader(runnableCond(cond)))
		if err != nil {
			t.Fatalf("Import: %v", err)
		}

		// No WithExpressionEngine: lite is one of the engines a thresher
		// registers by default (pkg/thresher/thresher.go:286), so an
		// imported lite condition is runnable with no host wiring at all.
		th, err := thresher.New("imported-condition-" + t.Name())
		if err != nil {
			t.Fatalf("thresher.New: %v", err)
		}

		if _, err := th.RegisterProcess(p); err != nil {
			t.Fatalf("RegisterProcess: %v", err)
		}

		if err := th.Run(ctx); err != nil {
			t.Fatalf("Run: %v", err)
		}

		h, err := th.StartLatest(p.ID())
		if err != nil {
			t.Fatalf("StartLatest: %v", err)
		}

		return h.WaitCompletion(ctx)
	}

	t.Run("a true condition routes the token", func(t *testing.T) {
		state, err := run(t, "1 &gt; 0", 5*time.Second)
		if err != nil {
			t.Fatalf("WaitCompletion: %v", err)
		}

		if state != thresher.StateCompleted {
			t.Errorf("state = %q, want %q — a true condition must route",
				state, thresher.StateCompleted)
		}
	})

	t.Run("the engine really evaluates it", func(t *testing.T) {
		// The discriminator. "ghost" is not a datum this process has, so
		// lite fails to read it — but ONLY if something evaluates the
		// expression at all. A converter that still handed the engine an
		// inert condition, or an engine that never consulted it, would
		// complete here exactly as the true case does.
		// A short wait: the point is that it does NOT complete. Evaluating
		// "ghost" raises an incident and parks the instance, so the wait
		// expires — which is the observation, not a flake.
		state, err := run(t, "ghost &gt; 1", 2*time.Second)

		if err == nil {
			t.Fatalf("state = %q with no error — a condition over an undefined "+
				"datum must not resolve cleanly; nothing evaluated it", state)
		}

		if state == thresher.StateCompleted {
			t.Errorf("state = %q — the instance completed despite an "+
				"unevaluable condition", state)
		}
	})
}
