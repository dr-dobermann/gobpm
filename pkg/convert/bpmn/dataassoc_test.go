package bpmn

import (
	"strings"
	"testing"
)

// assocDoc wraps a task with an ioSpecification and associations beside
// a declared data object, the §4a shape reduced to its wiring.
func assocDoc(decls, taskBody string) string {
	return propDoc(
		`  <bpmn:itemDefinition id="idOrder" structureRef="xsd:string"/>
  <bpmn:itemDefinition id="idCount" structureRef="xsd:int"/>`+decls,
		`    <bpmn:dataObject id="do1" name="order" itemSubjectRef="idOrder"/>
    <bpmn:task id="t1" name="Work">
`+taskBody+`
    </bpmn:task>
    <bpmn:sequenceFlow id="f2" sourceRef="s1" targetRef="t1"/>
    <bpmn:sequenceFlow id="f3" sourceRef="t1" targetRef="e1"/>`)
}

// inputAssoc is the canonical typed input wiring: do1 → din1.
const inputAssoc = `      <bpmn:ioSpecification id="io1">
        <bpmn:dataInput id="din1" name="in" itemSubjectRef="idOrder"/>
      </bpmn:ioSpecification>
      <bpmn:dataInputAssociation id="dia1">
        <bpmn:sourceRef>do1</bpmn:sourceRef>
        <bpmn:targetRef>din1</bpmn:targetRef>
      </bpmn:dataInputAssociation>`

// TestInputAssociationWires is T-7 (FR-3): both ends typed over one
// item, wired through the data object's own AssociateTarget.
func TestInputAssociationWires(t *testing.T) {
	res, err := importEventDoc(t, assocDoc("", inputAssoc))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	if dos := res.Processes[0].DataObjects(); len(dos) != 1 {
		t.Fatalf("DataObjects() = %v, want order", dos)
	}

	if len(res.Dropped) != 0 {
		t.Errorf("Dropped = %v, want nothing — everything here maps", res.Dropped)
	}
}

// TestDuplicateWiringHitsTheModelGuard: the model refuses a second
// association from one object to one node — which also proves the FIRST
// one actually bound (the guard is on the bound set).
func TestDuplicateWiringHitsTheModelGuard(t *testing.T) {
	_, err := importEventDoc(t, assocDoc("",
		inputAssoc+`
      <bpmn:dataInputAssociation id="dia2">
        <bpmn:sourceRef>do1</bpmn:sourceRef>
        <bpmn:targetRef>din1</bpmn:targetRef>
      </bpmn:dataInputAssociation>`))
	if err == nil || !strings.Contains(err.Error(), "duplicate association") {
		t.Fatalf("error = %v, want the model's duplicate-binding guard", err)
	}

	if !strings.Contains(err.Error(), `"dia2"`) {
		t.Errorf("error = %v, want the second association's id attached", err)
	}
}

// TestOutputAssociationWires is T-8: dataOutput → dataObject through
// AssociateSource.
func TestOutputAssociationWires(t *testing.T) {
	_, err := importEventDoc(t, assocDoc("",
		`      <bpmn:ioSpecification id="io1">
        <bpmn:dataOutput id="dout1" name="out" itemSubjectRef="idOrder"/>
      </bpmn:ioSpecification>
      <bpmn:dataOutputAssociation id="doa1">
        <bpmn:sourceRef>dout1</bpmn:sourceRef>
        <bpmn:targetRef>do1</bpmn:targetRef>
      </bpmn:dataOutputAssociation>`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
}

// TestAssociationThroughAReference is T-9: SAD-001 §14.1 rule 2 — a
// sourceRef naming a <dataObjectReference> retargets to its object.
func TestAssociationThroughAReference(t *testing.T) {
	_, err := importEventDoc(t, assocDoc("",
		`      <bpmn:ioSpecification id="io1">
        <bpmn:dataInput id="din1" name="in" itemSubjectRef="idOrder"/>
      </bpmn:ioSpecification>
      <bpmn:dataInputAssociation id="dia1">
        <bpmn:sourceRef>dor1</bpmn:sourceRef>
        <bpmn:targetRef>din1</bpmn:targetRef>
      </bpmn:dataInputAssociation>
      </bpmn:task>
    <bpmn:dataObjectReference id="dor1" dataObjectRef="do1"/>
    <bpmn:task id="tpad" name="pad">`))
	if err != nil {
		t.Fatalf("import: %v — the reference must retarget to do1", err)
	}
}

// TestAssociationWithAStoreEnd is T-10: the store reference wires
// through its own Associate*, seeding the engine-store routing.
func TestAssociationWithAStoreEnd(t *testing.T) {
	_, err := importEventDoc(t, assocDoc("",
		`      <bpmn:ioSpecification id="io1">
        <bpmn:dataInput id="din1" name="in" itemSubjectRef="idOrder"/>
      </bpmn:ioSpecification>
      <bpmn:dataInputAssociation id="dia1">
        <bpmn:sourceRef>dsr1</bpmn:sourceRef>
        <bpmn:targetRef>din1</bpmn:targetRef>
      </bpmn:dataInputAssociation>
      </bpmn:task>
    <bpmn:dataStoreReference id="dsr1" name="orders" dataStoreRef="S"
                             itemSubjectRef="idOrder"/>
    <bpmn:task id="tpad" name="pad">`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
}

// TestUntypedParameterAdopts is T-11 (§4.3 case 2): the parameter names
// no itemSubjectRef, its association partner does — the parameter is
// built over the partner's item, observably.
func TestUntypedParameterAdopts(t *testing.T) {
	res, err := importEventDoc(t, assocDoc("",
		`      <bpmn:ioSpecification id="io1">
        <bpmn:dataInput id="din1" name="in"/>
      </bpmn:ioSpecification>
      <bpmn:dataInputAssociation id="dia1">
        <bpmn:sourceRef>do1</bpmn:sourceRef>
        <bpmn:targetRef>din1</bpmn:targetRef>
      </bpmn:dataInputAssociation>`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	ins := taskOf(t, res).Inputs()
	if len(ins) != 1 || ins[0].ItemDefinition().ID() != "idOrder" {
		t.Errorf("Inputs() = %v, want the adopted idOrder item", ins)
	}
}

// TestTypedMismatchRefused is T-12: both ends typed, items differ — the
// standard's own type constraint, both ids named.
func TestTypedMismatchRefused(t *testing.T) {
	_, err := importEventDoc(t, assocDoc("",
		`      <bpmn:ioSpecification id="io1">
        <bpmn:dataInput id="din1" name="in" itemSubjectRef="idCount"/>
      </bpmn:ioSpecification>
      <bpmn:dataInputAssociation id="dia1">
        <bpmn:sourceRef>do1</bpmn:sourceRef>
        <bpmn:targetRef>din1</bpmn:targetRef>
      </bpmn:dataInputAssociation>`))
	if err == nil {
		t.Fatal("a typed mismatch must be refused")
	}

	for _, want := range []string{"§10.4.1", `"idCount"`, `"idOrder"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to carry %q", err, want)
		}
	}
}

// TestUntypedElementRefused is T-13 (§4.3 case 3): the element never
// adopts, in both the adoption path (untyped param) and the wiring path
// (typed param).
func TestUntypedElementRefused(t *testing.T) {
	tests := map[string]string{
		"untyped parameter too": `<bpmn:dataInput id="din1" name="in"/>`,
		"typed parameter":       `<bpmn:dataInput id="din1" name="in" itemSubjectRef="idOrder"/>`,
	}

	for name, param := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := importEventDoc(t, propDoc(
				`  <bpmn:itemDefinition id="idOrder" structureRef="xsd:string"/>`,
				`    <bpmn:dataObject id="bare" name="untyped"/>
    <bpmn:task id="t1" name="Work">
      <bpmn:ioSpecification id="io1">
        `+param+`
      </bpmn:ioSpecification>
      <bpmn:dataInputAssociation id="dia1">
        <bpmn:sourceRef>bare</bpmn:sourceRef>
        <bpmn:targetRef>din1</bpmn:targetRef>
      </bpmn:dataInputAssociation>
    </bpmn:task>
    <bpmn:sequenceFlow id="f2" sourceRef="s1" targetRef="t1"/>
    <bpmn:sequenceFlow id="f3" sourceRef="t1" targetRef="e1"/>`))
			if err == nil || !strings.Contains(err.Error(), "itemSubjectRef") {
				t.Fatalf("error = %v, want the type-the-element refusal", err)
			}
		})
	}
}

// TestAssociationEndErrors is T-14: dangling refs, wrong kinds, another
// activity's parameter, and a missing end.
func TestAssociationEndErrors(t *testing.T) {
	tests := map[string]struct {
		assoc string
		want  string
	}{
		"dangling element end": {
			assoc: `<bpmn:sourceRef>ghost</bpmn:sourceRef>
        <bpmn:targetRef>din1</bpmn:targetRef>`,
			want: "no such element is declared",
		},
		"wrong-kind element end": {
			assoc: `<bpmn:sourceRef>s1</bpmn:sourceRef>
        <bpmn:targetRef>din1</bpmn:targetRef>`,
			want: `"s1" is a startEvent`,
		},
		"dangling parameter end": {
			assoc: `<bpmn:sourceRef>do1</bpmn:sourceRef>
        <bpmn:targetRef>ghost</bpmn:targetRef>`,
			want: "no such element is declared",
		},
		"no element end": {
			assoc: `<bpmn:targetRef>din1</bpmn:targetRef>`,
			want:  "names no sourceRef",
		},
		"no parameter end": {
			assoc: `<bpmn:sourceRef>do1</bpmn:sourceRef>`,
			want:  "names no targetRef",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := importEventDoc(t, assocDoc("",
				`      <bpmn:ioSpecification id="io1">
        <bpmn:dataInput id="din1" name="in" itemSubjectRef="idOrder"/>
      </bpmn:ioSpecification>
      <bpmn:dataInputAssociation id="dia1">
        `+tc.assoc+`
      </bpmn:dataInputAssociation>`))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

// TestDialectAttrsOnTheDataFlowFamily: the .D reporting funnel covers
// the family's own elements — a Camunda attribute on the spec, a set or
// an association is reported, never silently dropped.
func TestDialectAttrsOnTheDataFlowFamily(t *testing.T) {
	res, err := importEventDoc(t, `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:xsd="http://www.w3.org/2001/XMLSchema"
                  xmlns:camunda="http://camunda.org/schema/1.0/bpmn">
  <bpmn:itemDefinition id="idOrder" structureRef="xsd:string"/>
  <bpmn:process id="P" name="P">
    <bpmn:dataObject id="do1" name="order" itemSubjectRef="idOrder"/>
    <bpmn:startEvent id="s1"/>
    <bpmn:task id="t1" name="Work">
      <bpmn:ioSpecification id="io1" camunda:a="1">
        <bpmn:dataInput id="din1" name="in" itemSubjectRef="idOrder"/>
        <bpmn:inputSet id="is1" camunda:b="2">
          <bpmn:dataInputRefs>din1</bpmn:dataInputRefs>
        </bpmn:inputSet>
      </bpmn:ioSpecification>
      <bpmn:dataInputAssociation id="dia1" camunda:c="3">
        <bpmn:sourceRef>do1</bpmn:sourceRef>
        <bpmn:targetRef>din1</bpmn:targetRef>
      </bpmn:dataInputAssociation>
    </bpmn:task>
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="t1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t1" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`)
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	got := map[string]string{}
	for _, d := range res.Dropped {
		got[d.Construct] = d.Element
	}

	want := map[string]string{
		"camunda:a": "io1", "camunda:b": "is1", "camunda:c": "dia1",
	}

	for construct, elem := range want {
		if got[construct] != elem {
			t.Errorf("Dropped[%s] = %q, want %q — the funnel must cover the "+
				"family's own elements", construct, got[construct], elem)
		}
	}
}

// TestArtifactAssociationIDJoinsTheLedger: the .E artifact association's
// id was the last declared id outside the §4.11 ledger.
func TestArtifactAssociationIDJoinsTheLedger(t *testing.T) {
	_, err := importEventDoc(t, propDoc("",
		`    <bpmn:textAnnotation id="note"><bpmn:text>x</bpmn:text></bpmn:textAnnotation>
    <bpmn:association id="s1" sourceRef="note" targetRef="e1"/>`))
	if err == nil || !strings.Contains(err.Error(), "duplicate id") {
		t.Fatalf("error = %v, want the ledger's refusal", err)
	}
}

// TestReferenceRetargetActuallyBinds: the duplicate-binding guard fires
// only against a BOUND association, so a second association to the same
// object proves the retargeted first one really attached (the
// under-constrained-positive finding from the independent review).
func TestReferenceRetargetActuallyBinds(t *testing.T) {
	_, err := importEventDoc(t, assocDoc("",
		`      <bpmn:ioSpecification id="io1">
        <bpmn:dataInput id="din1" name="in" itemSubjectRef="idOrder"/>
      </bpmn:ioSpecification>
      <bpmn:dataInputAssociation id="dia1">
        <bpmn:sourceRef>dor1</bpmn:sourceRef>
        <bpmn:targetRef>din1</bpmn:targetRef>
      </bpmn:dataInputAssociation>
      <bpmn:dataInputAssociation id="dia2">
        <bpmn:sourceRef>do1</bpmn:sourceRef>
        <bpmn:targetRef>din1</bpmn:targetRef>
      </bpmn:dataInputAssociation>
      </bpmn:task>
    <bpmn:dataObjectReference id="dor1" dataObjectRef="do1"/>
    <bpmn:task id="tpad" name="pad">`))
	if err == nil || !strings.Contains(err.Error(), "duplicate association") {
		t.Fatalf("error = %v, want the model's duplicate guard — it can only "+
			"fire if the retargeted first association BOUND", err)
	}
}

// TestUntypedParameterWithoutMatchingAssociation: adoption only serves
// an association's end — an untyped parameter with none (the node's one
// association belongs to the other direction) keeps an empty item of
// its own.
func TestUntypedParameterWithoutMatchingAssociation(t *testing.T) {
	res, err := importEventDoc(t, assocDoc("",
		`      <bpmn:ioSpecification id="io1">
        <bpmn:dataInput id="din1" name="in"/>
        <bpmn:dataOutput id="dout1" name="out" itemSubjectRef="idOrder"/>
      </bpmn:ioSpecification>
      <bpmn:dataOutputAssociation id="doa1">
        <bpmn:sourceRef>dout1</bpmn:sourceRef>
        <bpmn:targetRef>do1</bpmn:targetRef>
      </bpmn:dataOutputAssociation>`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	ins := taskOf(t, res).Inputs()
	// emptyItem derives the item id from the element's own (id+":item").
	if len(ins) != 1 || ins[0].ItemDefinition().ID() != "din1:item" {
		t.Errorf("Inputs() = %v, want din1's own empty item", ins)
	}
}

// TestOutputAssociationEndErrors mirrors T-14 for the output direction,
// where sourceRef is the parameter and targetRef the element.
func TestOutputAssociationEndErrors(t *testing.T) {
	tests := map[string]struct {
		assoc string
		want  string
	}{
		"no parameter end": {
			assoc: `<bpmn:targetRef>do1</bpmn:targetRef>`,
			want:  "names no sourceRef",
		},
		"no element end": {
			assoc: `<bpmn:sourceRef>dout1</bpmn:sourceRef>`,
			want:  "names no targetRef",
		},
		"dangling element end": {
			assoc: `<bpmn:sourceRef>dout1</bpmn:sourceRef>
        <bpmn:targetRef>ghost</bpmn:targetRef>`,
			want: "no such element is declared",
		},
		"wrong-kind parameter end": {
			assoc: `<bpmn:sourceRef>do1</bpmn:sourceRef>
        <bpmn:targetRef>do1</bpmn:targetRef>`,
			want: `"do1" is a dataObject`,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := importEventDoc(t, assocDoc("",
				`      <bpmn:ioSpecification id="io1">
        <bpmn:dataOutput id="dout1" name="out" itemSubjectRef="idOrder"/>
      </bpmn:ioSpecification>
      <bpmn:dataOutputAssociation id="doa1">
        `+tc.assoc+`
      </bpmn:dataOutputAssociation>`))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

// TestOutputAssociationToAStore: the store reference as an OUTPUT target
// — the direction SRD-068's write half routes.
func TestOutputAssociationToAStore(t *testing.T) {
	_, err := importEventDoc(t, assocDoc("",
		`      <bpmn:ioSpecification id="io1">
        <bpmn:dataOutput id="dout1" name="out" itemSubjectRef="idOrder"/>
      </bpmn:ioSpecification>
      <bpmn:dataOutputAssociation id="doa1">
        <bpmn:sourceRef>dout1</bpmn:sourceRef>
        <bpmn:targetRef>dsr1</bpmn:targetRef>
      </bpmn:dataOutputAssociation>
      </bpmn:task>
    <bpmn:dataStoreReference id="dsr1" name="orders" dataStoreRef="S"
                             itemSubjectRef="idOrder"/>
    <bpmn:task id="tpad" name="pad">`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
}

// TestBrokenAssociationInsideATask: the parse error surfaces through the
// node-child registration.
func TestBrokenAssociationInsideATask(t *testing.T) {
	_, err := importEventDoc(t, assocDoc("",
		`      <bpmn:dataInputAssociation/>`))
	if err == nil || !strings.Contains(err.Error(), "has no id") {
		t.Fatalf("error = %v, want the missing-id refusal", err)
	}
}

// TestAnotherActivitysParameter: the ref exists and is even a dataInput
// — of a different task. Saying "wrong kind" would mislead.
func TestAnotherActivitysParameter(t *testing.T) {
	_, err := importEventDoc(t, assocDoc("",
		`      <bpmn:dataInputAssociation id="dia1">
        <bpmn:sourceRef>do1</bpmn:sourceRef>
        <bpmn:targetRef>din2</bpmn:targetRef>
      </bpmn:dataInputAssociation>
      </bpmn:task>
    <bpmn:task id="t2" name="Other">
      <bpmn:ioSpecification id="io2">
        <bpmn:dataInput id="din2" name="other" itemSubjectRef="idOrder"/>
      </bpmn:ioSpecification>
    </bpmn:task>
    <bpmn:task id="tpad" name="pad">`))
	if err == nil || !strings.Contains(err.Error(), "belongs to another activity") {
		t.Fatalf("error = %v, want the other-activity wording", err)
	}
}

// TestTransformationRefused is T-15/T-16 (§4.6, #328): the expression
// shapes, one wording — several sources are only legal under a
// transformation, and the transformation is the refused capability.
func TestTransformationRefused(t *testing.T) {
	tests := map[string]string{
		"transformation": `<bpmn:sourceRef>do1</bpmn:sourceRef>
        <bpmn:targetRef>din1</bpmn:targetRef>
        <bpmn:transformation>a + b</bpmn:transformation>`,
		"two sources": `<bpmn:sourceRef>do1</bpmn:sourceRef>
        <bpmn:sourceRef>do1</bpmn:sourceRef>
        <bpmn:targetRef>din1</bpmn:targetRef>`,
	}

	for name, assoc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := importEventDoc(t, assocDoc("",
				`      <bpmn:ioSpecification id="io1">
        <bpmn:dataInput id="din1" name="in" itemSubjectRef="idOrder"/>
      </bpmn:ioSpecification>
      <bpmn:dataInputAssociation id="dia1">
        `+assoc+`
      </bpmn:dataInputAssociation>`))
			if err == nil {
				t.Fatal("the expression shapes must be refused")
			}

			for _, want := range []string{"#328", "SRD-063 §10.3"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %v, want it to carry %q", err, want)
				}
			}

			if strings.Contains(err.Error(), " yet") {
				t.Errorf("refusal says \"yet\": %v", err)
			}
		})
	}
}

// TestAssignmentRefused is T-17 (#328's assignment wording).
func TestAssignmentRefused(t *testing.T) {
	_, err := importEventDoc(t, assocDoc("",
		`      <bpmn:ioSpecification id="io1">
        <bpmn:dataInput id="din1" name="in" itemSubjectRef="idOrder"/>
      </bpmn:ioSpecification>
      <bpmn:dataInputAssociation id="dia1">
        <bpmn:sourceRef>do1</bpmn:sourceRef>
        <bpmn:targetRef>din1</bpmn:targetRef>
        <bpmn:assignment id="as1"/>
      </bpmn:dataInputAssociation>`))
	if err == nil || !strings.Contains(err.Error(), "#328") ||
		!strings.Contains(err.Error(), "assignment") {
		t.Fatalf("error = %v, want the assignment refusal naming #328", err)
	}
}

// TestEventAssociationRefused is T-18 (§4.7, #329): BPMN allows the
// shape; the model has no attachment for it.
func TestEventAssociationRefused(t *testing.T) {
	_, err := importEventDoc(t, propDoc(
		`  <bpmn:signal id="sig1" name="Cancelled"/>`,
		`    <bpmn:dataObject id="do1" name="order"/>
    <bpmn:intermediateThrowEvent id="ev1">
      <bpmn:signalEventDefinition signalRef="sig1"/>
      <bpmn:dataInputAssociation id="dia1">
        <bpmn:sourceRef>do1</bpmn:sourceRef>
        <bpmn:targetRef>din1</bpmn:targetRef>
      </bpmn:dataInputAssociation>
    </bpmn:intermediateThrowEvent>
    <bpmn:sequenceFlow id="f2" sourceRef="s1" targetRef="ev1"/>
    <bpmn:sequenceFlow id="f3" sourceRef="ev1" targetRef="e1"/>`))
	if err == nil || !strings.Contains(err.Error(), "#329") {
		t.Fatalf("error = %v, want the event refusal naming #329", err)
	}
}

// TestGatewayAssociationRefused: outside both the tasks and the events —
// the association's activity end is a task's parameter, and the refusal
// says where to move it.
func TestGatewayAssociationRefused(t *testing.T) {
	_, err := importEventDoc(t, propDoc("",
		`    <bpmn:dataObject id="do1" name="order"/>
    <bpmn:exclusiveGateway id="g1">
      <bpmn:dataInputAssociation id="dia1">
        <bpmn:sourceRef>do1</bpmn:sourceRef>
      </bpmn:dataInputAssociation>
    </bpmn:exclusiveGateway>
    <bpmn:sequenceFlow id="f2" sourceRef="s1" targetRef="g1"/>
    <bpmn:sequenceFlow id="f3" sourceRef="g1" targetRef="e1"/>`))
	if err == nil || !strings.Contains(err.Error(), "move it to the task") {
		t.Fatalf("error = %v, want the non-activity refusal", err)
	}
}

// TestPropertyEndRefused is T-20 (§4.7, #331).
func TestPropertyEndRefused(t *testing.T) {
	_, err := importEventDoc(t, propDoc(
		`  <bpmn:itemDefinition id="idOrder" structureRef="xsd:string"/>`,
		`    <bpmn:property id="p1" name="note" itemSubjectRef="idOrder"/>
    <bpmn:task id="t1" name="Work">
      <bpmn:ioSpecification id="io1">
        <bpmn:dataInput id="din1" name="in" itemSubjectRef="idOrder"/>
      </bpmn:ioSpecification>
      <bpmn:dataInputAssociation id="dia1">
        <bpmn:sourceRef>p1</bpmn:sourceRef>
        <bpmn:targetRef>din1</bpmn:targetRef>
      </bpmn:dataInputAssociation>
    </bpmn:task>
    <bpmn:sequenceFlow id="f2" sourceRef="s1" targetRef="t1"/>
    <bpmn:sequenceFlow id="f3" sourceRef="t1" targetRef="e1"/>`))
	if err == nil || !strings.Contains(err.Error(), "#331") {
		t.Fatalf("error = %v, want the property refusal naming #331", err)
	}
}
