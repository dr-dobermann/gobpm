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

// callActivityOf imports doc and returns its single call activity, failing
// the test on anything that is not a clean import of one.
func callActivityOf(t *testing.T, doc string) *activities.CallActivity {
	t.Helper()

	res, err := importEventDoc(t, doc)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	n := nodeByID(t, res, "ca")

	ca, ok := n.(*activities.CallActivity)
	if !ok {
		t.Fatalf("ca is a %T, want a *activities.CallActivity", n)
	}

	return ca
}

// qnameDoc builds a document whose <definitions> carries targetNamespace and
// the prefixes the case needs, so a calledElement can be written qualified.
func qnameDoc(nsAttrs, imports, call string) string {
	return `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
    ` + nsAttrs + `>
` + imports + `
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    ` + call + `
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="ca"/>
    <bpmn:sequenceFlow id="f2" sourceRef="ca" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`
}

// TestCalledElementIsReadAsAQName covers SRD-096 T-9 through T-12: the four
// dispositions of ADR-024 v.7 §2.13.
//
// The converter resolves the PREFIX and never the callable — which
// registration a foreign namespace maps onto is the host's decision, through
// the engine's resolver. So the assertions here are about what the node
// CARRIES, not about anything being looked up.
func TestCalledElementIsReadAsAQName(t *testing.T) {
	const (
		own    = "http://example.com/orders"
		shared = "http://example.com/shared"
	)

	nsAttrs := `xmlns:tns="` + own + `" xmlns:ext="` + shared +
		`" targetNamespace="` + own + `"`
	imp := `  <bpmn:import namespace="` + shared + `" location="shared.bpmn"` +
		` importType="http://www.omg.org/spec/BPMN/20100524/MODEL"/>`

	t.Run("unprefixed is the key, verbatim", func(t *testing.T) {
		ca := callActivityOf(t, callDoc(
			`<bpmn:callActivity id="ca" name="C" calledElement="approve"/>`))

		if got := ca.CalledKey(); got != "approve" {
			t.Errorf("CalledKey() = %q, want %q", got, "approve")
		}

		if ns := ca.CalledNamespace(); ns != "" {
			t.Errorf("CalledNamespace() = %q, want empty — nothing qualified "+
				"this reference", ns)
		}
	})

	t.Run("the document's own namespace collapses", func(t *testing.T) {
		ca := callActivityOf(t, qnameDoc(nsAttrs, imp,
			`<bpmn:callActivity id="ca" name="C" calledElement="tns:approve"/>`))

		if got := ca.CalledKey(); got != "approve" {
			t.Errorf("CalledKey() = %q, want the local part %q", got, "approve")
		}

		if ns := ca.CalledNamespace(); ns != "" {
			t.Errorf("CalledNamespace() = %q, want empty — a document "+
				"qualifying its OWN definitions has said nothing, and "+
				"carrying it would send the host resolving a self-reference",
				ns)
		}
	})

	t.Run("an imported namespace rides with the call", func(t *testing.T) {
		ca := callActivityOf(t, qnameDoc(nsAttrs, imp,
			`<bpmn:callActivity id="ca" name="C" calledElement="ext:audit"/>`))

		if got := ca.CalledKey(); got != "audit" {
			t.Errorf("CalledKey() = %q, want the local part %q", got, "audit")
		}

		if ns := ca.CalledNamespace(); ns != shared {
			t.Errorf("CalledNamespace() = %q, want %q — the host's resolver "+
				"needs the pair to map it", ns, shared)
		}
	})

	t.Run("an undeclared prefix is refused", func(t *testing.T) {
		_, err := importEventDoc(t, qnameDoc(nsAttrs, imp,
			`<bpmn:callActivity id="ca" name="C" calledElement="ghost:audit"/>`))
		if err == nil {
			t.Fatal("a prefix no xmlns binds must be refused: the file is " +
				"malformed, and guessing would call a coincidence")
		}

		for _, want := range []string{"ghost", "no xmlns declaration binds"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error = %v, want it to mention %q", err, want)
			}
		}
	})

	t.Run("a declared prefix with no <import> is refused", func(t *testing.T) {
		_, err := importEventDoc(t, qnameDoc(nsAttrs, "",
			`<bpmn:callActivity id="ca" name="C" calledElement="ext:audit"/>`))
		if err == nil {
			t.Fatal("a namespace no <import> declares must be refused: the " +
				"file references a document it never imported")
		}

		for _, want := range []string{shared, "no <import> declares"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error = %v, want it to mention %q", err, want)
			}
		}
	})
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

// TestCallActivityCarriesItsParameters is SRD-096 M5a: an imported call
// activity declares what it passes.
//
// §10.4.1's containment list names only Tasks and CallableElements, and read
// strictly that would exclude a Call Activity — but §10.4's own CallActivity
// row says its "DataInputs / DataOutputs are mapped to corresponding elements
// in the CallableElement without any explicit DataAssociation", which
// presupposes it has them. Under the strict reading that row is unreachable
// and no imported document can hand data to a callable at all, so gobpm reads
// "Tasks" as the activities that do work.
func TestCallActivityCarriesItsParameters(t *testing.T) {
	ca := callActivityOf(t, callDoc(
		`<bpmn:callActivity id="ca" name="C" calledElement="rate">
      <bpmn:ioSpecification id="ca.io">
        <bpmn:dataInput id="ca.in" name="amount"/>
        <bpmn:dataOutput id="ca.out" name="total"/>
        <bpmn:inputSet id="ca.is">
          <bpmn:dataInputRefs>ca.in</bpmn:dataInputRefs>
        </bpmn:inputSet>
        <bpmn:outputSet id="ca.os">
          <bpmn:dataOutputRefs>ca.out</bpmn:dataOutputRefs>
        </bpmn:outputSet>
      </bpmn:ioSpecification>
    </bpmn:callActivity>`))

	// CallInputs/CallOutputs are what the instance loop hands the invoker:
	// the names it binds into the child's root scope, and the names it reads
	// back. They are the direct mapping §10.4 describes.
	ins, outs := ca.CallInputs(), ca.CallOutputs()

	if len(ins) != 1 || ins[0] != "amount" {
		t.Errorf("CallInputs() = %v, want [amount] — the name the callable's "+
			"own contract declares", ins)
	}

	if len(outs) != 1 || outs[0] != "total" {
		t.Errorf("CallOutputs() = %v, want [total]", outs)
	}
}
