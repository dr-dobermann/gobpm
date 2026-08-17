package bpmn

import (
	"strings"
	"testing"
)

// TestDataStoreReferenceImports is T-14 (FR-5): the reference imports
// carrying its dataStoreRef verbatim — the store itself is the engine
// registry's to supply, so the file need not declare it.
func TestDataStoreReferenceImports(t *testing.T) {
	res, err := importEventDoc(t, dataDoc(idOrder,
		`    <bpmn:dataStoreReference id="dsr1" name="orders"
                             dataStoreRef="ordersDB" itemSubjectRef="idOrder"/>`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	dsrs := res.Processes[0].DataStoreReferences()
	if len(dsrs) != 1 {
		t.Fatalf("DataStoreReferences() has %d entries, want 1", len(dsrs))
	}

	dsr := dsrs[0]

	if dsr.Name() != "orders" || dsr.DataStoreRef() != "ordersDB" {
		t.Errorf("got %q → %q, want %q → %q",
			dsr.Name(), dsr.DataStoreRef(), "orders", "ordersDB")
	}

	if got := dsr.ItemDefinition().ID(); got != "idOrder" {
		t.Errorf("item id = %q, want %q", got, "idOrder")
	}

	if len(res.Dropped) != 0 {
		t.Errorf("Dropped = %v, want nothing — an undeclared store is the "+
			"registry's business, not a loss", res.Dropped)
	}
}

// TestDataStoreIsReported is T-15 (§4.5): a definitions-level <dataStore>
// builds nothing and is reported as the host obligation it is, carrying
// the advisory capacity the file declared.
func TestDataStoreIsReported(t *testing.T) {
	res, err := importEventDoc(t, dataDoc(
		`  <bpmn:dataStore id="ordersDB" name="Orders" capacity="1000"
                  isUnlimited="false"/>`, ""))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	if len(res.Dropped) != 1 {
		t.Fatalf("Dropped = %v, want the one store entry", res.Dropped)
	}

	d := res.Dropped[0]
	if d.Element != "ordersDB" || d.Construct != tagDataStore {
		t.Errorf("Dropped = %+v, want the store under its own id", d)
	}

	for _, want := range []string{"register a store", "1000", "isUnlimited=false"} {
		if !strings.Contains(d.Reason, want) {
			t.Errorf("Reason = %q, want it to carry %q", d.Reason, want)
		}
	}
}

// TestBareDataStoreReportsNoCapacity: a store declaring neither capacity
// nor isUnlimited is reported without inventing either (NFR-3).
func TestBareDataStoreReportsNoCapacity(t *testing.T) {
	res, err := importEventDoc(t, dataDoc(
		`  <bpmn:dataStore id="ordersDB"/>`, ""))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	if got := res.Dropped[0].Reason; strings.Contains(got, "capacity") ||
		strings.Contains(got, "isUnlimited") {
		t.Errorf("Reason = %q speaks of attributes the file never wrote", got)
	}
}

// TestWorkedDataExample is SRD-089.F §4a without its <property> (that is
// M5): one collapsed object, one store reference, and EXACTLY three
// losses — two means one is silent, four means something implemented is
// being reported.
func TestWorkedDataExample(t *testing.T) {
	res, err := importEventDoc(t, dataDoc(
		`  <bpmn:import importType="http://www.w3.org/2001/XMLSchema"
               location="order.xsd" namespace="http://example.com/schema"/>
  <bpmn:itemDefinition id="idOrder" structureRef="ex:PurchaseOrder"/>
  <bpmn:dataStore id="ordersDB" name="Orders" capacity="1000"/>`,
		`    <bpmn:dataObject id="do1" name="order" itemSubjectRef="idOrder"/>
    <bpmn:dataObjectReference id="dor1" dataObjectRef="do1">
      <bpmn:dataState name="Approved"/>
    </bpmn:dataObjectReference>
    <bpmn:dataStoreReference id="dsr1" name="orders" dataStoreRef="ordersDB"
                             itemSubjectRef="idOrder"/>`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	proc := res.Processes[0]

	if dos := proc.DataObjects(); len(dos) != 1 || dos[0].Name() != "order" {
		t.Errorf("DataObjects() = %v, want the one collapsed object", dos)
	}

	if dsrs := proc.DataStoreReferences(); len(dsrs) != 1 ||
		dsrs[0].DataStoreRef() != "ordersDB" {
		t.Errorf("DataStoreReferences() = %v, want orders → ordersDB", dsrs)
	}

	got := map[string]bool{}
	for _, d := range res.Dropped {
		got[d.Element+"/"+d.Construct] = true
	}

	if len(res.Dropped) != 3 || !got["idOrder/"+attrStructureRef] ||
		!got["ordersDB/"+tagDataStore] || !got["dor1/"+tagDataState] {
		t.Errorf("Dropped = %v, want exactly the three §4a names", res.Dropped)
	}
}

// TestDataStoreReferenceInsideASubProcess: the reference lands on its
// container, through the same table a data object uses.
func TestDataStoreReferenceInsideASubProcess(t *testing.T) {
	res, err := importEventDoc(t, subProcessDoc(innerGraph+`
      <bpmn:dataStoreReference id="dsr1" name="orders" dataStoreRef="S"/>`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	sub := containerOf(t, nodeByID(t, res, "sub"))

	if dsrs := sub.DataStoreReferences(); len(dsrs) != 1 {
		t.Errorf("sub-process store references = %v, want dsr1", dsrs)
	}

	if dsrs := res.Processes[0].DataStoreReferences(); len(dsrs) != 0 {
		t.Errorf("the process holds %d store references, want 0", len(dsrs))
	}
}

// TestUnnamedDataStoreReferenceTakesItsID: fallbackName serves the store
// reference as it serves every node.
func TestUnnamedDataStoreReferenceTakesItsID(t *testing.T) {
	res, err := importEventDoc(t, dataDoc("",
		`    <bpmn:dataStoreReference id="dsr1" dataStoreRef="S"/>`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	if got := res.Processes[0].DataStoreReferences()[0].Name(); got != "dsr1" {
		t.Errorf("Name() = %q, want the id", got)
	}
}

// TestDataStateOnAStoreReferenceIsReported is T-13's store half (§4.7).
func TestDataStateOnAStoreReferenceIsReported(t *testing.T) {
	res, err := importEventDoc(t, dataDoc("",
		`    <bpmn:dataStoreReference id="dsr1" dataStoreRef="S">
      <bpmn:dataState name="Loaded"/>
    </bpmn:dataStoreReference>`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	if len(res.Dropped) != 1 || res.Dropped[0].Element != "dsr1" ||
		res.Dropped[0].Construct != tagDataState {
		t.Fatalf("Dropped = %v, want dsr1's dataState", res.Dropped)
	}
}

// TestEmptyDataStoreRefIsRefused: the one thing verbatim cannot excuse
// is no reference at all — and the check is the model's, not a
// converter-local copy (NFR-4), so the converter's wrap attaches the id.
func TestEmptyDataStoreRefIsRefused(t *testing.T) {
	_, err := importEventDoc(t, dataDoc("",
		`    <bpmn:dataStoreReference id="dsr1" name="orders"/>`))
	if err == nil || !strings.Contains(err.Error(), "non-empty dataStoreRef") {
		t.Fatalf("error = %v, want the model's empty-ref refusal", err)
	}

	if !strings.Contains(err.Error(), `"dsr1"`) {
		t.Errorf("error = %v, want the element's id attached", err)
	}
}

// TestStoreReferenceItemRefErrors: itemFor serves the store reference
// with the same three refusals it gives a data object.
func TestStoreReferenceItemRefErrors(t *testing.T) {
	_, err := importEventDoc(t, dataDoc("",
		`    <bpmn:dataStoreReference id="dsr1" dataStoreRef="S"
                             itemSubjectRef="nothing"/>`))
	if err == nil || !strings.Contains(err.Error(), "no such element is declared") {
		t.Fatalf("error = %v, want the dangling-item refusal", err)
	}
}

// TestDataStoreInsideAProcessIsRefused is T-25a: an importable element
// keeps its sections row for the contexts that do not claim it, and a
// <dataStore> written among flow elements is exactly that reader.
func TestDataStoreInsideAProcessIsRefused(t *testing.T) {
	_, err := importEventDoc(t, dataDoc("",
		`    <bpmn:dataStore id="ordersDB"/>`))
	if err == nil {
		t.Fatal("a <dataStore> among flow elements must be refused")
	}

	if !strings.Contains(err.Error(), "§10.4.1") {
		t.Errorf("error = %v, want the §10.4.1 pin", err)
	}
}

// TestDataStoreWithoutIDIsRefused: the id is what every dataStoreRef
// names and what the host must register under, so a declaration without
// one obliges nobody to anything.
func TestDataStoreWithoutIDIsRefused(t *testing.T) {
	_, err := importEventDoc(t, dataDoc(
		`  <bpmn:dataStore name="Orders"/>`, ""))
	if err == nil || !strings.Contains(err.Error(), "has no id") {
		t.Fatalf("error = %v, want the missing-id refusal", err)
	}
}

// TestDuplicateDataStoreIDIsRefused: a store shares the definitions-level
// id space with the catalog and the item definitions.
func TestDuplicateDataStoreIDIsRefused(t *testing.T) {
	_, err := importEventDoc(t, dataDoc(
		`  <bpmn:message id="dup" name="M"/>
  <bpmn:dataStore id="dup"/>`, ""))
	if err == nil || !strings.Contains(err.Error(), "duplicate id") {
		t.Fatalf("error = %v, want the duplicate-id refusal", err)
	}
}

// TestDataStoreDocumentationIsTolerated: a RootElement's content model —
// the docs have no model object to land on and go down with the element,
// but they must not break the parse.
func TestDataStoreDocumentationIsTolerated(t *testing.T) {
	res, err := importEventDoc(t, dataDoc(
		`  <bpmn:dataStore id="ordersDB">
    <bpmn:documentation>the orders database</bpmn:documentation>
  </bpmn:dataStore>`, ""))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	if len(res.Dropped) != 1 {
		t.Errorf("Dropped = %v, want just the store entry", res.Dropped)
	}
}

// TestTruncatedDataStore covers the body reader's token-error path.
func TestTruncatedDataStore(t *testing.T) {
	_, err := importEventDoc(t, `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:dataStore id="ordersDB">
    <bpmn:documentation>half a`)
	if err == nil {
		t.Fatal("a document ending inside a <dataStore> must fail")
	}
}
