package bpmn

import (
	"context"
	"strings"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
)

// dataDoc wraps definitions-level declarations and a process body around
// the smallest graph the model runs, so every test here starts from a
// document that imports but for the element under test.
func dataDoc(decls, body string) string {
	return `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:xsd="http://www.w3.org/2001/XMLSchema"
                  xmlns:ex="http://example.com/schema"
                  xmlns:camunda="http://camunda.org/schema/1.0/bpmn">
` + decls + `
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="e1"/>
` + body + `
  </bpmn:process>
</bpmn:definitions>`
}

// idOrder is the one item declaration most tests here share.
const idOrder = `  <bpmn:itemDefinition id="idOrder" structureRef="xsd:string"/>`

// TestDataObjectImportsOntoItsProcess is T-8 (FR-3): a <dataObject> lands
// on the process that holds it, named by its name and carrying the item
// its itemSubjectRef names.
func TestDataObjectImportsOntoItsProcess(t *testing.T) {
	res, err := importEventDoc(t, dataDoc(idOrder,
		`    <bpmn:dataObject id="do1" name="order" itemSubjectRef="idOrder"/>`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	dos := res.Processes[0].DataObjects()
	if len(dos) != 1 {
		t.Fatalf("DataObjects() has %d entries, want 1", len(dos))
	}

	do := dos[0]

	if do.Name() != "order" || do.ID() != "do1" {
		t.Errorf("got %q(%s), want %q(%s)", do.Name(), do.ID(), "order", "do1")
	}

	if got := do.ItemDefinition().ID(); got != "idOrder" {
		t.Errorf("item id = %q, want %q — associations match on it", got, "idOrder")
	}

	if got := do.ItemDefinition().Structure().Get(context.Background()); got != "" {
		t.Errorf("structure = %#v, want the typed zero %q", got, "")
	}

	if len(res.Dropped) != 0 {
		t.Errorf("Dropped = %v, want nothing — everything here maps", res.Dropped)
	}
}

// TestEachDataObjectGetsItsOwnItemCopy pins the copyItem decision: two
// elements naming one <itemDefinition> must not share a structure, or
// writing one variable would write the other — the structure IS the value
// (ADR-010).
func TestEachDataObjectGetsItsOwnItemCopy(t *testing.T) {
	res, err := importEventDoc(t, dataDoc(idOrder,
		`    <bpmn:dataObject id="do1" name="order" itemSubjectRef="idOrder"/>
    <bpmn:dataObject id="do2" name="invoice" itemSubjectRef="idOrder"/>`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	dos := res.Processes[0].DataObjects()
	if len(dos) != 2 {
		t.Fatalf("DataObjects() has %d entries, want 2", len(dos))
	}

	ctx := context.Background()

	if err := dos[0].ItemDefinition().Structure().Update(ctx, "42"); err != nil {
		t.Fatalf("Update: %v", err)
	}

	if got := dos[1].ItemDefinition().Structure().Get(ctx); got != "" {
		t.Errorf("the other object reads %#v after the write; the two share "+
			"one structure and are one variable", got)
	}
}

// TestUnnamedDataObjectTakesItsID is T-9: a <dataObject> with no name is
// named by its id, as .A's fallbackName does for nodes.
func TestUnnamedDataObjectTakesItsID(t *testing.T) {
	res, err := importEventDoc(t, dataDoc("",
		`    <bpmn:dataObject id="do1"/>`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	if got := res.Processes[0].DataObjects()[0].Name(); got != "do1" {
		t.Errorf("Name() = %q, want the id", got)
	}
}

// TestUntypedDataObjectGetsAnEmptyRecord covers itemFor's other half:
// BPMN permits itemSubjectRef at 0..1, every constructor refuses a nil
// item, and nothing was dropped — so an untyped object carries an empty
// record and says nothing.
func TestUntypedDataObjectGetsAnEmptyRecord(t *testing.T) {
	res, err := importEventDoc(t, dataDoc("",
		`    <bpmn:dataObject id="do1" name="order"/>`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	do := res.Processes[0].DataObjects()[0]

	if _, ok := do.ItemDefinition().Structure().(*values.Record); !ok {
		t.Errorf("structure is %T, want an empty record",
			do.ItemDefinition().Structure())
	}

	if len(res.Dropped) != 0 {
		t.Errorf("Dropped = %v, want nothing — the file asserted no type",
			res.Dropped)
	}
}

// TestDataObjectInsideASubProcess is T-10 (SRD-089.E §4.1): the object
// lands on the sub-process that holds it, not on the process — through
// the same table, since a container's children dispatch via
// processParsers.
func TestDataObjectInsideASubProcess(t *testing.T) {
	res, err := importEventDoc(t, subProcessDoc(innerGraph+`
      <bpmn:dataObject id="do1" name="order"/>`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	sub := containerOf(t, nodeByID(t, res, "sub"))

	if dos := sub.DataObjects(); len(dos) != 1 || dos[0].ID() != "do1" {
		t.Errorf("sub-process data objects = %v, want do1", dos)
	}

	if dos := res.Processes[0].DataObjects(); len(dos) != 0 {
		t.Errorf("the process holds %d data objects, want 0 — the object "+
			"belongs to its container", len(dos))
	}
}

// TestReferencesCollapseToTheirObject is T-11 (FR-4, SAD-001 §14.1 rules
// 1 and 3): a reference contributes no element of its own, and every
// reference to one object collapses to that single object.
func TestReferencesCollapseToTheirObject(t *testing.T) {
	res, err := importEventDoc(t, dataDoc(idOrder,
		`    <bpmn:dataObject id="do1" name="order" itemSubjectRef="idOrder"/>
    <bpmn:dataObjectReference id="dor1" dataObjectRef="do1"/>
    <bpmn:dataObjectReference id="dor2" dataObjectRef="do1"/>`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	dos := res.Processes[0].DataObjects()
	if len(dos) != 1 || dos[0].ID() != "do1" {
		t.Fatalf("DataObjects() = %v, want the one object both references "+
			"name", dos)
	}

	if len(res.Dropped) != 0 {
		t.Errorf("Dropped = %v, want nothing — a stateless reference loses "+
			"nothing by collapsing", res.Dropped)
	}
}

// TestDataObjectReferenceErrors covers the three ways a reference can
// fail to name its object: T-12's dangling ref, a target of the wrong
// kind, and no target at all.
func TestDataObjectReferenceErrors(t *testing.T) {
	tests := map[string]struct {
		ref  string
		want string
	}{
		"dangling ref": {
			ref:  `dataObjectRef="nope"`,
			want: "no such element is declared",
		},
		"wrong kind": {
			ref:  `dataObjectRef="s1"`,
			want: `"s1" is a startEvent`,
		},
		"no ref at all": {
			ref:  "",
			want: "names no dataObjectRef",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := importEventDoc(t, dataDoc("",
				`    <bpmn:dataObjectReference id="dor1" `+tc.ref+`/>`))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

// TestDataStateIsReported is T-13 (§4.7): a <dataState> is reported and
// never mapped — the element imports in the model's default state,
// because gobpm's readiness pair is not the document's to set.
func TestDataStateIsReported(t *testing.T) {
	res, err := importEventDoc(t, dataDoc(idOrder,
		`    <bpmn:dataObject id="do1" name="order" itemSubjectRef="idOrder"/>
    <bpmn:dataObjectReference id="dor1" dataObjectRef="do1">
      <bpmn:dataState name="Approved"/>
    </bpmn:dataObjectReference>`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	if len(res.Dropped) != 1 {
		t.Fatalf("Dropped = %v, want the one dataState entry", res.Dropped)
	}

	d := res.Dropped[0]
	if d.Element != "dor1" || d.Construct != tagDataState {
		t.Errorf("Dropped = %+v, want dor1's dataState", d)
	}

	do := res.Processes[0].DataObjects()[0]
	if got := do.State().Name(); got != data.StateUnavailable {
		t.Errorf("State() = %q, want the model's default %q",
			got, data.StateUnavailable)
	}
}

// TestDataStateOnADataObjectIsReported completes T-13's kinds: BPMN gives
// the state to the reference, not the object (semantics/data.md:49), but
// a file writing one on the object is told the same thing every
// <dataState> is told — reported, never mapped, default state (§4.7).
func TestDataStateOnADataObjectIsReported(t *testing.T) {
	res, err := importEventDoc(t, dataDoc("",
		`    <bpmn:dataObject id="do1" name="order">
      <bpmn:dataState name="Draft"/>
    </bpmn:dataObject>`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	if len(res.Dropped) != 1 || res.Dropped[0].Element != "do1" ||
		res.Dropped[0].Construct != tagDataState {
		t.Fatalf("Dropped = %v, want do1's dataState", res.Dropped)
	}
}

// TestItemRefOnAReferenceIsReported: BPMN says a reference takes its type
// from the object it references (semantics/data.md:64), so a file typing
// one is told rather than silently obeyed or silently ignored.
func TestItemRefOnAReferenceIsReported(t *testing.T) {
	res, err := importEventDoc(t, dataDoc(idOrder,
		`    <bpmn:dataObject id="do1" name="order" itemSubjectRef="idOrder"/>
    <bpmn:dataObjectReference id="dor1" dataObjectRef="do1"
                              itemSubjectRef="idOrder"/>`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	if len(res.Dropped) != 1 || res.Dropped[0].Element != "dor1" ||
		res.Dropped[0].Construct != attrItemSubjectRef {
		t.Fatalf("Dropped = %v, want dor1's itemSubjectRef", res.Dropped)
	}
}

// TestDataObjectItemRefErrors covers itemFor's three refusals: an item
// that is not declared, an id that names a flow element, and an id that
// names a catalog object.
func TestDataObjectItemRefErrors(t *testing.T) {
	tests := map[string]struct {
		decls string
		ref   string
		want  string
	}{
		"not declared": {
			ref:  "nothing",
			want: "no such element is declared",
		},
		"a flow element": {
			ref:  "s1",
			want: `"s1" is a startEvent`,
		},
		"a catalog object": {
			decls: `  <bpmn:message id="m1" name="M"/>`,
			ref:   "m1",
			want:  `"m1" is a message`,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := importEventDoc(t, dataDoc(tc.decls,
				`    <bpmn:dataObject id="do1" name="order" itemSubjectRef="`+
					tc.ref+`"/>`))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

// TestReservedCharacterNameCarriesTheElementID is T-19a: the model
// refuses a name carrying a path character (data/name.go), and the
// converter's job is to attach the file's element id to that refusal.
func TestReservedCharacterNameCarriesTheElementID(t *testing.T) {
	_, err := importEventDoc(t, dataDoc("",
		`    <bpmn:dataObject id="do1" name="Order.v2"/>`))
	if err == nil {
		t.Fatal("a name with a reserved character must be refused")
	}

	for _, want := range []string{`"do1"`, "reserved character"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to carry %q", err, want)
		}
	}
}

// TestDuplicateDataObjectName covers the owner.Add path: name uniqueness
// in a container is the model's check (NFR-4), and the converter's wrap
// names the element it could not add.
func TestDuplicateDataObjectName(t *testing.T) {
	_, err := importEventDoc(t, dataDoc("",
		`    <bpmn:dataObject id="do1" name="order"/>
    <bpmn:dataObject id="do2" name="order"/>`))
	if err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("error = %v, want the model's duplicate-name refusal", err)
	}

	if !strings.Contains(err.Error(), `"do2"`) {
		t.Errorf("error = %v, want the second element's id attached", err)
	}
}

// TestDataObjectIDCollidesWithANode is T-22: a data element shares the
// document's one id space with the flow nodes.
func TestDataObjectIDCollidesWithANode(t *testing.T) {
	_, err := importEventDoc(t, dataDoc("",
		`    <bpmn:dataObject id="s1" name="order"/>`))
	if err == nil || !strings.Contains(err.Error(), "duplicate id") {
		t.Fatalf("error = %v, want the duplicate-id refusal", err)
	}
}

// TestDataObjectWithoutIDIsRefused: the id is what a dataObjectRef
// resolves against, so an object without one can be referred to by
// nothing.
func TestDataObjectWithoutIDIsRefused(t *testing.T) {
	_, err := importEventDoc(t, dataDoc("",
		`    <bpmn:dataObject name="order"/>`))
	if err == nil || !strings.Contains(err.Error(), "has no id") {
		t.Fatalf("error = %v, want the missing-id refusal", err)
	}
}

// TestDialectAttributeOnADataObject is T-23: the .D reporting funnel
// covers a data element's attributes like any node's.
func TestDialectAttributeOnADataObject(t *testing.T) {
	res, err := importEventDoc(t, dataDoc("",
		`    <bpmn:dataObject id="do1" name="order" camunda:topic="orders"/>`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	if len(res.Dropped) != 1 || res.Dropped[0].Element != "do1" ||
		res.Dropped[0].Construct != "camunda:topic" {
		t.Fatalf("Dropped = %v, want do1's camunda:topic", res.Dropped)
	}
}

// TestDataObjectDocumentation covers the one child besides <dataState>
// that means anything on a data element.
func TestDataObjectDocumentation(t *testing.T) {
	res, err := importEventDoc(t, dataDoc("",
		`    <bpmn:dataObject id="do1" name="order">
      <bpmn:documentation>the order under work</bpmn:documentation>
    </bpmn:dataObject>`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	docs := res.Processes[0].DataObjects()[0].Docs()
	if len(docs) != 1 || docs[0].Text() != "the order under work" {
		t.Errorf("Docs() = %v, want the file's one line", docs)
	}
}

// TestDataObjectItemCarriesItsImport: copyItem must carry the import
// binding across the copy — it records where the type came from (§4.8),
// and losing it on the copy would strip every data object of the one
// thing an exporter needs to write the declaration back.
func TestDataObjectItemCarriesItsImport(t *testing.T) {
	res, err := importEventDoc(t, dataDoc(
		`  <bpmn:import importType="http://www.w3.org/2001/XMLSchema"
               location="order.xsd" namespace="http://example.com/schema"/>
  <bpmn:itemDefinition id="idOrder" structureRef="ex:PurchaseOrder"/>`,
		`    <bpmn:dataObject id="do1" name="order" itemSubjectRef="idOrder"/>`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	imp := res.Processes[0].DataObjects()[0].ItemDefinition().Import()
	if imp == nil || imp.Location != "order.xsd" {
		t.Errorf("Import() = %+v, want the file's declaration to survive "+
			"the item copy", imp)
	}
}

// TestForeignNamespaceChildOfADataObjectIsSkipped: a converter cannot
// speak about a vocabulary it does not know, so a foreign child is
// skipped silently — the same rule the dialect tests pin for nodes.
func TestForeignNamespaceChildOfADataObjectIsSkipped(t *testing.T) {
	res, err := importEventDoc(t, dataDoc("",
		`    <bpmn:dataObject id="do1" name="order">
      <camunda:properties/>
    </bpmn:dataObject>`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	if len(res.Processes[0].DataObjects()) != 1 {
		t.Error("the object must import; its foreign child is not its problem")
	}
}

// TestForeignChildOfADataObject: an in-namespace element that does not
// belong in an item-aware element is refused through the disposition
// tables, not skipped.
func TestForeignChildOfADataObject(t *testing.T) {
	_, err := importEventDoc(t, dataDoc("",
		`    <bpmn:dataObject id="do1" name="order">
      <bpmn:task id="t1"/>
    </bpmn:dataObject>`))
	if err == nil {
		t.Fatal("a <task> inside a <dataObject> must be refused")
	}
}

// TestTruncatedDataObject covers the body reader's two token-error
// paths: a document ending inside the element's own body, and one ending
// inside a child the body reader handed off.
func TestTruncatedDataObject(t *testing.T) {
	tests := map[string]string{
		"inside the body":  `    <bpmn:dataObject id="do1" name="order">`,
		"inside its child": `    <bpmn:dataObject id="do1" name="order">
      <bpmn:documentation>half a`,
	}

	for name, tail := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := importEventDoc(t, `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="P" name="P">
`+tail)
			if err == nil {
				t.Fatal("a document ending inside a <dataObject> must fail")
			}
		})
	}
}
