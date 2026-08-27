package bpmn

import (
	"strings"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
)

// variantDoc builds a process holding one container element, written
// verbatim so a test can say exactly what the file said.
func variantDoc(container string) string {
	return variantDocWith(container, innerGraph)
}

// variantDocWith is variantDoc with the container's body spelled out, for
// the tests whose subject is what is inside it.
func variantDocWith(container, inner string) string {
	return `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    ` + container + `
` + inner + `
    </` + strings.SplitN(strings.TrimPrefix(container, "<"), " ", 2)[0] + `>
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="sub"/>
    <bpmn:sequenceFlow id="f2" sourceRef="sub" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`
}

// TestTransactionImportsAsTheVariant is FR-3: <transaction> is a
// sub-process carrying one more option, not a node of its own.
func TestTransactionImportsAsTheVariant(t *testing.T) {
	res, err := importEventDoc(t, variantDoc(`<bpmn:transaction id="sub" name="Charge">`))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	sub := containerOf(t, nodeByID(t, res, "sub"))

	if !sub.IsTransaction() {
		t.Error("IsTransaction() = false for an imported <transaction>")
	}

	if sub.IsEventSubProcess() {
		t.Error("IsEventSubProcess() = true for a <transaction>")
	}

	if got := len(sub.Nodes()); got != 3 {
		t.Errorf("the transaction holds %d nodes, want its inner graph", got)
	}
}

// TestEventSubProcessImportsAsTheVariant is FR-2.
func TestEventSubProcessImportsAsTheVariant(t *testing.T) {
	doc := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:escalation id="esc" name="Overdue"/>
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    <bpmn:task id="t1" name="Work"/>
    <bpmn:endEvent id="e1"/>
    <bpmn:subProcess id="sub" name="Handler" triggeredByEvent="true">
      <bpmn:startEvent id="is">
        <bpmn:escalationEventDefinition id="ed" escalationRef="esc"/>
      </bpmn:startEvent>
      <bpmn:endEvent id="ie"/>
      <bpmn:sequenceFlow id="if1" sourceRef="is" targetRef="ie"/>
    </bpmn:subProcess>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="t1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t1" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`

	res, err := importEventDoc(t, doc)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	sub := containerOf(t, nodeByID(t, res, "sub"))

	if !sub.IsEventSubProcess() {
		t.Error("IsEventSubProcess() = false for triggeredByEvent=\"true\"")
	}

	if sub.IsTransaction() {
		t.Error("IsTransaction() = true for an event sub-process")
	}
}

// TestTriggeredByEventReadsXSDBoolean covers the other spelling: XML
// writes a boolean "true" or "1", and modeler exports use both.
func TestTriggeredByEventReadsXSDBoolean(t *testing.T) {
	for _, v := range []string{"true", "1"} {
		t.Run(v, func(t *testing.T) {
			doc := strings.Replace(subProcessDoc(innerGraph),
				`<bpmn:subProcess id="sub" name="Inner">`,
				`<bpmn:subProcess id="sub" name="Inner" triggeredByEvent="`+v+`">`, 1)

			// An event sub-process takes no incoming flow, so this document
			// is refused — by the MODEL, which is the point. What matters
			// here is that the attribute was read at all.
			_, err := importEventDoc(t, doc)
			if err == nil {
				t.Fatal("an event sub-process with an incoming flow must be refused")
			}
		})
	}
}

// TestTransactionAndEventSubProcessIsTheModelsRefusal is T-7: the
// converter passes both options and the model names the conflict. A
// converter-side check would be a second copy of ADR-028 §2.6.
func TestTransactionAndEventSubProcessIsTheModelsRefusal(t *testing.T) {
	_, err := importEventDoc(t,
		variantDoc(`<bpmn:transaction id="sub" name="Both" triggeredByEvent="true">`))
	if err == nil {
		t.Fatal("a transaction marked triggeredByEvent must be refused")
	}

	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error = %v, want the model's mutual-exclusion message", err)
	}
}

// TestTransactionMethodDispositions is SRD-093 T-6: the absent attribute
// and both standard spellings read as compensate, every other identifier is
// carried verbatim for registration to judge, and nothing is reported —
// the converter keeps no value table of its own (ADR-024 §2.16).
func TestTransactionMethodDispositions(t *testing.T) {
	tests := map[string]struct {
		attr string
		want activities.TransactionMethod
	}{
		"absent means compensate": {"", activities.TransactionCompensate},
		"metamodel compensate": {
			` method="compensate"`, activities.TransactionCompensate,
		},
		"schema ##Compensate": {
			` method="##Compensate"`, activities.TransactionCompensate,
		},
		"store carried":           {` method="store"`, "store"},
		"image carried":           {` method="image"`, "image"},
		"schema ##Store carried":  {` method="##Store"`, "##Store"},
		"a URI carried":           {` method="urn:acme:saga"`, "urn:acme:saga"},
		"a non-BPMN word carried": {` method="rollback"`, "rollback"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			res, err := importEventDoc(t,
				variantDoc(`<bpmn:transaction id="sub" name="Charge"`+tc.attr+`>`))
			if err != nil {
				t.Fatalf("Import: %v", err)
			}

			sub := containerOf(t, nodeByID(t, res, "sub"))

			tx := sub.Transaction()
			if tx == nil {
				t.Fatal("Transaction() = nil for an imported <transaction>")
			}

			if tx.Method() != tc.want {
				t.Errorf("Method() = %q, want %q", tx.Method(), tc.want)
			}

			if len(res.Dropped) != 0 {
				t.Errorf("dropped = %v, want nothing — the method is mapped, "+
					"not reported", res.Dropped)
			}
		})
	}
}

// TestCancelBecomesReachable is §4.11, and it is a claim about code that
// did not change.
//
// SRD-089.D routed "may a Cancel sit here" to the model. With no
// transaction able to import, that delegation had exactly one observable
// outcome — refusal — and could have been a hard-coded no without any
// test noticing. Now a Cancel End belongs inside a transaction's graph
// and a Cancel boundary on the transaction itself, so the same
// delegation has to answer both ways.
func TestCancelBecomesReachable(t *testing.T) {
	t.Run("a cancel end inside a transaction imports", func(t *testing.T) {
		_, err := importEventDoc(t, variantDocWith(
			`<bpmn:transaction id="sub" name="Charge">`,
			`      <bpmn:startEvent id="is"/>
      <bpmn:task id="it" name="Work"/>
      <bpmn:endEvent id="ie">
        <bpmn:cancelEventDefinition id="ced"/>
      </bpmn:endEvent>
      <bpmn:sequenceFlow id="if1" sourceRef="is" targetRef="it"/>
      <bpmn:sequenceFlow id="if2" sourceRef="it" targetRef="ie"/>`))
		if err != nil {
			t.Fatalf("Import: %v", err)
		}
	})

	t.Run("a cancel boundary attaches to a transaction", func(t *testing.T) {
		doc := strings.Replace(
			variantDoc(`<bpmn:transaction id="sub" name="Charge">`),
			`    <bpmn:endEvent id="e1"/>`,
			`    <bpmn:endEvent id="e1"/>
    <bpmn:boundaryEvent id="b1" attachedToRef="sub">
      <bpmn:cancelEventDefinition id="bced"/>
    </bpmn:boundaryEvent>`, 1)

		res, err := importEventDoc(t, doc)
		if err != nil {
			t.Fatalf("Import: %v", err)
		}

		nodeByID(t, res, "b1")
	})

	t.Run("a cancel boundary on a plain sub-process is refused", func(t *testing.T) {
		doc := strings.Replace(
			variantDoc(`<bpmn:subProcess id="sub" name="Plain">`),
			`    <bpmn:endEvent id="e1"/>`,
			`    <bpmn:endEvent id="e1"/>
    <bpmn:boundaryEvent id="b1" attachedToRef="sub">
      <bpmn:cancelEventDefinition id="bced"/>
    </bpmn:boundaryEvent>`, 1)

		if _, err := importEventDoc(t, doc); err == nil {
			t.Fatal("a Cancel boundary on a non-transaction must be refused")
		}
	})

	t.Run("a cancel end outside one is still refused", func(t *testing.T) {
		_, err := importEventDoc(t, variantDocWith(
			`<bpmn:subProcess id="sub" name="Plain">`,
			`      <bpmn:startEvent id="is"/>
      <bpmn:endEvent id="ie">
        <bpmn:cancelEventDefinition id="ced"/>
      </bpmn:endEvent>
      <bpmn:sequenceFlow id="if1" sourceRef="is" targetRef="ie"/>`))
		if err == nil {
			t.Fatal("a Cancel End outside a transaction must be refused")
		}
	})
}

// TestTransactionProtocolIsCarried is SRD-093 T-7: the protocol lands on
// the model as stated and is no longer reported as dropped.
func TestTransactionProtocolIsCarried(t *testing.T) {
	t.Run("stated", func(t *testing.T) {
		res, err := importEventDoc(t, variantDoc(`<bpmn:transaction id="sub" `+
			`name="Charge" method="compensate" protocol="wsat">`))
		if err != nil {
			t.Fatalf("Import: %v", err)
		}

		sub := containerOf(t, nodeByID(t, res, "sub"))
		if got := sub.Transaction().Protocol(); got != "wsat" {
			t.Errorf("Protocol() = %q, want wsat", got)
		}

		if len(res.Dropped) != 0 {
			t.Errorf("dropped = %v, want nothing — the protocol is carried",
				res.Dropped)
		}
	})

	t.Run("absent", func(t *testing.T) {
		res, err := importEventDoc(t, variantDoc(
			`<bpmn:transaction id="sub" name="Charge">`))
		if err != nil {
			t.Fatalf("Import: %v", err)
		}

		sub := containerOf(t, nodeByID(t, res, "sub"))
		if got := sub.Transaction().Protocol(); got != "" {
			t.Errorf("Protocol() = %q, want none", got)
		}
	})
}
