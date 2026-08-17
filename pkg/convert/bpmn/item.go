package bpmn

import (
	"encoding/xml"
	"strings"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// xsdNamespace is the XML Schema namespace. BPMN's default typeLanguage
// (semantics/data.md:41), and the one structureRef vocabulary a converter
// can resolve without fetching anything.
const xsdNamespace = "http://www.w3.org/2001/XMLSchema"

// xsdKind is one XML Schema built-in as this engine stores it: the zero
// value a scalar item gets, and the empty collection a
// isCollection="true" item gets.
type xsdKind struct {
	scalar     func() data.Value
	collection func() data.Value
}

// zeroKind builds both forms of one type from its Go zero value, so a row
// of the table below names the type once instead of twice.
func zeroKind[T any](zero T) xsdKind {
	return xsdKind{
		scalar:     func() data.Value { return values.NewVariable(zero) },
		collection: func() data.Value { return values.NewArray[T]() },
	}
}

// xsdTypes maps an XML Schema built-in to the Go value an item definition
// carries for it. SAD-001 §14.1 fixes the FORM — "the 'declare empty, fill
// at runtime' intent is expressed with a typed-zero value" — and this is
// the type half of it.
//
// A table rather than a switch because it is a fixed classification whose
// one useful property, which types are in and which are out, should be
// readable at a glance (SRD-089.F §4.1).
//
// Every numeric type is a float64, INCLUDING the integers, and that is not
// an oversight. The evaluator unifies every Go numeric kind to float64
// (expression/lite/eval.go:68-96, ADR-032 §2.3) while values.checkValue is
// a strict type assertion with no coercion (values/array.go:402-414) — so
// an xsd:int item stored as Variable[int] would accept NO expression
// result at all. It would import cleanly and fail on the first assignment.
//
// time.Time is here because the evaluator carries it as a first-class
// operand kind beside the other three (eval.go:72). A type the expression
// tier cannot hold would not belong in this table however well XSD names
// it — which is why xsd:duration, xsd:base64Binary and the rest are absent
// rather than approximated.
var xsdTypes = map[string]xsdKind{
	"string":           zeroKind(""),
	"normalizedString": zeroKind(""),
	"token":            zeroKind(""),
	"anyURI":           zeroKind(""),
	"QName":            zeroKind(""),

	"boolean": zeroKind(false),

	"int":     zeroKind(float64(0)),
	"integer": zeroKind(float64(0)),
	"long":    zeroKind(float64(0)),
	"short":   zeroKind(float64(0)),
	"byte":    zeroKind(float64(0)),
	"decimal": zeroKind(float64(0)),
	"double":  zeroKind(float64(0)),
	"float":   zeroKind(float64(0)),

	"dateTime": zeroKind(time.Time{}),
	"date":     zeroKind(time.Time{}),
	"time":     zeroKind(time.Time{}),
}

// itemKinds maps BPMN's itemKind attribute to the model's. The empty key
// is the absent attribute, whose default the standard sets to Information
// (semantics/data.md:35).
var itemKinds = map[string]data.ItemKind{
	"":            data.InformationKind,
	"Information": data.InformationKind,
	"Physical":    data.PhysicalKind,
}

// itemSpec is a definitions-level <itemDefinition> as read.
//
// It is kept as a spec rather than built on sight because resolving its
// structureRef needs the document's <import> declarations, and BPMN orders
// root elements no more than it orders flow elements — the <import> may
// follow the item that names it.
type itemSpec struct {
	id           string
	structureRef string
	kind         string
	docs         []docSpec
	isCollection bool
}

// importSpec is a definitions-level <import>: the three attributes, in the
// shape foundation.Import already has (foundation/import.go:4-8).
type importSpec struct {
	importType, location, namespace string
}

// items is the document's type vocabulary: the item definitions, the
// imports they may name, and the namespace prefixes that connect the two.
type items struct {
	// prefixes maps an xmlns prefix to its namespace URI, collected from
	// the elements that declare one. A structureRef's prefix is XML text
	// inside an ATTRIBUTE VALUE, which the decoder does not resolve — only
	// element and attribute NAMES get a Space.
	prefixes map[string]string
	// imports are keyed by namespace, which is what an itemDefinition's
	// prefix resolves to.
	imports map[string]importSpec
	// used records the namespaces an item definition actually referred to,
	// so an <import> nobody named can be reported rather than dropped.
	used  map[string]bool
	specs []itemSpec
}

// newItems builds the empty vocabulary the parser fills.
func newItems() *items {
	return &items{
		prefixes: map[string]string{},
		imports:  map[string]importSpec{},
		used:     map[string]bool{},
	}
}

// declareNamespaces records every xmlns prefix se declares.
//
// A deeper element re-declaring a prefix overwrites the outer binding
// rather than shadowing it for its subtree. That is a simplification, and
// a safe one here: <itemDefinition> and <import> are root elements of
// <definitions>, where any real document declares its namespaces, and the
// consequence of getting it wrong is a structureRef that does not resolve
// — which is reported, not silently mistyped.
func (it *items) declareNamespaces(se xml.StartElement) {
	for _, a := range se.Attr {
		if a.Name.Space == "xmlns" && a.Value != "" {
			it.prefixes[a.Name.Local] = a.Value
		}
	}
}

// parseImportElem parses one definitions-level <import>.
func parseImportElem(p *parser, _ *assembly, se xml.StartElement) (*assembly, error) {
	return nil, p.parseImport(se)
}

// parseImport records an <import> by the namespace it declares.
//
// An <import> with no namespace is refused rather than kept: the namespace
// is the only thing that can connect it to an itemDefinition, so one
// without it could never be attached to anything, and silently keeping it
// would report it later as "referred to by nothing" — a true statement
// about the wrong problem.
func (p *parser) parseImport(se xml.StartElement) error {
	p.items.declareNamespaces(se)

	spec := importSpec{
		importType: strings.TrimSpace(attrValue(se, attrImportType)),
		location:   strings.TrimSpace(attrValue(se, attrLocation)),
		namespace:  strings.TrimSpace(attrValue(se, attrNamespace)),
	}

	if spec.namespace == "" {
		return errs.New(
			errs.M("bpmn: <import> declares no namespace; the namespace is what "+
				"an <itemDefinition>'s structureRef resolves to, so an import "+
				"without one can never be bound to a type"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if _, dup := p.items.imports[spec.namespace]; dup {
		return errs.New(
			errs.M("bpmn: <import> declares namespace %q twice", spec.namespace),
			errs.C(errorClass, errs.DuplicateObject))
	}

	p.items.imports[spec.namespace] = spec

	return p.skipElement()
}

// parseItemDefElem parses one definitions-level <itemDefinition>.
func parseItemDefElem(p *parser, _ *assembly, se xml.StartElement) (*assembly, error) {
	return nil, p.parseItemDefinition(se)
}

// parseItemDefinition records an <itemDefinition> for pass 2.
func (p *parser) parseItemDefinition(se xml.StartElement) error {
	id, err := requiredID(se)
	if err != nil {
		return err
	}

	err = p.claimID(id, tagItemDefinition)
	if err != nil {
		return err
	}

	p.items.declareNamespaces(se)

	spec := itemSpec{
		id:           id,
		structureRef: strings.TrimSpace(attrValue(se, attrStructureRef)),
		kind:         strings.TrimSpace(attrValue(se, attrItemKind)),
		isCollection: attrBool(se, attrIsCollection, false),
	}

	if _, known := itemKinds[spec.kind]; !known {
		return errs.New(
			errs.M("bpmn: itemDefinition %q: itemKind %q is neither Information "+
				"nor Physical (§8.4.10)", id, spec.kind),
			errs.C(errorClass, errs.InvalidParameter))
	}

	// The element's own id owns whatever its subtree reports, exactly as a
	// catalog element's does.
	outer := p.owner
	p.owner = id

	// An <itemDefinition> is a RootElement → BaseElement, so its content
	// model is a catalog element's: <documentation>, <extensionElements>,
	// and nothing executable. The same body reader serves both.
	spec.docs, err = p.parseCatalogBody(se)

	p.owner = outer

	if err != nil {
		return err
	}

	p.items.specs = append(p.items.specs, spec)
	p.cat.kinds[id] = tagItemDefinition

	return nil
}

// builtin resolves ref to an XML Schema built-in, reporting whether it is
// one. It also records the namespace ref resolved to, which is what makes
// an unreferenced <import> visible.
//
// An UNPREFIXED reference is not a built-in: it names a type in the
// document's own target namespace, and reading it as XSD would type an
// item by a coincidence of spelling.
func (it *items) builtin(ref string) (xsdKind, bool) {
	prefix, local, found := strings.Cut(ref, ":")
	if !found {
		return xsdKind{}, false
	}

	ns, declared := it.prefixes[prefix]

	// A real namespace this converter cannot fetch. Recording it is what
	// lets an <import> naming it count as referred-to.
	if declared && ns != xsdNamespace {
		it.used[ns] = true

		return xsdKind{}, false
	}

	// An UNDECLARED prefix is read as XSD only when it is spelled the
	// conventional way. Schema-invalid strictly speaking, and common
	// enough in hand-written and tool-trimmed files that refusing it would
	// cost more than it buys; any other undeclared prefix resolves to
	// nothing rather than to a guess.
	if !declared && prefix != "xsd" && prefix != "xs" {
		return xsdKind{}, false
	}

	kind, ok := xsdTypes[local]

	return kind, ok
}

// structureFor returns the value an item definition carries, and reports
// what could not be typed.
//
// The unresolvable case yields an EMPTY RECORD rather than a nil
// structure. Nil constructs fine — NewItemDefinition stores its value
// unchecked (item_options.go:28-40) — and then fails far away, because
// ItemAwareElement.Clone refuses a nil value (item.go:326-331) and cloning
// is what a snapshot does per instance. An empty record is truthful in the
// same way .D's placeholder is, and it can be filled: the record is
// permissive, so SetField adds a field it did not know (SRD-089.F §4.2).
func (p *parser) structureFor(s itemSpec) data.Value {
	kind, ok := p.items.builtin(s.structureRef)
	if !ok {
		if s.structureRef != "" {
			p.report(s.id, attrStructureRef, structureLoss)
		}

		if s.isCollection {
			p.report(s.id, attrIsCollection, collectionLoss)
		}

		return values.EmptyRecord()
	}

	if s.isCollection {
		return kind.collection()
	}

	return kind.scalar()
}

// collectionLoss is why isCollection cannot survive a structure the
// converter could not type: an Array needs an element type, and the
// element type is exactly what could not be read (SRD-089.F §4.3).
const collectionLoss = "marks a collection whose element type comes from the " +
	"structure this converter could not resolve, and a collection of an " +
	"unknown type is a shape rather than a value; the item imports as a " +
	"single empty record — build it in Go if the collection matters"

// buildItems turns the document's item definitions into model objects,
// keyed by id.
//
// It runs at the start of pass 2, after the whole document has been read,
// because an <import> may follow the <itemDefinition> that names its
// namespace — BPMN orders root elements no more than flow elements
// (elements/foundation.md:23).
func buildItems(p *parser) (map[string]*data.ItemDefinition, error) {
	built := make(map[string]*data.ItemDefinition, len(p.items.specs))

	for _, s := range p.items.specs {
		opts := append([]options.Option{
			foundation.WithID(s.id),
			data.WithKind(itemKinds[s.kind]),
		}, docOptions(s.docs)...)

		if imp, ok := p.items.importFor(s.structureRef); ok {
			opts = append(opts, data.WithImport(imp))
		}

		// Unreachable for any document: NewItemDefinition rejects only an
		// option of an unknown type or an invalid itemKind, and the three
		// options above are a valid id, a kind this parser took from
		// itemKinds, and documentation that cannot fail. A failure here
		// means an option was added without checking what can refuse it.
		idef, err := data.NewItemDefinition(p.structureFor(s), opts...)
		if err != nil {
			return nil, errs.Invariant(
				"itemDefinition %q rejected its own options: %w", s.id, err)
		}

		built[s.id] = idef
	}

	p.reportUnusedImports()

	return built, nil
}

// importFor returns the <import> whose namespace ref's prefix resolves to.
//
// Attaching it does NOT make the structure resolvable — no schema is
// fetched, so §4.2 still applies. What it preserves is WHERE the type came
// from, which is what ItemDefinition.Import() exposes and what an export
// needs to write the declaration back.
func (it *items) importFor(ref string) (*foundation.Import, bool) {
	prefix, _, found := strings.Cut(ref, ":")
	if !found {
		return nil, false
	}

	ns, declared := it.prefixes[prefix]
	if !declared {
		return nil, false
	}

	spec, ok := it.imports[ns]
	if !ok {
		return nil, false
	}

	return &foundation.Import{
		Type:      spec.importType,
		Location:  spec.location,
		Namespace: spec.namespace,
	}, true
}

// reportUnusedImports names every <import> no item definition referred to.
//
// It is reported rather than skipped because it declares a dependency the
// imported definition does not carry: the file says a schema is needed and
// the model has no record of it, which is the quiet loss ADR-024 §2.14
// exists to surface. An <import> carries no id of its own, so the report
// identifies it by the namespace it declares — the thing a reader greps
// the file for.
func (p *parser) reportUnusedImports() {
	for ns, spec := range p.items.imports {
		if p.items.used[ns] {
			continue
		}

		p.report(spec.namespace, tagImport,
			"declares a type namespace no <itemDefinition> refers to, so "+
				"nothing in the imported definition records the dependency; "+
				"point an itemDefinition's structureRef at it, or drop it")
	}
}
