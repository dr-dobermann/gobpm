package bpmn

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/convert"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
)

// itemDoc wraps definitions-level declarations around the smallest legal
// process, so every test here starts from a document the parser accepts.
func itemDoc(decls string) string {
	return `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:xsd="http://www.w3.org/2001/XMLSchema"
                  xmlns:ex="http://example.com/schema">
` + decls + `
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
  </bpmn:process>
</bpmn:definitions>`
}

// itemsOf runs pass 1 over doc and builds its item definitions, returning
// them with whatever the run reported.
//
// It stops one step short of a full import because the item map is pass-2
// state that no *convert.Result exposes yet — the data elements that carry
// it land in the next milestone.
func itemsOf(
	t *testing.T, doc string,
) (map[string]*data.ItemDefinition, []convert.Dropped) {
	t.Helper()

	p := newParser(context.Background(), strings.NewReader(doc))

	root, err := p.rootElement()
	if err != nil {
		t.Fatalf("rootElement: %v", err)
	}

	if err := p.parseDefinitions(root); err != nil {
		t.Fatalf("parseDefinitions: %v", err)
	}

	built, err := buildItems(p)
	if err != nil {
		t.Fatalf("buildItems: %v", err)
	}

	return built, p.dropped
}

// itemError runs the same path and returns the error it must fail with.
func itemError(t *testing.T, doc string) error {
	t.Helper()

	p := newParser(context.Background(), strings.NewReader(doc))

	root, err := p.rootElement()
	if err != nil {
		return err
	}

	if err := p.parseDefinitions(root); err != nil {
		return err
	}

	_, err = buildItems(p)

	return err
}

// structureOf returns the Go value an item definition carries.
func structureOf(t *testing.T, items map[string]*data.ItemDefinition, id string) any {
	t.Helper()

	idef, ok := items[id]
	if !ok {
		t.Fatalf("no itemDefinition %q; got %v", id, items)
	}

	return idef.Structure().Get(context.Background())
}

// TestXSDTypesMapToTypedZeroes is FR-1: a structureRef naming an XML
// Schema built-in yields the Go zero value of the type gobpm stores it as.
func TestXSDTypesMapToTypedZeroes(t *testing.T) {
	tests := map[string]any{
		"xsd:string":           "",
		"xsd:normalizedString": "",
		"xsd:token":            "",
		"xsd:anyURI":           "",
		"xsd:QName":            "",
		"xsd:boolean":          false,
		"xsd:int":              float64(0),
		"xsd:integer":          float64(0),
		"xsd:long":             float64(0),
		"xsd:short":            float64(0),
		"xsd:byte":             float64(0),
		"xsd:decimal":          float64(0),
		"xsd:double":           float64(0),
		"xsd:float":            float64(0),
		"xsd:dateTime":         time.Time{},
		"xsd:date":             time.Time{},
		"xsd:time":             time.Time{},
	}

	for ref, want := range tests {
		t.Run(ref, func(t *testing.T) {
			items, dropped := itemsOf(t, itemDoc(
				`  <bpmn:itemDefinition id="i1" structureRef="`+ref+`"/>`))

			if got := structureOf(t, items, "i1"); got != want {
				t.Errorf("structure = %#v (%T), want %#v (%T)",
					got, got, want, want)
			}

			if len(dropped) != 0 {
				t.Errorf("dropped = %v, want nothing — the type resolved",
					dropped)
			}
		})
	}
}

// TestIntegersAreFloat64 pins the §4.1 decision on its own, because it is
// the one row of the table a reader would "fix".
//
// A Variable[int] would reject every value the engine can produce: the
// evaluator unifies numbers to float64 and values.checkValue is a strict
// assertion. The item would import and fail on its first assignment, so
// the test asserts the write SUCCEEDS rather than merely that the type
// reads back as float64.
func TestIntegersAreFloat64(t *testing.T) {
	items, _ := itemsOf(t, itemDoc(
		`  <bpmn:itemDefinition id="i1" structureRef="xsd:int"/>`))

	v := items["i1"].Structure()

	if err := v.Update(context.Background(), float64(42)); err != nil {
		t.Fatalf("Update(float64) = %v; an xsd:int item that cannot take an "+
			"expression result is unusable", err)
	}

	if got := v.Get(context.Background()); got != float64(42) {
		t.Errorf("value = %#v, want 42.0", got)
	}
}

// TestUnresolvableStructureIsAnEmptyRecord is FR-2: a type in an external
// schema yields an empty record and one report — never a nil structure,
// which constructs fine and fails when a snapshot clones it.
func TestUnresolvableStructureIsAnEmptyRecord(t *testing.T) {
	items, dropped := itemsOf(t, itemDoc(
		`  <bpmn:itemDefinition id="i1" structureRef="ex:PurchaseOrder"/>`))

	idef := items["i1"]

	if idef.Structure() == nil {
		t.Fatal("structure is nil; an item that cannot be cloned imports " +
			"cleanly and dies at registration")
	}

	rec, ok := idef.Structure().(*values.Record)
	if !ok {
		t.Fatalf("structure is %T, want a *values.Record", idef.Structure())
	}

	if fields, ok := rec.Get(context.Background()).(map[string]any); !ok ||
		len(fields) != 0 {
		t.Errorf("record = %v, want empty", fields)
	}

	if len(dropped) != 1 || dropped[0].Construct != attrStructureRef {
		t.Fatalf("dropped = %v, want one structureRef entry", dropped)
	}

	if dropped[0].Element != "i1" {
		t.Errorf("Element = %q, want the item's own id", dropped[0].Element)
	}
}

// TestItemDefinitionWithoutStructureSaysNothing covers the other half of
// §4.2: BPMN makes structureRef optional, so an item that declares no type
// loses nothing and is not reported.
func TestItemDefinitionWithoutStructureSaysNothing(t *testing.T) {
	items, dropped := itemsOf(t, itemDoc(
		`  <bpmn:itemDefinition id="i1"/>`))

	if _, ok := items["i1"].Structure().(*values.Record); !ok {
		t.Errorf("structure is %T, want an empty record", items["i1"].Structure())
	}

	if len(dropped) != 0 {
		t.Errorf("dropped = %v, want nothing — the file asserted no structure",
			dropped)
	}
}

// TestCollectionImportsEmpty is FR-1's collection case: nothing is seeded
// into it (§4.3).
func TestCollectionImportsEmpty(t *testing.T) {
	items, dropped := itemsOf(t, itemDoc(
		`  <bpmn:itemDefinition id="i1" structureRef="xsd:string"
                       isCollection="true"/>`))

	idef := items["i1"]

	if !idef.IsCollection() {
		t.Error("IsCollection() = false, want true")
	}

	col, ok := idef.Structure().(data.Collection)
	if !ok {
		t.Fatalf("structure is %T, want a data.Collection", idef.Structure())
	}

	if got := col.Count(); got != 0 {
		t.Errorf("Count() = %d, want 0 — nothing may be seeded", got)
	}

	if len(dropped) != 0 {
		t.Errorf("dropped = %v, want nothing", dropped)
	}
}

// TestUnresolvableCollectionReportsBothLosses covers §4.3's second half:
// an Array needs an element type, and that is exactly what was unreadable,
// so the flag is reported beside the structure rather than silently kept.
func TestUnresolvableCollectionReportsBothLosses(t *testing.T) {
	items, dropped := itemsOf(t, itemDoc(
		`  <bpmn:itemDefinition id="i1" structureRef="ex:Orders"
                       isCollection="true"/>`))

	if items["i1"].IsCollection() {
		t.Error("IsCollection() = true, but the item is a single record")
	}

	got := map[string]bool{}
	for _, d := range dropped {
		got[d.Construct] = true
	}

	if len(dropped) != 2 || !got[attrStructureRef] || !got[attrIsCollection] {
		t.Errorf("dropped = %v, want one structureRef and one isCollection "+
			"entry — a host fixing one needs to see the other", dropped)
	}
}

// TestItemKindImports covers the third attribute, including the default
// BPMN sets when it is absent.
func TestItemKindImports(t *testing.T) {
	tests := map[string]struct {
		attr string
		want data.ItemKind
	}{
		"absent":      {attr: "", want: data.InformationKind},
		"Information": {attr: ` itemKind="Information"`, want: data.InformationKind},
		"Physical":    {attr: ` itemKind="Physical"`, want: data.PhysicalKind},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			items, _ := itemsOf(t, itemDoc(
				`  <bpmn:itemDefinition id="i1" structureRef="xsd:string"`+
					tc.attr+`/>`))

			if got := items["i1"].Kind(); got != tc.want {
				t.Errorf("Kind() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestUnknownItemKindIsRefused: the attribute is an enumeration of two, so
// a third value is a broken file rather than a lost datum.
func TestUnknownItemKindIsRefused(t *testing.T) {
	err := itemError(t, itemDoc(
		`  <bpmn:itemDefinition id="i1" itemKind="Chemical"/>`))
	if err == nil || !strings.Contains(err.Error(), "itemKind") {
		t.Fatalf("error = %v, want the itemKind refusal", err)
	}
}

// TestItemDefinitionDocumentation covers the one child the element can
// carry that reaches the model.
func TestItemDefinitionDocumentation(t *testing.T) {
	items, _ := itemsOf(t, itemDoc(
		`  <bpmn:itemDefinition id="i1" structureRef="xsd:string">
    <bpmn:documentation>the order id</bpmn:documentation>
  </bpmn:itemDefinition>`))

	docs := items["i1"].Docs()
	if len(docs) != 1 || docs[0].Text() != "the order id" {
		t.Errorf("Docs() = %v, want the file's one line", docs)
	}
}

// TestItemDefinitionIDIsDocumentUnique: an item definition shares the
// document's one id space with the catalog and the flow elements.
func TestItemDefinitionIDIsDocumentUnique(t *testing.T) {
	err := itemError(t, itemDoc(
		`  <bpmn:message id="dup" name="M"/>
  <bpmn:itemDefinition id="dup" structureRef="xsd:string"/>`))
	if err == nil || !strings.Contains(err.Error(), "duplicate id") {
		t.Fatalf("error = %v, want the duplicate-id refusal", err)
	}
}

// TestItemDefinitionWithoutIDIsRefused: the id is what every
// itemSubjectRef resolves against, so an item without one can be referred
// to by nothing.
func TestItemDefinitionWithoutIDIsRefused(t *testing.T) {
	err := itemError(t, itemDoc(
		`  <bpmn:itemDefinition structureRef="xsd:string"/>`))
	if err == nil || !strings.Contains(err.Error(), "has no id") {
		t.Fatalf("error = %v, want the missing-id refusal", err)
	}
}

// TestTruncatedItemDefinition covers the body reader's token-error path.
func TestTruncatedItemDefinition(t *testing.T) {
	err := itemError(t, `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:itemDefinition id="i1">
    <bpmn:documentation>half a`)
	if err == nil {
		t.Fatal("a document ending inside an <itemDefinition> must fail")
	}
}

// TestImportBindsToItsItemDefinition is FR-7: the declaration that was
// skipped until an itemDefinition could consult it now reaches the model.
func TestImportBindsToItsItemDefinition(t *testing.T) {
	items, dropped := itemsOf(t, itemDoc(
		`  <bpmn:import importType="http://www.w3.org/2001/XMLSchema"
               location="order.xsd" namespace="http://example.com/schema"/>
  <bpmn:itemDefinition id="i1" structureRef="ex:PurchaseOrder"/>`))

	imp := items["i1"].Import()
	if imp == nil {
		t.Fatal("Import() = nil, want the file's declaration")
	}

	if imp.Namespace != "http://example.com/schema" ||
		imp.Location != "order.xsd" ||
		imp.Type != "http://www.w3.org/2001/XMLSchema" {
		t.Errorf("Import() = %+v, want all three attributes", *imp)
	}

	// The structure is STILL unresolved — an import records where the type
	// came from, it does not fetch it.
	if len(dropped) != 1 || dropped[0].Construct != attrStructureRef {
		t.Errorf("dropped = %v, want the structureRef report to survive the "+
			"import binding", dropped)
	}
}

// TestImportDeclaredAfterItsItemDefinition covers why items are built in
// pass 2: BPMN orders root elements no more than flow elements.
func TestImportDeclaredAfterItsItemDefinition(t *testing.T) {
	items, _ := itemsOf(t, itemDoc(
		`  <bpmn:itemDefinition id="i1" structureRef="ex:PurchaseOrder"/>
  <bpmn:import importType="http://www.w3.org/2001/XMLSchema"
               location="order.xsd" namespace="http://example.com/schema"/>`))

	if items["i1"].Import() == nil {
		t.Error("Import() = nil; an <import> following the item that names " +
			"it must still bind")
	}
}

// TestUnreferencedImportIsReported: the file says a schema is needed and
// the imported definition has nowhere to record it.
func TestUnreferencedImportIsReported(t *testing.T) {
	_, dropped := itemsOf(t, itemDoc(
		`  <bpmn:import importType="http://www.w3.org/2001/XMLSchema"
               location="unused.xsd" namespace="http://example.com/other"/>`))

	if len(dropped) != 1 || dropped[0].Construct != tagImport {
		t.Fatalf("dropped = %v, want one import entry", dropped)
	}

	if dropped[0].Element != "http://example.com/other" {
		t.Errorf("Element = %q, want the namespace — an <import> carries no "+
			"id to name it by", dropped[0].Element)
	}
}

// TestImportWithoutNamespaceIsRefused: the namespace is the only thing
// that could bind it, so one without it can never be referred to.
func TestImportWithoutNamespaceIsRefused(t *testing.T) {
	err := itemError(t, itemDoc(
		`  <bpmn:import importType="x" location="y"/>`))
	if err == nil || !strings.Contains(err.Error(), "no namespace") {
		t.Fatalf("error = %v, want the missing-namespace refusal", err)
	}
}

// TestDuplicateImportNamespaceIsRefused: two imports of one namespace
// leave no way to say which an item definition meant.
func TestDuplicateImportNamespaceIsRefused(t *testing.T) {
	err := itemError(t, itemDoc(
		`  <bpmn:import importType="x" location="a.xsd" namespace="http://n"/>
  <bpmn:import importType="x" location="b.xsd" namespace="http://n"/>`))
	if err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("error = %v, want the duplicate-namespace refusal", err)
	}
}

// TestPrefixResolution covers how a structureRef's prefix decides whether
// the reference is an XML Schema built-in.
//
// The prefix is text inside an attribute VALUE, which the decoder does not
// resolve, so every one of these paths is the converter's own.
func TestPrefixResolution(t *testing.T) {
	tests := map[string]struct {
		ref string
		// resolved is true when the reference typed the item; false means
		// it fell through to the empty record.
		resolved bool
	}{
		"declared xsd prefix":      {ref: "xsd:string", resolved: true},
		"undeclared xs prefix":     {ref: "xs:string", resolved: true},
		"declared other namespace": {ref: "ex:string", resolved: false},
		"undeclared other prefix":  {ref: "zz:string", resolved: false},
		"no prefix at all":         {ref: "string", resolved: false},
		"unknown builtin":          {ref: "xsd:duration", resolved: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			items, _ := itemsOf(t, itemDoc(
				`  <bpmn:itemDefinition id="i1" structureRef="`+tc.ref+`"/>`))

			_, isRecord := items["i1"].Structure().(*values.Record)
			if isRecord == tc.resolved {
				t.Errorf("structure is %T, resolved = %v, want resolved = %v",
					items["i1"].Structure(), !isRecord, tc.resolved)
			}
		})
	}
}

// TestItemDefinitionRefusedInAProcess pins the context half of the tables:
// <itemDefinition> is a definitions-level root element, and one written
// among flow elements is refused with the § FR-8 gave it.
func TestItemDefinitionRefusedInAProcess(t *testing.T) {
	_, err := importEventDoc(t, `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="P" name="P">
    <bpmn:itemDefinition id="i1"/>
  </bpmn:process>
</bpmn:definitions>`)
	if err == nil {
		t.Fatal("an <itemDefinition> among flow elements must be refused")
	}

	if !strings.Contains(err.Error(), "§8.4.10") {
		t.Errorf("error = %v, want the §8.4.10 pin", err)
	}
}
