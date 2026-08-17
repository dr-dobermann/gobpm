package bpmn

import (
	"encoding/xml"
	"strconv"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	dataobjects "github.com/dr-dobermann/gobpm/pkg/model/data_objects"
	datastores "github.com/dr-dobermann/gobpm/pkg/model/data_stores"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// dataSpec is one item-aware flow element as pass 1 read it: a
// <dataObject>, a <dataObjectReference> or (from the next milestone) a
// <dataStoreReference>.
//
// One record serves all three because they differ in which fields they use
// rather than in shape — the same reason catalogSpec serves four catalog
// elements. Which fields mean what stays in the builders.
type dataSpec struct {
	// local is the element name as the file wrote it, so an error names
	// <dataObjectReference> rather than the record standing in for it.
	local    string
	id, name string
	// container is the id of the container holding it, "" for the process
	// — read at parse time, exactly as a nodeSpec's is (SRD-089.E §4.1).
	container string
	// itemRef is itemSubjectRef: the <itemDefinition> naming its type.
	itemRef string
	// targetRef is dataObjectRef on a reference, dataStoreRef on a store
	// reference.
	targetRef string
	// state is <dataState name=…>, reported and never mapped (§4.7).
	state string
	docs  []docSpec
}

// opts renders the spec as construction options, id first.
func (s dataSpec) opts() []options.Option {
	return append([]options.Option{foundation.WithID(s.id)}, docOptions(s.docs)...)
}

// owner names the element in an error, as the file wrote it.
func (s dataSpec) owner() string {
	return s.local + " " + strconv.Quote(s.id)
}

// dataElements are the item-aware flow elements this stage claims, mapped
// to the context they are parsed in. Registered from init() beside the
// containers, so a container's children reach them through the same table.
var dataElements = map[string]bool{
	tagDataObject:    true,
	tagDataObjectRef: true,
	tagDataStoreRef:  true,
}

// targetAttrs maps a data element to the attribute naming its target — a
// lookup miss means the element carries none, and attrValue reads an
// empty attribute name as absent.
var targetAttrs = map[string]string{
	tagDataObjectRef: attrDataObjectRef,
	tagDataStoreRef:  attrDataStoreRef,
}

// parseDataElem parses one item-aware flow element of a container.
func parseDataElem(p *parser, asm *assembly, se xml.StartElement) error {
	return p.parseDataElement(asm, se)
}

// parseDataElement records one data element for pass 2.
//
// It is not built on sight for the same reason a flow node is not: a
// <dataObjectReference> may precede the <dataObject> it names, and its
// itemSubjectRef may name an <itemDefinition> the document declares after
// the process (§4.4).
func (p *parser) parseDataElement(asm *assembly, se xml.StartElement) error {
	id, err := requiredID(se)
	if err != nil {
		return err
	}

	err = p.claimID(id, se.Name.Local)
	if err != nil {
		return err
	}

	asm.declared[id] = se.Name.Local

	spec := dataSpec{
		local:     se.Name.Local,
		id:        id,
		name:      strings.TrimSpace(attrValue(se, "name")),
		container: p.container,
		itemRef:   strings.TrimSpace(attrValue(se, attrItemSubjectRef)),
		targetRef: strings.TrimSpace(attrValue(se, targetAttrs[se.Name.Local])),
	}

	// The element's own id owns whatever its subtree reports.
	outer := p.owner
	p.owner = id

	err = p.parseDataBody(&spec, se)

	p.owner = outer

	if err != nil {
		return err
	}

	p.reportUnmappedAttrs(se, id, nil)

	asm.datas = append(asm.datas, spec)

	return nil
}

// parseDataBody reads a data element's children into spec.
//
// An item-aware element's content model is a BaseElement's plus
// <dataState> (elements/data.md:17-18), so documentation and the state are
// the two children that mean anything here.
func (p *parser) parseDataBody(spec *dataSpec, se xml.StartElement) error {
	for {
		tok, err := p.token()
		if err != nil {
			return err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			if err := p.parseDataChild(spec, t); err != nil {
				return err
			}

		case xml.EndElement:
			if t.Name == se.Name {
				return nil
			}
		}
	}
}

// parseDataChild handles one child of a data element.
func (p *parser) parseDataChild(spec *dataSpec, se xml.StartElement) error {
	if se.Name.Space != nsBPMN {
		return p.skipElement()
	}

	switch se.Name.Local {
	case tagDocumentation:
		d, err := p.parseDoc(se)
		if err != nil {
			return err
		}

		spec.docs = append(spec.docs, d)

		return nil

	case tagDataState:
		spec.state = strings.TrimSpace(attrValue(se, "name"))

		return p.skipElement()
	}

	return p.settle(ctxData, se)
}

// dataStateLoss is why a <dataState> cannot survive the import.
//
// BPMN leaves the value's meaning to the engine — "State value semantics
// are out of scope of the standard" (semantics/data.md:27) — and gobpm
// replaced the open label with a closed pair the engine acts on
// (SAD-001 §14.1). Its two names are internal sentinels no modeler writes,
// so matching a file's state against them would be a coincidence rather
// than a mapping (§4.7).
const dataStateLoss = "names a data state, which BPMN leaves without meaning " +
	"and this engine replaced with a closed readiness pair it can act on " +
	"(unavailable and ready); the element imports in the model's default " +
	"state — model a domain state as process data"

// buildDataElements builds the document's data elements and adds each to
// the container that held it.
//
// It runs after buildNodes because a container IS a node: the sub-process
// holding a data object does not exist until the node pass has built it.
func buildDataElements(p *parser, asm *assembly) error {
	// Indexed rather than ranged by value: a dataSpec carries five strings
	// and a slice, and copying it per element buys nothing — the same
	// reason buildNodes indexes its specs.
	for i := range asm.datas {
		s := &asm.datas[i]

		if s.state != "" {
			p.report(s.id, tagDataState, dataStateLoss)
		}

		build, ok := dataBuilders[s.local]
		if !ok {
			// Unreachable through processParsers, which routes only the
			// names this table also carries — a guard against the two
			// drifting.
			return errs.Invariant("no data builder for %q", s.local)
		}

		elem, err := build(p, asm, s)
		if err != nil {
			return err
		}

		// A reference contributes no element of its own (§4.4 rule 1).
		if elem == nil {
			continue
		}

		owner, err := containerFor(asm, s.container)
		if err != nil {
			return err
		}

		if err := owner.Add(elem); err != nil {
			return wrapErr(
				"bpmn: couldn't add "+s.owner()+" to "+strconv.Quote(owner.ID()),
				errs.BulidingFailed,
				err)
		}

		// For the association pass: a data element is wired by the
		// Associate* methods on the ELEMENT, so the pass needs the built
		// object back by id (SRD-089.G §4.1).
		asm.dataElems[s.id] = elem
	}

	return nil
}

// dataBuilder builds the model element behind one data element, or returns
// a nil element for one that contributes none.
type dataBuilder func(*parser, *assembly, *dataSpec) (flow.Element, error)

// dataBuilders maps a data element to what it becomes, keyed by tag
// exactly as nodeBuilders is.
var dataBuilders = map[string]dataBuilder{
	tagDataObject:    buildDataObject,
	tagDataObjectRef: buildDataObjectReference,
	tagDataStoreRef:  buildDataStoreReference,
}

// buildDataObject builds a Data Object over the item its itemSubjectRef
// names.
//
// The state is the model's default: BPMN forbids a DataObject its own
// <dataState> (semantics/data.md:49) and gobpm's readiness pair is not the
// document's to set either way.
func buildDataObject(
	p *parser, asm *assembly, s *dataSpec,
) (flow.Element, error) {
	item, err := itemFor(p, asm, s.owner(), s.id, s.itemRef)
	if err != nil {
		return nil, err
	}

	do, err := dataobjects.New(fallbackName(s.id, s.name), item, nil, s.opts()...)
	if err != nil {
		return nil, wrapErr(
			"bpmn: couldn't create "+s.owner(), errs.BulidingFailed, err)
	}

	return do, nil
}

// buildDataObjectReference collapses a reference into the object it names,
// per SAD-001 §14.1's translation rules 1, 3 and 4.
//
// It contributes no element: the reference exists to draw one object at
// several points on a diagram, which a programmatic model has no use for,
// and every reference to one object collapses to that single object. What
// it must still do is FAIL when it names nothing — a reference resolving
// to no object is a broken file, and importing it as silence would drop
// whatever the modeler drew there.
func buildDataObjectReference(
	p *parser, asm *assembly, s *dataSpec,
) (flow.Element, error) {
	site := refSite{from: s.owner(), attr: attrDataObjectRef, target: s.targetRef}

	if site.target == "" {
		return nil, errs.New(
			errs.M("bpmn: %s names no dataObjectRef; a reference exists to "+
				"point at a <dataObject>, and one pointing nowhere has nothing "+
				"to import", s.owner()),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	kind, declared := asm.declared[site.target]
	if !declared {
		return nil, site.notFound("dataObject")
	}

	if kind != tagDataObject {
		return nil, site.wrongKind("dataObject", kind)
	}

	// itemSubjectRef on a reference is meaningless by the standard's own
	// rule — a reference "cannot specify itemSubjectRef" and inherits the
	// object's (semantics/data.md:64) — so a file carrying one is told
	// rather than silently obeyed or silently ignored.
	if s.itemRef != "" {
		p.report(s.id, attrItemSubjectRef,
			"types a <dataObjectReference>, which BPMN says takes its type "+
				"from the <dataObject> it references; the reference collapses "+
				"into that object and the object's own itemSubjectRef decides")
	}

	return nil, nil
}

// buildDataStoreReference builds a Data Store Reference carrying its
// dataStoreRef verbatim (FR-5).
//
// Verbatim is a decision, not an omission: the store the id names is
// engine infrastructure resolved from the engine's registry at run time
// (ADR-030 §2.5), so the document is not the authority the reference
// resolves against — a file may reference a store it never declares, and
// a declared <dataStore> is reported as a host obligation rather than
// consulted. An EMPTY dataStoreRef is still refused, by the model's own
// check (NFR-4): a reference to no store at all can never be resolved by
// any registry.
func buildDataStoreReference(
	p *parser, asm *assembly, s *dataSpec,
) (flow.Element, error) {
	item, err := itemFor(p, asm, s.owner(), s.id, s.itemRef)
	if err != nil {
		return nil, err
	}

	dsr, err := datastores.New(
		fallbackName(s.id, s.name), s.targetRef, item, nil, s.opts()...)
	if err != nil {
		return nil, wrapErr(
			"bpmn: couldn't create "+s.owner(), errs.BulidingFailed, err)
	}

	return dsr, nil
}

// itemFor returns the ItemDefinition an item-aware element carries —
// from is the element as an error names it, id its own id, ref its
// itemSubjectRef.
//
// Each element gets its OWN instance, even when several name one
// <itemDefinition>: the structure IS the value (ADR-010), so a shared
// pointer would make two data objects one variable — writing `order` would
// write `order2`. The id is preserved across the copies, because that is
// what a data association matches on (data_object.go:151-160).
//
// An element naming no item still needs one: every constructor here
// refuses a nil ItemDefinition. It gets an empty record of its own, which
// is what an untyped item-aware element is — BPMN permits itemSubjectRef
// at 0..1 (semantics/data.md:26).
func itemFor(
	p *parser, asm *assembly, from, id, ref string,
) (*data.ItemDefinition, error) {
	if ref == "" {
		return emptyItem(id)
	}

	declared, ok := asm.items[ref]
	if !ok {
		site := refSite{
			from:   from,
			attr:   attrItemSubjectRef,
			target: ref,
		}

		if kind, taken := asm.declared[ref]; taken {
			return nil, site.wrongKind(tagItemDefinition, kind)
		}

		if kind, taken := p.cat.kinds[ref]; taken &&
			kind != tagItemDefinition {
			return nil, site.wrongKind(tagItemDefinition, kind)
		}

		return nil, site.notFound(tagItemDefinition)
	}

	return copyItem(declared)
}

// copyItem returns a fresh ItemDefinition carrying a clone of the
// original's structure under the original's id — see itemFor.
func copyItem(idef *data.ItemDefinition) (*data.ItemDefinition, error) {
	opts := []options.Option{
		foundation.WithID(idef.ID()),
		data.WithKind(idef.Kind()),
	}

	if imp := idef.Import(); imp != nil {
		opts = append(opts, data.WithImport(imp))
	}

	// Unreachable for any document: the options are a non-empty id, a kind
	// that came from an ItemDefinition the same run built, and an import
	// that cannot be refused. A failure means an option was added without
	// checking what can refuse it.
	copied, err := data.NewItemDefinition(idef.Structure().Clone(), opts...)
	if err != nil {
		return nil, errs.Invariant(
			"itemDefinition %q rejected a copy of its own options: %w",
			idef.ID(), err)
	}

	return copied, nil
}
