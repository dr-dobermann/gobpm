package bpmn

import (
	"strings"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
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
// shapedStart imports a start event whose output association carries the
// given shape children, and returns the built association. An event owns
// the same wireDataAssoc path a task does and, unlike a task, exposes its
// associations — so the mapping is assertable without a new model API.
func shapedStart(t *testing.T, extraDecl, shape string) *data.Association {
	t.Helper()

	s := startOf(t, propDoc(eventDataDecls+extraDecl,
		`    <bpmn:dataObject id="do1" name="order" itemSubjectRef="idStr"/>
    <bpmn:startEvent id="s2">
      <bpmn:messageEventDefinition messageRef="m1"/>
      <bpmn:dataOutput id="s2-out" itemSubjectRef="idStr"/>
      <bpmn:dataOutputAssociation id="oa1">
        <bpmn:sourceRef>s2-out</bpmn:sourceRef>
        <bpmn:targetRef>do1</bpmn:targetRef>
`+shape+`
      </bpmn:dataOutputAssociation>
    </bpmn:startEvent>
    <bpmn:sequenceFlow id="f2" sourceRef="s2" targetRef="e1"/>`), "s2")

	aa := s.OutputAssociations()
	if len(aa) != 1 {
		t.Fatalf("OutputAssociations() = %v, want one", aa)
	}

	return aa[0]
}

// shapedStartErr is shapedStart for the documents that must NOT import.
func shapedStartErr(t *testing.T, shape string) error {
	t.Helper()

	_, err := importEventDoc(t, propDoc(eventDataDecls,
		`    <bpmn:dataObject id="do1" name="order" itemSubjectRef="idStr"/>
    <bpmn:startEvent id="s2">
      <bpmn:messageEventDefinition messageRef="m1"/>
      <bpmn:dataOutput id="s2-out" itemSubjectRef="idStr"/>
      <bpmn:dataOutputAssociation id="oa1">
        <bpmn:sourceRef>s2-out</bpmn:sourceRef>
        <bpmn:targetRef>do1</bpmn:targetRef>
`+shape+`
      </bpmn:dataOutputAssociation>
    </bpmn:startEvent>
    <bpmn:sequenceFlow id="f2" sourceRef="s2" targetRef="e1"/>`))

	return err
}

// TestTransformationImports is SRD-097 T-9: a <transformation> maps onto
// the model instead of refusing the file.
func TestTransformationImports(t *testing.T) {
	a := shapedStart(t, "",
		`        <bpmn:transformation language="gobpm:lite">order</bpmn:transformation>`)

	fe := a.Transformation()
	if fe == nil {
		t.Fatal("Transformation() = nil, want the mapped expression")
	}

	// Non-nil proves only that SOMETHING was mapped. The id proves it came
	// from THIS element — the converter mints an expression under its
	// owner's id and role when the document declares none — and the
	// language proves the resolution ran rather than a default being
	// assumed. (A lite expression evaluates only through the engine
	// registry, so a converter test cannot run its body; that the body
	// itself computes is proven end to end by
	// examples/association-expressions.)
	if got := fe.ID(); got != "oa1:transformation" {
		t.Errorf("Transformation().ID() = %q, want it minted from the "+
			"association that carried it", got)
	}

	if got := fe.Language(); !strings.Contains(strings.ToLower(got), "lite") {
		t.Errorf("Transformation().Language() = %q, want the resolved "+
			"language", got)
	}

	if len(a.Assignments()) != 0 {
		t.Error("Assignments() = non-empty, want none")
	}
}

// TestAssignmentImports is SRD-097 T-9's second half: each <assignment>
// maps, from and to alike, and the ${…} wrapper a modeler's tool writes
// around a path is unwrapped (FR-8).
func TestAssignmentImports(t *testing.T) {
	a := shapedStart(t, "",
		`        <bpmn:assignment id="as1">
          <bpmn:from language="gobpm:lite">s2-out</bpmn:from>
          <bpmn:to>order</bpmn:to>
        </bpmn:assignment>
        <bpmn:assignment id="as2">
          <bpmn:from>${s2-out}</bpmn:from>
          <bpmn:to>${order}</bpmn:to>
        </bpmn:assignment>`)

	as := a.Assignments()
	if len(as) != 2 {
		t.Fatalf("Assignments() = %d, want both", len(as))
	}

	if as[0].To() != "order" || as[1].To() != "order" {
		t.Errorf("to = %q/%q, want the path and the unwrapped ${…}",
			as[0].To(), as[1].To())
	}

	// Each <from> is minted under its own <assignment>'s declared id, which
	// is what proves the two did not collapse into one expression.
	for i, want := range []string{"as1:from", "as2:from"} {
		if as[i].From() == nil {
			t.Fatalf("assignment #%d: From() = nil", i)
		}

		if got := as[i].From().ID(); got != want {
			t.Errorf("assignment #%d: From().ID() = %q, want %q — the two "+
				"<from>s must map to distinct expressions", i, got, want)
		}
	}

	// the second from is JUEL, translated on the way in like any other

	if a.Transformation() != nil {
		t.Error("Transformation() = non-nil, want none")
	}
}

// TestToMustBeAPath is SRD-097 T-11 / FR-8: a <to> that is not a path is
// refused where it is read — never imported as a whole-value copy, which
// would silently discard the mapping the file declared.
func TestToMustBeAPath(t *testing.T) {
	err := shapedStartErr(t,
		`        <bpmn:assignment id="as1">
          <bpmn:from language="gobpm:lite">s2-out</bpmn:from>
          <bpmn:to>concat(order, "-done")</bpmn:to>
        </bpmn:assignment>`)
	if err == nil {
		t.Fatal("a <to> that is not a path must be refused")
	}

	for _, want := range []string{"oa1", "doesn't name", "<from>"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to carry %q", err, want)
		}
	}
}

// TestShapeCarriesEverySource is SRD-097 T-10's other side: under a
// transformation the file may name SEVERAL sources, and every one of them
// reaches the model — they gate the association even when the expression
// names none of them (§10.4.2 rule 1). A throw event's input association
// is the shape that carries them: several data elements into one node
// input.
func TestShapeCarriesEverySource(t *testing.T) {
	// The two sources carry DIFFERENT item definitions: an association
	// keys its sources by ItemDefinition id, so two of one type collide —
	// loudly, with the model's own "duplicate source" error.
	res, err := importEventDoc(t, propDoc(eventDataDecls+`
  <bpmn:itemDefinition id="idNum" structureRef="xsd:int"/>`,
		`    <bpmn:dataObject id="do1" name="order" itemSubjectRef="idStr"/>
    <bpmn:dataObject id="do2" name="extra" itemSubjectRef="idNum"/>
    <bpmn:intermediateThrowEvent id="t1">
      <bpmn:messageEventDefinition messageRef="m1"/>
      <bpmn:dataInput id="t1-in" itemSubjectRef="idStr"/>
      <bpmn:dataInputAssociation id="ia1">
        <bpmn:sourceRef>do1</bpmn:sourceRef>
        <bpmn:sourceRef>do2</bpmn:sourceRef>
        <bpmn:targetRef>t1-in</bpmn:targetRef>
        <bpmn:transformation language="gobpm:lite">order</bpmn:transformation>
      </bpmn:dataInputAssociation>
    </bpmn:intermediateThrowEvent>
    <bpmn:sequenceFlow id="f2" sourceRef="s1" targetRef="t1"/>
    <bpmn:sequenceFlow id="f3" sourceRef="t1" targetRef="e1"/>`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	th, _ := nodeByID(t, res, "t1").(*events.IntermediateThrowEvent)
	if th == nil || len(th.InputAssociations()) != 1 {
		t.Fatalf("the throw carries no input association")
	}

	a := th.InputAssociations()[0]

	names := a.SourceNames()
	if len(names) != 2 {
		t.Fatalf("SourceNames() = %v, want both sources carried", names)
	}

	if a.Transformation() == nil {
		t.Error("Transformation() = nil, want the mapped expression")
	}
}

// TestSeveralTargetsRefused is §10.4.1's own rule: an association has ONE
// target, so a second <targetRef> is refused rather than silently dropped.
func TestSeveralTargetsRefused(t *testing.T) {
	_, err := importEventDoc(t, propDoc(eventDataDecls,
		`    <bpmn:dataObject id="do1" name="order" itemSubjectRef="idStr"/>
    <bpmn:dataObject id="do2" name="extra" itemSubjectRef="idStr"/>
    <bpmn:startEvent id="s2">
      <bpmn:messageEventDefinition messageRef="m1"/>
      <bpmn:dataOutput id="s2-out" itemSubjectRef="idStr"/>
      <bpmn:dataOutputAssociation id="oa1">
        <bpmn:sourceRef>s2-out</bpmn:sourceRef>
        <bpmn:targetRef>do1</bpmn:targetRef>
        <bpmn:targetRef>do2</bpmn:targetRef>
      </bpmn:dataOutputAssociation>
    </bpmn:startEvent>
    <bpmn:sequenceFlow id="f2" sourceRef="s2" targetRef="e1"/>`))
	if err == nil {
		t.Fatal("several targets must be refused")
	}

	if !strings.Contains(err.Error(), "ONE target") {
		t.Errorf("error = %v, want §10.4.1's one-target rule", err)
	}
}

// TestOutputAssociationCarriesSeveralSources is the OUTPUT direction's
// multi-source half: there the PARAMETER side is the source, so several
// <sourceRef>s name several node outputs, and every one of them reaches
// the model's Associate* (§10.4.2 rule 1).
func TestOutputAssociationCarriesSeveralSources(t *testing.T) {
	_, err := importEventDoc(t, assocDoc("",
		`      <bpmn:ioSpecification id="io1">
        <bpmn:dataOutput id="dout1" name="a" itemSubjectRef="idOrder"/>
        <bpmn:dataOutput id="dout2" name="b" itemSubjectRef="idCount"/>
      </bpmn:ioSpecification>
      <bpmn:dataOutputAssociation id="doa1">
        <bpmn:sourceRef>dout1</bpmn:sourceRef>
        <bpmn:sourceRef>dout2</bpmn:sourceRef>
        <bpmn:targetRef>do1</bpmn:targetRef>
        <bpmn:transformation language="gobpm:lite">a</bpmn:transformation>
      </bpmn:dataOutputAssociation>`))
	if err != nil {
		t.Fatalf("several node outputs under a transformation must import: %v",
			err)
	}
}

// TestExtraSourceMustResolve: an extra <sourceRef> naming nothing the
// document declared is refused like the first one is — the refusal names
// the ref, not just the association.
func TestExtraSourceMustResolve(t *testing.T) {
	_, err := importEventDoc(t, propDoc(eventDataDecls,
		`    <bpmn:dataObject id="do1" name="order" itemSubjectRef="idStr"/>
    <bpmn:intermediateThrowEvent id="t1">
      <bpmn:messageEventDefinition messageRef="m1"/>
      <bpmn:dataInput id="t1-in" itemSubjectRef="idStr"/>
      <bpmn:dataInputAssociation id="ia1">
        <bpmn:sourceRef>do1</bpmn:sourceRef>
        <bpmn:sourceRef>nosuch</bpmn:sourceRef>
        <bpmn:targetRef>t1-in</bpmn:targetRef>
        <bpmn:transformation language="gobpm:lite">order</bpmn:transformation>
      </bpmn:dataInputAssociation>
    </bpmn:intermediateThrowEvent>
    <bpmn:sequenceFlow id="f2" sourceRef="s1" targetRef="t1"/>
    <bpmn:sequenceFlow id="f3" sourceRef="t1" targetRef="e1"/>`))
	if err == nil || !strings.Contains(err.Error(), "nosuch") {
		t.Fatalf("error = %v, want the unresolved extra source named", err)
	}
}

// TestAssignmentSkipsUnknownBPMNChildren: a BPMN child of <assignment>
// this converter has no use for is skipped, and the assignment still maps.
func TestAssignmentSkipsUnknownBPMNChildren(t *testing.T) {
	a := shapedStart(t, "",
		`        <bpmn:assignment id="as1">
          <bpmn:documentation>why</bpmn:documentation>
          <bpmn:from language="gobpm:lite">s2-out</bpmn:from>
          <bpmn:to>order</bpmn:to>
        </bpmn:assignment>`)

	if len(a.Assignments()) != 1 {
		t.Fatalf("Assignments() = %v, want the mapped one", a.Assignments())
	}
}

// TestToPathMustParse: a <to> whose path is malformed — an empty head —
// is refused by the path parser itself, before the target comparison.
func TestToPathMustParse(t *testing.T) {
	err := shapedStartErr(t, `        <bpmn:assignment id="as1">
          <bpmn:from language="gobpm:lite">s2-out</bpmn:from>
          <bpmn:to>.status</bpmn:to>
        </bpmn:assignment>`)
	if err == nil || !strings.Contains(err.Error(), "isn't a data path") {
		t.Fatalf("error = %v, want the malformed-path refusal", err)
	}
}

// TestExtraSourceOfTheWrongKind: an extra <sourceRef> naming an id that
// exists but is not a data element says which kind it found — the same
// refusal the first source gets.
func TestExtraSourceOfTheWrongKind(t *testing.T) {
	_, err := importEventDoc(t, propDoc(eventDataDecls,
		`    <bpmn:dataObject id="do1" name="order" itemSubjectRef="idStr"/>
    <bpmn:intermediateThrowEvent id="t1">
      <bpmn:messageEventDefinition messageRef="m1"/>
      <bpmn:dataInput id="t1-in" itemSubjectRef="idStr"/>
      <bpmn:dataInputAssociation id="ia1">
        <bpmn:sourceRef>do1</bpmn:sourceRef>
        <bpmn:sourceRef>s1</bpmn:sourceRef>
        <bpmn:targetRef>t1-in</bpmn:targetRef>
        <bpmn:transformation language="gobpm:lite">order</bpmn:transformation>
      </bpmn:dataInputAssociation>
    </bpmn:intermediateThrowEvent>
    <bpmn:sequenceFlow id="f2" sourceRef="s1" targetRef="t1"/>
    <bpmn:sequenceFlow id="f3" sourceRef="t1" targetRef="e1"/>`))
	if err == nil || !strings.Contains(err.Error(), "s1") {
		t.Fatalf("error = %v, want the wrong-kind refusal naming the ref", err)
	}
}

// TestExtraOutputSourceMustBeDeclared: on an OUTPUT association the extra
// <sourceRef>s name further node outputs, so one the activity does not
// declare is refused naming it.
func TestExtraOutputSourceMustBeDeclared(t *testing.T) {
	_, err := importEventDoc(t, assocDoc("",
		`      <bpmn:ioSpecification id="io1">
        <bpmn:dataOutput id="dout1" name="a" itemSubjectRef="idOrder"/>
      </bpmn:ioSpecification>
      <bpmn:dataOutputAssociation id="doa1">
        <bpmn:sourceRef>dout1</bpmn:sourceRef>
        <bpmn:sourceRef>nosuch</bpmn:sourceRef>
        <bpmn:targetRef>do1</bpmn:targetRef>
        <bpmn:transformation language="gobpm:lite">a</bpmn:transformation>
      </bpmn:dataOutputAssociation>`))
	if err == nil || !strings.Contains(err.Error(), "nosuch") {
		t.Fatalf("error = %v, want the undeclared-output refusal", err)
	}
}

// TestShapeExpressionsMustBeRunnable is FR-7's language half: a shape whose
// expression this converter cannot make runnable is refused naming the
// association, exactly as a condition in the same language would be.
func TestShapeExpressionsMustBeRunnable(t *testing.T) {
	tests := map[string]struct{ shape, names string }{
		"transformation": {
			`        <bpmn:transformation language="xpath">/a/b</bpmn:transformation>`,
			"oa1",
		},
		"assignment from": {
			`        <bpmn:assignment id="as1">
          <bpmn:from language="xpath">/a/b</bpmn:from>
          <bpmn:to>order</bpmn:to>
        </bpmn:assignment>`,
			"as1",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			err := shapedStartErr(t, tc.shape)
			if err == nil {
				t.Fatal("an unrunnable expression must be refused")
			}

			// The refusal names the element that CARRIES the expression:
			// the association for a transformation, the assignment for a
			// from — a modeler edits the one the message points at.
			if !strings.Contains(err.Error(), tc.names) {
				t.Errorf("error = %v, want it naming %q", err, tc.names)
			}
		})
	}
}

// TestAssignmentToMustBeDeclared is FR-7: an <assignment> whose <to> is
// empty has nowhere to write, and the refusal says so.
func TestAssignmentToMustBeDeclared(t *testing.T) {
	err := shapedStartErr(t, `        <bpmn:assignment id="as1">
          <bpmn:from language="gobpm:lite">s2-out</bpmn:from>
          <bpmn:to></bpmn:to>
        </bpmn:assignment>`)
	if err == nil || !strings.Contains(err.Error(), "<to>") {
		t.Fatalf("error = %v, want the missing-to refusal", err)
	}
}

// TestEmptyTransformationIsNoShape: a <transformation> with no body
// declares nothing, so the association stays a plain copy rather than
// carrying an expression that evaluates to nothing.
func TestEmptyTransformationIsNoShape(t *testing.T) {
	a := shapedStart(t, "",
		`        <bpmn:transformation language="gobpm:lite"></bpmn:transformation>`)

	if a.Transformation() != nil {
		t.Error("Transformation() = non-nil for an empty body")
	}
}

// TestAssignmentSkipsForeignChildren: a vendor element inside an
// <assignment> is skipped like any other foreign namespace, and the
// assignment still maps.
func TestAssignmentSkipsForeignChildren(t *testing.T) {
	a := shapedStart(t, "",
		`        <bpmn:assignment id="as1">
          <ex:hint kind="ignored"/>
          <bpmn:from language="gobpm:lite">s2-out</bpmn:from>
          <bpmn:to>order</bpmn:to>
        </bpmn:assignment>`)

	if len(a.Assignments()) != 1 {
		t.Fatalf("Assignments() = %v, want the mapped one", a.Assignments())
	}
}

// TestAssignmentNeedsAFrom is FR-7's other half: an <assignment> with no
// <from> has nothing to evaluate, and says so.
func TestAssignmentNeedsAFrom(t *testing.T) {
	err := shapedStartErr(t, `        <bpmn:assignment id="as1">
          <bpmn:to>order</bpmn:to>
        </bpmn:assignment>`)
	if err == nil || !strings.Contains(err.Error(), "<from>") {
		t.Fatalf("error = %v, want the missing-from refusal", err)
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
