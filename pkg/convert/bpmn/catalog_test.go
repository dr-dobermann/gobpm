package bpmn

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/convert"
	"github.com/dr-dobermann/gobpm/pkg/errs"
)

// parseCatalog runs a document through the parser and hands back the
// catalog it filled. The catalog is not reachable from the imported
// process — nothing refers to it until the event definitions land — so a
// test that asserts what was built has to look at the parser's own state.
func parseCatalog(t *testing.T, doc string) (*catalog, []convert.Dropped, error) {
	t.Helper()

	p := newParser(context.Background(), strings.NewReader(doc))

	_, err := p.parse()

	return p.cat, p.dropped, err
}

// catalogDoc wraps definitions-level content in a document carrying a
// minimal valid process, since a file without one is an import error.
func catalogDoc(rootElements string) string {
	return `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
` + rootElements + `
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`
}

// TestCatalogElementsImport covers SRD-089.D §6 T-1 (FR-1): the four
// definitions-level objects an event definition refers to become model
// objects indexed by id, and the two that carry a code carry it through.
func TestCatalogElementsImport(t *testing.T) {
	cat, _, err := parseCatalog(t, catalogDoc(`
  <bpmn:message id="m1" name="Order placed"/>
  <bpmn:signal id="sig1" name="Cancelled"/>
  <bpmn:error id="err1" name="Payment failed" errorCode="E_PAY"/>
  <bpmn:escalation id="esc1" name="Overdue" escalationCode="ESC_1"/>`))
	if err != nil {
		t.Fatalf("Import of a file carrying catalog elements: %v", err)
	}

	msg, ok := cat.messages["m1"]
	if !ok {
		t.Fatalf("message m1 is not in the catalog: %v", cat.messages)
	}

	if got := msg.Name(); got != "Order placed" {
		t.Errorf("message name = %q, want %q", got, "Order placed")
	}

	sig, ok := cat.signals["sig1"]
	if !ok {
		t.Fatalf("signal sig1 is not in the catalog: %v", cat.signals)
	}

	if got := sig.Name(); got != "Cancelled" {
		t.Errorf("signal name = %q, want %q", got, "Cancelled")
	}

	e, ok := cat.errors["err1"]
	if !ok {
		t.Fatalf("error err1 is not in the catalog: %v", cat.errors)
	}

	if got := e.ErrorCode(); got != "E_PAY" {
		t.Errorf("errorCode = %q, want %q — the code is what an error "+
			"event definition matches on", got, "E_PAY")
	}

	esc, ok := cat.escalations["esc1"]
	if !ok {
		t.Fatalf("escalation esc1 is not in the catalog: %v", cat.escalations)
	}

	if got := esc.Code(); got != "ESC_1" {
		t.Errorf("escalationCode = %q, want %q", got, "ESC_1")
	}
}

// TestCatalogIDsCarryThrough pins that each object keeps the id the file
// gave it. The id is what every later reference resolves against, so an
// object indexed under one id and carrying another would resolve here and
// export as a different element.
func TestCatalogIDsCarryThrough(t *testing.T) {
	cat, _, err := parseCatalog(t, catalogDoc(`
  <bpmn:message id="m1" name="Order placed"/>
  <bpmn:escalation id="esc1" name="Overdue"/>`))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	if got := cat.messages["m1"].ID(); got != "m1" {
		t.Errorf("message id = %q, want %q (ids are never auto-generated)", got, "m1")
	}

	if got := cat.escalations["esc1"].ID(); got != "esc1" {
		t.Errorf("escalation id = %q, want %q", got, "esc1")
	}
}

// TestCatalogStructureIsReportedNotDropped covers §6 T-2 (FR-8, §4.1):
// the payload type a catalog object names cannot be resolved here, and
// every occurrence says so. A structure discarded in silence is how a
// host discovers at run time that its message has no payload.
func TestCatalogStructureIsReportedNotDropped(t *testing.T) {
	cat, dropped, err := parseCatalog(t, catalogDoc(`
  <bpmn:message id="m1" name="Order" itemRef="Item_1"/>
  <bpmn:signal id="sig1" name="Cancelled" structureRef="Item_2"/>
  <bpmn:error id="err1" name="Failed" structureRef="Item_3"/>
  <bpmn:escalation id="esc1" name="Overdue" structureRef="Item_4"/>`))
	if err != nil {
		t.Fatalf("Import of catalog elements carrying a structure: %v", err)
	}

	want := map[string]string{
		"m1":   "itemRef",
		"sig1": "structureRef",
		"err1": "structureRef",
		"esc1": "structureRef",
	}

	got := map[string]string{}

	for _, d := range dropped {
		got[d.Element] = d.Construct

		if !strings.Contains(d.Reason, "payload") {
			t.Errorf("dropped %s/%s reason = %q, want it to say what was lost",
				d.Element, d.Construct, d.Reason)
		}
	}

	for id, attr := range want {
		if got[id] != attr {
			t.Errorf("dropped[%q] = %q, want %q — BPMN spells the payload "+
				"reference differently on a message than on the other three",
				id, got[id], attr)
		}
	}

	if len(dropped) != len(want) {
		t.Errorf("dropped = %d entries, want %d (one per occurrence)",
			len(dropped), len(want))
	}

	// The two constructors that demand an item get the empty placeholder;
	// the two that accept nil get nil, which is the model's own way of
	// saying "no payload".
	if item := cat.messages["m1"].Item(); item == nil {
		t.Error("message item = nil, want the empty placeholder — " +
			"NewMessage rejects a nil item")
	}

	if item := cat.escalations["esc1"].Item(); item == nil {
		t.Error("escalation item = nil, want the empty placeholder — " +
			"NewEscalation rejects a nil item")
	}

	if str := cat.signals["sig1"].Item(); str != nil {
		t.Error("signal structure is non-nil, want nil — NewSignal accepts " +
			"nil, and a placeholder there would assert a structure the " +
			"document did not describe")
	}

	if str := cat.errors["err1"].Structure(); str != nil {
		t.Error("error structure is non-nil, want nil — Structure() " +
			"documents nil as 'no payload'")
	}
}

// TestCatalogStructureAbsentIsNotReported pins the other half of T-2: an
// object with no structure at all lost nothing, so it must not appear in
// the report. A report that fires either way tells a host nothing.
func TestCatalogStructureAbsentIsNotReported(t *testing.T) {
	_, dropped, err := parseCatalog(t, catalogDoc(`
  <bpmn:message id="m1" name="Order"/>
  <bpmn:signal id="sig1" name="Cancelled"/>`))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	if len(dropped) != 0 {
		t.Errorf("dropped = %v, want nothing — neither element named a "+
			"structure, so nothing was lost", dropped)
	}
}

// TestNamelessCatalogObjectTakesItsID covers §6 T-3 (§4.2): all four
// constructors demand a non-empty name and BPMN makes it optional, so a
// <signal id="s1"/> imports under its id rather than being refused over a
// cosmetic field.
func TestNamelessCatalogObjectTakesItsID(t *testing.T) {
	cat, _, err := parseCatalog(t, catalogDoc(`
  <bpmn:signal id="sig1"/>
  <bpmn:message id="m1"/>
  <bpmn:error id="err1" errorCode="E"/>
  <bpmn:escalation id="esc1" escalationCode="X"/>`))
	if err != nil {
		t.Fatalf("Import of nameless catalog elements: %v", err)
	}

	for name, got := range map[string]string{
		"signal":     cat.signals["sig1"].Name(),
		"message":    cat.messages["m1"].Name(),
		"error":      cat.errors["err1"].Name(),
		"escalation": cat.escalations["esc1"].Name(),
	} {
		if got == "" {
			t.Errorf("%s name is empty — the constructor would have "+
				"refused it", name)
		}
	}

	if got := cat.signals["sig1"].Name(); got != "sig1" {
		t.Errorf("nameless signal name = %q, want its id %q", got, "sig1")
	}
}

// TestCatalogDeclaredAfterProcess pins the ordering the metamodel allows:
// Definitions.rootElements is an unordered 0..* collection
// (elements/foundation.md:23) and Process is itself a RootElement, so a
// <message> may legally follow the <process> that will refer to it.
// Requiring declare-before-use would refuse ordinary modeller output.
func TestCatalogDeclaredAfterProcess(t *testing.T) {
	doc := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="e1"/>
  </bpmn:process>
  <bpmn:message id="m1" name="Order placed"/>
</bpmn:definitions>`

	cat, _, err := parseCatalog(t, doc)
	if err != nil {
		t.Fatalf("Import with the message after the process: %v", err)
	}

	if _, ok := cat.messages["m1"]; !ok {
		t.Error("message m1 is missing — a root element after <process> " +
			"must still reach the catalog")
	}
}

// TestCatalogDuplicateID pins that two root elements cannot take the same
// id. BPMN ids are unique across a document, and the four maps alone
// cannot see a collision between two of them — so the catalog keeps the
// kind that claimed each id.
func TestCatalogDuplicateID(t *testing.T) {
	for name, elems := range map[string]string{
		"same element twice": `
  <bpmn:message id="x" name="A"/>
  <bpmn:message id="x" name="B"/>`,
		"two different kinds": `
  <bpmn:message id="x" name="A"/>
  <bpmn:signal id="x" name="B"/>`,
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := parseCatalog(t, catalogDoc(elems))
			if err == nil {
				t.Fatal("Import of a duplicate catalog id = nil, want an error")
			}

			var ae *errs.ApplicationError
			if !errors.As(err, &ae) || !ae.HasClass(errorClass) {
				t.Errorf("error = %v, want one classified %s", err, errorClass)
			}

			if !strings.Contains(err.Error(), "duplicate") {
				t.Errorf("error = %q, want it to name the duplication", err)
			}
		})
	}
}

// TestCatalogWithoutID refuses an unidentified catalog object: ids are
// never auto-generated (ADR-019), and an object nothing can refer to is
// not a catalog entry.
func TestCatalogWithoutID(t *testing.T) {
	_, _, err := parseCatalog(t, catalogDoc(`  <bpmn:signal name="Cancelled"/>`))
	if err == nil {
		t.Fatal("Import of a signal with no id = nil, want an error")
	}

	if !strings.Contains(err.Error(), "no id") {
		t.Errorf("error = %q, want it to name the missing id", err)
	}
}

// TestCatalogDocumentationImports pins that a catalog object carries its
// <documentation> into the model — all four are BaseElements, so the one
// child that can reach them does.
func TestCatalogDocumentationImports(t *testing.T) {
	cat, _, err := parseCatalog(t, catalogDoc(`
  <bpmn:message id="m1" name="Order">
    <bpmn:documentation>placed by the customer</bpmn:documentation>
  </bpmn:message>`))
	if err != nil {
		t.Fatalf("Import of a documented message: %v", err)
	}

	docs := cat.messages["m1"].Docs()
	if len(docs) != 1 || docs[0].Text() != "placed by the customer" {
		t.Errorf("message docs = %v, want the documentation the file carried", docs)
	}
}

// TestCatalogAddRejectsWhatTheModelRejects pins that a builder refusing
// an object files nothing. The parser cannot produce a nameless spec —
// §4.2's fallback runs first — so the four builders are exercised
// directly: the guarantee under test is theirs, that a constructor's
// refusal propagates instead of leaving a half-built catalog behind.
func TestCatalogAddRejectsWhatTheModelRejects(t *testing.T) {
	for local, add := range catalogBuilders {
		t.Run(local, func(t *testing.T) {
			cat := newCatalog()

			// An empty name is the one thing all four constructors refuse.
			if err := add(cat, catalogSpec{local: local, id: "x"}); err == nil {
				t.Fatalf("add %s with no name = nil, want the model's refusal", local)
			}

			for name, n := range map[string]int{
				"messages":    len(cat.messages),
				"signals":     len(cat.signals),
				"errors":      len(cat.errors),
				"escalations": len(cat.escalations),
			} {
				if n != 0 {
					t.Errorf("%s = %d entries after a refused build, want 0", name, n)
				}
			}
		})
	}
}

// TestCatalogBuilderTableGuards covers the two failures the dispatch
// itself can produce: a tag routed to the catalog with no builder behind
// it, and a builder that refuses. Both are unreachable through any
// document — definitionsParsers routes only the tags catalogBuilders
// carries, and the parser resolves every field a constructor demands —
// so the table is used as the seam it already is.
func TestCatalogBuilderTableGuards(t *testing.T) {
	t.Run("no builder for a routed tag", func(t *testing.T) {
		saved := catalogBuilders[tagMessage]
		delete(catalogBuilders, tagMessage)

		defer func() { catalogBuilders[tagMessage] = saved }()

		_, _, err := parseCatalog(t, catalogDoc(`  <bpmn:message id="m1" name="Order"/>`))
		if err == nil || !strings.Contains(err.Error(), "no catalog constructor") {
			t.Fatalf("error = %v, want the drift guard between the two tables", err)
		}
	})

	t.Run("the builder refuses", func(t *testing.T) {
		saved := catalogBuilders[tagMessage]
		catalogBuilders[tagMessage] = func(*catalog, catalogSpec) error {
			return errors.New("refused by the model")
		}

		defer func() { catalogBuilders[tagMessage] = saved }()

		_, _, err := parseCatalog(t, catalogDoc(`  <bpmn:message id="m1" name="Order"/>`))
		if err == nil {
			t.Fatal("error = nil, want the refusal wrapped")
		}

		if !strings.Contains(err.Error(), `message "m1"`) {
			t.Errorf("error = %q, want it to name the element the file wrote", err)
		}
	})
}

// TestCatalogForeignChildIsSkipped pins that a catalog element carrying
// vendor content still imports: a foreign namespace is out of execution
// scope and skipped whole, exactly as it is under every other element.
func TestCatalogForeignChildIsSkipped(t *testing.T) {
	doc := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:vendor="urn:example:vendor">
  <bpmn:signal id="sig1" name="Cancelled">
    <vendor:meta><vendor:owner>ops</vendor:owner></vendor:meta>
  </bpmn:signal>
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`

	cat, _, err := parseCatalog(t, doc)
	if err != nil {
		t.Fatalf("Import of a signal carrying vendor content: %v", err)
	}

	if _, ok := cat.signals["sig1"]; !ok {
		t.Error("signal sig1 is missing — foreign content must be skipped, " +
			"not fatal")
	}
}

// TestCatalogDialectExtensionIsReported pins that <extensionElements>
// under a catalog element reports against that element's id. The
// extension carries no id of its own, so a report naming anything else
// sends a reader to the wrong place in the file.
func TestCatalogDialectExtensionIsReported(t *testing.T) {
	doc := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:camunda="http://camunda.org/schema/1.0/bpmn">
  <bpmn:message id="m1" name="Order">
    <bpmn:extensionElements>
      <camunda:properties/>
    </bpmn:extensionElements>
  </bpmn:message>
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`

	_, dropped, err := parseCatalog(t, doc)
	if err != nil {
		t.Fatalf("Import of a message carrying a dialect extension: %v", err)
	}

	if len(dropped) != 1 {
		t.Fatalf("dropped = %v, want one entry for the extension", dropped)
	}

	if dropped[0].Element != "m1" {
		t.Errorf("dropped element = %q, want %q — the extension has no id "+
			"of its own, so it is reported against its owner",
			dropped[0].Element, "m1")
	}
}

// TestCatalogDocumentationError propagates a failure from inside a
// catalog element's body rather than importing an object that lost it.
func TestCatalogDocumentationError(t *testing.T) {
	_, _, err := parseCatalog(t, catalogDoc(`
  <bpmn:message id="m1" name="Order">
    <bpmn:documentation>text <bpmn:task id="nested"/></bpmn:documentation>
  </bpmn:message>`))

	var uee *convert.UnsupportedElementError
	if !errors.As(err, &uee) {
		t.Fatalf("Import with an element nested in documentation = %v, "+
			"want it refused", err)
	}
}

// TestCatalogTruncatedStream pins that a stream ending inside a catalog
// element is an error, not an object built from half a file.
func TestCatalogTruncatedStream(t *testing.T) {
	doc := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:message id="m1" name="Order">`

	_, _, err := parseCatalog(t, doc)
	if err == nil {
		t.Fatal("Import of a truncated catalog element = nil, want an error")
	}

	if !strings.Contains(err.Error(), "EOF") {
		t.Errorf("error = %q, want it to name the truncated stream", err)
	}
}

// TestCatalogUnknownChildIsRefused pins the default disposition inside a
// catalog element: an in-namespace child no parser claims is refused
// rather than swallowed, so a content model this stage misread cannot
// pass as an import that understood the file.
func TestCatalogUnknownChildIsRefused(t *testing.T) {
	_, _, err := parseCatalog(t, catalogDoc(`
  <bpmn:message id="m1" name="Order">
    <bpmn:extensionElements/>
    <bpmn:auditing id="a1"/>
  </bpmn:message>`))

	var uee *convert.UnsupportedElementError
	if !errors.As(err, &uee) {
		t.Fatalf("Import of an unknown catalog child = %v, want it refused", err)
	}

	if uee.Tag != "auditing" {
		t.Errorf("refused tag = %q, want %q — <extensionElements> is skipped "+
			"in every context and must not be the one reported", uee.Tag, "auditing")
	}
}
