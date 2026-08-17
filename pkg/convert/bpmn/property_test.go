package bpmn

import (
	"context"
	"strings"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
)

// propDoc places properties ahead of the process's flow elements, where
// BPMN serializes them — dataDoc appends its body AFTER the graph, which
// is exactly the T-18 refusal.
func propDoc(decls, props string) string {
	return `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:xsd="http://www.w3.org/2001/XMLSchema"
                  xmlns:ex="http://example.com/schema">
` + decls + `
  <bpmn:process id="P" name="P">
` + props + `
    <bpmn:startEvent id="s1"/>
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`
}

// TestPropertyOnAProcess is T-16 (FR-6): a leading <property> reaches the
// process as the construction option it must be (§4.6).
func TestPropertyOnAProcess(t *testing.T) {
	res, err := importEventDoc(t, propDoc(
		`  <bpmn:itemDefinition id="idCount" structureRef="xsd:int"/>`,
		`    <bpmn:property id="p1" name="retries" itemSubjectRef="idCount"/>`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	props := res.Processes[0].Properties()
	if len(props) != 1 || props[0].Name() != "retries" {
		t.Fatalf("Properties() = %v, want retries", props)
	}

	// The §4.1 decision at the property level: an xsd:int is a float64
	// zero, because that is the one numeric type the engine can write.
	if got := props[0].ItemDefinition().Structure().Get(context.Background()); got != float64(0) {
		t.Errorf("value = %#v (%T), want float64 zero", got, got)
	}
}

// TestPropertyResolvesAnItemDeclaredAfterTheProcess is why the process is
// built in pass 2 (§4.6): BPMN orders root elements freely, so the
// <itemDefinition> a leading property names may follow the </process>.
func TestPropertyResolvesAnItemDeclaredAfterTheProcess(t *testing.T) {
	res, err := importEventDoc(t, `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:xsd="http://www.w3.org/2001/XMLSchema">
  <bpmn:process id="P" name="P">
    <bpmn:property id="p1" name="retries" itemSubjectRef="idCount"/>
    <bpmn:startEvent id="s1"/>
  </bpmn:process>
  <bpmn:itemDefinition id="idCount" structureRef="xsd:int"/>
</bpmn:definitions>`)
	if err != nil {
		t.Fatalf("import: %v — a document is free to declare its items last", err)
	}

	if props := res.Processes[0].Properties(); len(props) != 1 {
		t.Fatalf("Properties() = %v, want the one property", props)
	}
}

// TestPropertyOnATaskAndAnEvent is T-17: the other two owners BPMN allows,
// through the node-body funnel.
func TestPropertyOnATaskAndAnEvent(t *testing.T) {
	res, err := importEventDoc(t, `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:xsd="http://www.w3.org/2001/XMLSchema">
  <bpmn:itemDefinition id="idCount" structureRef="xsd:int"/>
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1">
      <bpmn:property id="pe" name="seen" itemSubjectRef="idCount"/>
    </bpmn:startEvent>
    <bpmn:task id="t1" name="Work">
      <bpmn:property id="pt" name="tries" itemSubjectRef="idCount"/>
    </bpmn:task>
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="t1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t1" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`)
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	task, ok := nodeByID(t, res, "t1").(*activities.ManualTask)
	if !ok {
		t.Fatalf("t1 is not a *activities.ManualTask")
	}

	if props := task.Properties(); len(props) != 1 || props[0].Name() != "tries" {
		t.Errorf("task Properties() = %v, want tries", props)
	}

	start, ok := nodeByID(t, res, "s1").(*events.StartEvent)
	if !ok {
		t.Fatalf("s1 is not a *events.StartEvent")
	}

	if props := start.Properties(); len(props) != 1 || props[0].Name() != "seen" {
		t.Errorf("event Properties() = %v, want seen", props)
	}
}

// TestPropertyAfterFlowElementsIsRefused is T-18: the option cannot reach
// a process whose construction ingredients are already sealed, and
// dropping it silently is what the feedback contract exists to prevent —
// the same two halves as a late <laneSet> (§4.6).
func TestPropertyAfterFlowElementsIsRefused(t *testing.T) {
	_, err := importEventDoc(t, dataDoc("",
		`    <bpmn:property id="p1" name="late"/>`))
	if err == nil || !strings.Contains(err.Error(), "<property> after its flow") {
		t.Fatalf("error = %v, want the late-property refusal", err)
	}
}

// TestDataObjectNameCollidesWithAProperty is T-19: name uniqueness across
// a scope is the model's check (NFR-4), and it can only fire if the
// property exists before the data elements are added — the §4.6 build
// order under test.
func TestDataObjectNameCollidesWithAProperty(t *testing.T) {
	_, err := importEventDoc(t, propDoc("",
		`    <bpmn:property id="p1" name="order"/>
    <bpmn:dataObject id="do1" name="order"/>`))
	if err == nil || !strings.Contains(err.Error(), "collides with a process property") {
		t.Fatalf("error = %v, want the model's collision message", err)
	}
}

// TestPropertyOverAnUnresolvableItem is T-6: the §4.2 rule reaches
// properties through the same itemFor — a fillable empty record, never a
// value-less item the model would reject.
func TestPropertyOverAnUnresolvableItem(t *testing.T) {
	res, err := importEventDoc(t, propDoc(
		`  <bpmn:itemDefinition id="idOrder" structureRef="ex:PurchaseOrder"/>`,
		`    <bpmn:property id="p1" name="order" itemSubjectRef="idOrder"/>`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	prop := res.Processes[0].Properties()[0]

	rec, ok := prop.ItemDefinition().Structure().(*values.Record)
	if !ok {
		t.Fatalf("structure is %T, want a *values.Record", prop.ItemDefinition().Structure())
	}

	if err := rec.SetField(
		context.Background(), "total", values.NewVariable(42.0)); err != nil {
		t.Errorf("SetField: %v — the record must be fillable", err)
	}
}

// TestUntypedPropertyImports: itemSubjectRef is 0..1 on a property as on
// any item-aware element; an empty record stands in (§4.2).
func TestUntypedPropertyImports(t *testing.T) {
	res, err := importEventDoc(t, propDoc("",
		`    <bpmn:property id="p1" name="note"/>`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	if props := res.Processes[0].Properties(); len(props) != 1 {
		t.Fatalf("Properties() = %v, want the one untyped property", props)
	}
}

// TestUnnamedPropertyTakesItsID: fallbackName serves a property as it
// serves every named element.
func TestUnnamedPropertyTakesItsID(t *testing.T) {
	res, err := importEventDoc(t, propDoc("",
		`    <bpmn:property id="p1"/>`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	if got := res.Processes[0].Properties()[0].Name(); got != "p1" {
		t.Errorf("Name() = %q, want the id", got)
	}
}

// TestDataStateOnAPropertyIsReported is T-13's property half (§4.7).
func TestDataStateOnAPropertyIsReported(t *testing.T) {
	res, err := importEventDoc(t, propDoc("",
		`    <bpmn:property id="p1" name="note">
      <bpmn:dataState name="Fresh"/>
    </bpmn:property>`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	if len(res.Dropped) != 1 || res.Dropped[0].Element != "p1" ||
		res.Dropped[0].Construct != tagDataState {
		t.Fatalf("Dropped = %v, want p1's dataState", res.Dropped)
	}
}

// TestReservedCharacterPropertyName: the model's name rule, with the
// file's element id attached — T-19a's shape on the property path.
func TestReservedCharacterPropertyName(t *testing.T) {
	_, err := importEventDoc(t, propDoc("",
		`    <bpmn:property id="p1" name="order.v2"/>`))
	if err == nil {
		t.Fatal("a property name with a reserved character must be refused")
	}

	for _, want := range []string{`"p1"`, "reserved character"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to carry %q", err, want)
		}
	}
}

// TestPropertyItemRefErrors: itemFor's refusals reach a property with the
// property named as the referring element.
func TestPropertyItemRefErrors(t *testing.T) {
	_, err := importEventDoc(t, propDoc("",
		`    <bpmn:property id="p1" name="note" itemSubjectRef="ghost"/>`))
	if err == nil || !strings.Contains(err.Error(), "no such element is declared") {
		t.Fatalf("error = %v, want the dangling-item refusal", err)
	}

	if !strings.Contains(err.Error(), `property "p1"`) {
		t.Errorf("error = %v, want the property named as the referrer", err)
	}
}

// TestPropertyOnAGateway: BPMN's three owners are exactly the model types
// that take the option, so a fourth owner refuses it itself — with the
// node's id wrapped around the model's own message (FR-6).
func TestPropertyOnAGateway(t *testing.T) {
	_, err := importEventDoc(t, propDoc("",
		`    <bpmn:exclusiveGateway id="g1">
      <bpmn:property id="p1" name="stray"/>
    </bpmn:exclusiveGateway>
    <bpmn:sequenceFlow id="f2" sourceRef="s1" targetRef="g1"/>
    <bpmn:sequenceFlow id="f3" sourceRef="g1" targetRef="e1"/>`))
	if err == nil || !strings.Contains(err.Error(), `"g1"`) {
		t.Fatalf("error = %v, want the gateway's refusal under its id", err)
	}
}

// TestPropertyIDJoinsTheLedger: a property shares the document's one id
// space (§4.11).
func TestPropertyIDJoinsTheLedger(t *testing.T) {
	_, err := importEventDoc(t, propDoc("",
		`    <bpmn:property id="s1" name="stray"/>`))
	if err == nil || !strings.Contains(err.Error(), "duplicate id") {
		t.Fatalf("error = %v, want the ledger's refusal", err)
	}
}

// TestProcessIDJoinsTheLedger: the process's own id was the one id the
// ledger did not see when it landed (§4.11).
func TestProcessIDJoinsTheLedger(t *testing.T) {
	_, err := importEventDoc(t, propDoc("",
		`    <bpmn:task id="P" name="shadow"/>`))
	if err == nil || !strings.Contains(err.Error(), "duplicate id") {
		t.Fatalf("error = %v, want the ledger's refusal", err)
	}
}

// TestPropertyWithoutIDIsRefused: both owners' parse paths — the process
// buffer and the node body — refuse a property that nothing could ever
// refer to.
func TestPropertyWithoutIDIsRefused(t *testing.T) {
	tests := map[string]string{
		"on the process": propDoc("", `    <bpmn:property name="stray"/>`),
		"on a task": propDoc("", `    <bpmn:task id="t1" name="T">
      <bpmn:property name="stray"/>
    </bpmn:task>`),
	}

	for name, doc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := importEventDoc(t, doc)
			if err == nil || !strings.Contains(err.Error(), "has no id") {
				t.Fatalf("error = %v, want the missing-id refusal", err)
			}
		})
	}
}

// TestTwoPropertiesSharingAnID: the second declaration is the one the
// ledger turns away (§4.11).
func TestTwoPropertiesSharingAnID(t *testing.T) {
	_, err := importEventDoc(t, propDoc("",
		`    <bpmn:property id="p1" name="a"/>
    <bpmn:property id="p1" name="b"/>`))
	if err == nil || !strings.Contains(err.Error(), "duplicate id") {
		t.Fatalf("error = %v, want the ledger's refusal", err)
	}
}

// TestProcessReusingADeclaredIDIsRefused covers the other direction of
// TestProcessIDJoinsTheLedger: here the process is the SECOND declarer.
func TestProcessReusingADeclaredIDIsRefused(t *testing.T) {
	_, err := importEventDoc(t, `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:itemDefinition id="P"/>
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
  </bpmn:process>
</bpmn:definitions>`)
	if err == nil || !strings.Contains(err.Error(), "duplicate id") {
		t.Fatalf("error = %v, want the ledger's refusal", err)
	}
}

// TestNodePropertyBuildError: a node-side property that cannot build —
// its itemSubjectRef dangles — surfaces through the node funnel.
func TestNodePropertyBuildError(t *testing.T) {
	_, err := importEventDoc(t, propDoc("",
		`    <bpmn:task id="t1" name="T">
      <bpmn:property id="pt" name="x" itemSubjectRef="ghost"/>
    </bpmn:task>`))
	if err == nil || !strings.Contains(err.Error(), "no such element is declared") {
		t.Fatalf("error = %v, want the dangling-item refusal", err)
	}
}

// TestTruncatedProperty covers the body reader's token-error path on the
// property's side of the shared reader.
func TestTruncatedProperty(t *testing.T) {
	_, err := importEventDoc(t, `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="P" name="P">
    <bpmn:property id="p1" name="note">`)
	if err == nil {
		t.Fatal("a document ending inside a <property> must fail")
	}
}

// TestPropertyDocumentation: the one decorating child, through the same
// body reader every data element uses.
func TestPropertyDocumentation(t *testing.T) {
	res, err := importEventDoc(t, propDoc("",
		`    <bpmn:property id="p1" name="note">
      <bpmn:documentation>how often we retried</bpmn:documentation>
    </bpmn:property>`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	docs := res.Processes[0].Properties()[0].Docs()
	if len(docs) != 1 || docs[0].Text() != "how often we retried" {
		t.Errorf("Docs() = %v, want the file's one line", docs)
	}
}

// TestPropertyValueIsWritable closes the §4.1 loop end to end: the
// property the file typed xsd:int accepts the float64 the engine writes.
func TestPropertyValueIsWritable(t *testing.T) {
	res, err := importEventDoc(t, propDoc(
		`  <bpmn:itemDefinition id="idCount" structureRef="xsd:int"/>`,
		`    <bpmn:property id="p1" name="retries" itemSubjectRef="idCount"/>`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	v := res.Processes[0].Properties()[0].ItemDefinition().Structure()
	if err := v.Update(context.Background(), float64(3)); err != nil {
		t.Fatalf("Update(float64) = %v; an imported property the engine "+
			"cannot write is unusable", err)
	}
}
