package bpmn

import (
	"context"
	"strings"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
)

// implDoc is a serviceTask carrying the BPMN implementation hint.
const implDoc = `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:interface id="i1" name="Billing">
    <bpmn:operation id="op1" name="charge"/>
  </bpmn:interface>
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    <bpmn:serviceTask id="v1" name="Charge" implementation="##WebService" operationRef="op1"/>
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="v1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="v1" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`

// TestServiceTaskImplementationRoundTrips covers SRD-089.A §6 T-3 (FR-5).
// The exporter wrote `implementation` and the importer never read it, so
// the attribute survived no round-trip — and could not, because the value
// was derived from the Operation's Implementor, which an imported
// operation deliberately lacks.
func TestServiceTaskImplementationRoundTrips(t *testing.T) {
	p, err := importer{}.Import(context.Background(), strings.NewReader(implDoc))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	var st *activities.ServiceTask

	for _, n := range p.Nodes() {
		if s, ok := n.(*activities.ServiceTask); ok {
			st = s
		}
	}

	if st == nil {
		t.Fatal("service task missing after import")
	}

	if got := st.Implementation(); got != "##WebService" {
		t.Errorf("Implementation() = %q, want the imported hint", got)
	}

	if out := exportOnce(t, implDoc); !strings.Contains(out, `implementation="##WebService"`) {
		t.Errorf("export lost the implementation hint:\n%s", out)
	}
}

// TestServiceTaskImplementationDefaultsToTheOperation pins the
// backwards-compatible half: with no hint in the document, the value is
// still derived from the Operation, and an unspecified one is not written
// out at all.
func TestServiceTaskImplementationDefaultsToTheOperation(t *testing.T) {
	doc := strings.Replace(implDoc, ` implementation="##WebService"`, "", 1)

	p, err := importer{}.Import(context.Background(), strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	for _, n := range p.Nodes() {
		st, ok := n.(*activities.ServiceTask)
		if !ok {
			continue
		}

		if got := st.Implementation(); got != service.UnspecifiedImplementation {
			t.Errorf("Implementation() = %q, want the operation's derived value", got)
		}
	}

	if out := exportOnce(t, doc); strings.Contains(out, "implementation=") {
		t.Errorf("export wrote an unspecified implementation:\n%s", out)
	}
}
