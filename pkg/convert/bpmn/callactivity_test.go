package bpmn

import (
	"strings"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
)

// callDoc puts one <callActivity> between a start and an end, written
// verbatim so a test can say exactly what the file said.
func callDoc(call string) string {
	return `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    ` + call + `
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="ca"/>
    <bpmn:sequenceFlow id="f2" sourceRef="ca" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`
}

// TestCallActivityKeepsItsKeyVerbatim is FR-4. The key reaches the model
// exactly as the file wrote it, and NOTHING is resolved at import: the
// callable may be registered afterwards, or re-versioned, because the
// model resolves at call time (ADR-023 §2.7).
func TestCallActivityKeepsItsKeyVerbatim(t *testing.T) {
	res, err := importEventDoc(t,
		callDoc(`<bpmn:callActivity id="ca" name="Check" calledElement="credit-check"/>`))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	n := nodeByID(t, res, "ca")

	ca, ok := n.(*activities.CallActivity)
	if !ok {
		t.Fatalf("ca is a %T, want a *activities.CallActivity", n)
	}

	if got := ca.CalledKey(); got != "credit-check" {
		t.Errorf("CalledKey() = %q, want the file's key untouched", got)
	}
}

// TestCallActivityImportsBeforeItsCallableExists is the half of §4.6 that
// is easy to lose: no registry is consulted, so nothing needs to have
// been registered. An import that failed for an unregistered callable
// would make import order significant, which is exactly what call-time
// resolution exists to avoid.
func TestCallActivityImportsBeforeItsCallableExists(t *testing.T) {
	_, err := importEventDoc(t,
		callDoc(`<bpmn:callActivity id="ca" name="Later" calledElement="not-registered-anywhere"/>`))
	if err != nil {
		t.Fatalf("Import: %v — a callable is resolved at call time, not here", err)
	}
}

// TestCallActivityNeedsAKey leaves the empty case to the model, whose
// message names the parameter.
func TestCallActivityNeedsAKey(t *testing.T) {
	_, err := importEventDoc(t,
		callDoc(`<bpmn:callActivity id="ca" name="Nameless"/>`))
	if err == nil {
		t.Fatal("a callActivity with no calledElement must be refused")
	}

	if !strings.Contains(err.Error(), "called-process key") {
		t.Errorf("error = %v, want the model's own empty-key refusal", err)
	}
}

// TestCallActivityRefusesAForeignQName covers the capability boundary:
// a prefixed calledElement names a callable in another definitions
// document, which needs the resolution seam this engine does not have
// (#325).
//
// Taking the text as a key would be worse than refusing — it would call
// whatever the host happened to register under "other:Process_1", which
// is a coincidence rather than the reference the file made.
func TestCallActivityRefusesAForeignQName(t *testing.T) {
	_, err := importEventDoc(t,
		callDoc(`<bpmn:callActivity id="ca" name="Foreign" calledElement="other:Process_1"/>`))
	if err == nil {
		t.Fatal("a prefixed calledElement must be refused")
	}

	for _, want := range []string{"another definitions document", "#325"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to mention %q", err, want)
		}
	}
}

// TestBoundaryEventOnACallActivity covers the attachment BPMN §10.5.4
// allows explicitly (conformance.md line 174).
func TestBoundaryEventOnACallActivity(t *testing.T) {
	doc := strings.Replace(
		callDoc(`<bpmn:callActivity id="ca" name="Check" calledElement="credit-check"/>`),
		`    <bpmn:endEvent id="e1"/>`,
		`    <bpmn:endEvent id="e1"/>
    <bpmn:boundaryEvent id="b1" attachedToRef="ca">
      <bpmn:timerEventDefinition id="ted">
        <bpmn:timeDuration>PT10M</bpmn:timeDuration>
      </bpmn:timerEventDefinition>
    </bpmn:boundaryEvent>`, 1)

	res, err := importEventDoc(t, doc)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	nodeByID(t, res, "b1")
}
