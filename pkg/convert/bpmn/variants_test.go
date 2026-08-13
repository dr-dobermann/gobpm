package bpmn

import (
	"strings"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/convert"
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

// TestTransactionMethodDispositions is §4.5: three values, three
// different answers, and the silent one is as deliberate as the loud
// ones.
func TestTransactionMethodDispositions(t *testing.T) {
	tests := map[string]struct {
		attr     string
		imports  bool
		reported bool
	}{
		"absent means compensate": {"", true, false},
		"compensate":              {` method="compensate"`, true, false},
		"store":                   {` method="store"`, false, false},
		"image":                   {` method="image"`, false, false},
		"not a BPMN value":        {` method="rollback"`, false, false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			res, err := importEventDoc(t,
				variantDoc(`<bpmn:transaction id="sub" name="Charge"`+tc.attr+`>`))

			if !tc.imports {
				if err == nil {
					t.Fatal("want a refusal, got a clean import")
				}

				if strings.Contains(err.Error(), "yet") {
					t.Errorf("refusal %q says \"yet\" — this is a decided "+
						"non-goal, and nothing is coming", err)
				}

				return
			}

			if err != nil {
				t.Fatalf("Import: %v", err)
			}

			for _, d := range res.Dropped {
				if strings.Contains(d.Construct, attrTransactionMethod) {
					t.Errorf("method reported as dropped (%q) — the engine "+
						"implements this value", d.Reason)
				}
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

// TestTransactionProtocolIsReported is the other half of §4.5: the
// element imports, minus a datum, and the host is told which.
func TestTransactionProtocolIsReported(t *testing.T) {
	res, err := importEventDoc(t, variantDoc(
		`<bpmn:transaction id="sub" name="Charge" method="compensate" protocol="wsat">`))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	var got *convert.Dropped

	for i := range res.Dropped {
		if res.Dropped[i].Construct == attrTransactionProto {
			got = &res.Dropped[i]
		}
	}

	if got == nil {
		t.Fatalf("dropped = %v, want the protocol reported", res.Dropped)
	}

	if got.Element != "sub" {
		t.Errorf("protocol reported on %q, want the transaction", got.Element)
	}

	if got.Reason == "" {
		t.Error("protocol reported with no reason")
	}

	if len(res.Dropped) != 1 {
		t.Errorf("dropped = %v, want protocol alone — method=compensate is "+
			"implemented, and a construct is either mapped or reported",
			res.Dropped)
	}
}
