package bpmn

import (
	"encoding/xml"
	"fmt"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// catalog holds the definitions-level objects an event definition refers
// to: <message>, <signal>, <error> and <escalation>.
//
// It is a second index, parallel to refIndex's nodes and flows, and
// deliberately not merged with them: nothing in it is a flow element, so a
// nodes-keyed lookup that also held messages would let a
// <sequenceFlow sourceRef="msg1"> resolve to a message and fail later
// with a type error far from its cause (SRD-089.D §4.5).
type catalog struct {
	messages    map[string]*bpmncommon.Message
	signals     map[string]*events.Signal
	errors      map[string]*bpmncommon.Error
	escalations map[string]*events.Escalation
	// kinds records which element declared each id, so a duplicate names
	// the element that already took it. BPMN ids are unique across a
	// document, and the four maps alone could not see a collision between
	// two of them.
	kinds map[string]string
}

// newCatalog builds the empty catalog the parser fills.
func newCatalog() *catalog {
	return &catalog{
		messages:    map[string]*bpmncommon.Message{},
		signals:     map[string]*events.Signal{},
		errors:      map[string]*bpmncommon.Error{},
		escalations: map[string]*events.Escalation{},
		kinds:       map[string]string{},
	}
}

// catalogSpec is one parsed catalog element. The four differ in which
// fields they use rather than in shape, so one record serves all of them
// and the differences stay in catalogBuilders.
// The slice trails the strings for govet/fieldalignment, the same reason
// assembly orders its fields that way.
type catalogSpec struct {
	// local is the element name as the file wrote it, so an error names
	// <signal> rather than the record standing in for it.
	local string
	id    string
	name  string
	// code is errorCode or escalationCode; a message and a signal have
	// none.
	code string
	// structure is the payload type reference, kept only long enough to
	// report it — it names a type this converter cannot resolve (§4.1).
	structure string
	docs      []docSpec
}

// opts renders the spec as construction options, id first.
func (s catalogSpec) opts() []options.Option {
	return append([]options.Option{foundation.WithID(s.id)}, docOptions(s.docs)...)
}

// structureAttrs names the attribute each catalog element carries its
// payload type in: BPMN spells it itemRef on a message and structureRef
// on the other three (elements/event-definitions.md:230-318).
var structureAttrs = map[string]string{
	tagMessage:    "itemRef",
	tagSignal:     attrStructureRef,
	tagError:      attrStructureRef,
	tagEscalation: attrStructureRef,
}

// codeAttrs names the code attribute of the two catalog elements that
// have one. A tag absent from the table has no code, and reading a
// missing attribute yields the empty string — so the absence needs no
// branch of its own.
var codeAttrs = map[string]string{
	tagError:      "errorCode",
	tagEscalation: "escalationCode",
}

// structureLoss is why a catalog object's payload type does not survive
// the import. Reported rather than dropped: a structure silently
// discarded is how a host discovers at run time that its message has no
// payload (SRD-089.D §4.1).
const structureLoss = "names a type in an external XSD or WSDL this converter " +
	"does not have and cannot fetch, while an ItemDefinition needs a Go value; " +
	"the element imports carrying no payload structure — build the message in " +
	"Go if the payload matters"

// catalogBuilders files one parsed catalog element under its id. Keyed by
// tag exactly as nodeBuilders is, so a fifth catalog element the standard
// may grow is one row.
var catalogBuilders = map[string]func(*catalog, catalogSpec) error{
	tagMessage:    (*catalog).addMessage,
	tagSignal:     (*catalog).addSignal,
	tagError:      (*catalog).addError,
	tagEscalation: (*catalog).addEscalation,
}

// parseCatalogElement parses one definitions-level catalog element.
func (p *parser) parseCatalogElement(se xml.StartElement) error {
	id, err := requiredID(se)
	if err != nil {
		return err
	}

	local := se.Name.Local

	err = p.claimID(id, local)
	if err != nil {
		return err
	}

	spec := catalogSpec{
		local: local,
		id:    id,
		// All four constructors demand a non-empty name and BPMN makes it
		// optional, so an unnamed object takes its id — the answer .A
		// already gave for nodes (§4.2).
		name:      fallbackName(id, attrValue(se, "name")),
		code:      strings.TrimSpace(attrValue(se, codeAttrs[local])),
		structure: strings.TrimSpace(attrValue(se, structureAttrs[local])),
	}

	// The element's own id owns whatever its subtree reports, exactly as a
	// flow node's does.
	outer := p.owner
	p.owner = id

	spec.docs, err = p.parseCatalogBody(se)

	p.owner = outer

	if err != nil {
		return err
	}

	if spec.structure != "" {
		p.report(id, structureAttrs[local], structureLoss)
	}

	build, ok := catalogBuilders[local]
	if !ok {
		// Unreachable through definitionsParsers, which routes only the
		// names this table also carries — a guard against the two drifting.
		return errs.New(
			errs.M("bpmn: no catalog constructor for %q", local),
			errs.C(errorClass, errs.InvalidObject))
	}

	if err := build(p.cat, spec); err != nil {
		return wrapErr(
			fmt.Sprintf("bpmn: couldn't create %s %q", local, id),
			errs.BulidingFailed,
			err)
	}

	p.cat.kinds[id] = local

	return nil
}

// parseCatalogBody reads a catalog element's children. All four are
// BaseElements whose remaining content model carries nothing executable,
// so <documentation> is the only child that reaches the model.
func (p *parser) parseCatalogBody(se xml.StartElement) ([]docSpec, error) {
	var docs []docSpec

	for {
		tok, err := p.token()
		if err != nil {
			return nil, err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			d, err := p.parseCatalogChild(t)
			if err != nil {
				return nil, err
			}

			if d != nil {
				docs = append(docs, *d)
			}

		case xml.EndElement:
			if t.Name == se.Name {
				return docs, nil
			}
		}
	}
}

// parseCatalogChild handles one child of a catalog element, returning the
// documentation it contributed or nil when it contributed none.
func (p *parser) parseCatalogChild(se xml.StartElement) (*docSpec, error) {
	if se.Name.Space != nsBPMN {
		return nil, p.skipElement()
	}

	if se.Name.Local == tagDocumentation {
		d, err := p.parseDoc(se)
		if err != nil {
			return nil, err
		}

		return &d, nil
	}

	return nil, p.settle(ctxCatalog, se)
}

// addMessage files a <message>.
//
// NewMessage rejects a nil item, so the message gets the empty
// placeholder §4.1 chose over refusing a document that is entirely legal.
func (c *catalog) addMessage(s catalogSpec) error {
	item, err := emptyItem(s.id)
	if err != nil {
		return err
	}

	m, err := bpmncommon.NewMessage(s.name, item, s.opts()...)
	if err != nil {
		return err
	}

	c.messages[s.id] = m

	return nil
}

// addSignal files a <signal>.
//
// NewSignal accepts a nil structure, and nil is the model's own way of
// saying "no payload" — so a signal whose structureRef the converter
// cannot resolve gets nothing rather than an empty placeholder. The
// placeholder exists to satisfy a constructor that demands an item, not
// to assert a structure the document did not describe.
func (c *catalog) addSignal(s catalogSpec) error {
	sig, err := events.NewSignal(s.name, nil, s.opts()...)
	if err != nil {
		return err
	}

	c.signals[s.id] = sig

	return nil
}

// addError files an <error> with its errorCode. NewError accepts a nil
// structure and documents nil as "no payload" — see addSignal.
func (c *catalog) addError(s catalogSpec) error {
	e, err := bpmncommon.NewError(s.name, s.code, nil, s.opts()...)
	if err != nil {
		return err
	}

	c.errors[s.id] = e

	return nil
}

// addEscalation files an <escalation> with its escalationCode.
// NewEscalation rejects a nil item, so it takes the placeholder for the
// same reason a message does.
func (c *catalog) addEscalation(s catalogSpec) error {
	item, err := emptyItem(s.id)
	if err != nil {
		return err
	}

	e, err := events.NewEscalation(s.name, s.code, item, s.opts()...)
	if err != nil {
		return err
	}

	c.escalations[s.id] = e

	return nil
}

// emptyItem is the placeholder payload for the constructors that demand
// one. An empty record is truthful: the object exists and carries no
// payload structure, which is exactly what a document with no resolvable
// structure said.
func emptyItem(id string) (*data.ItemDefinition, error) {
	rec, err := values.NewRecord()
	if err != nil {
		return nil, err
	}

	return data.NewItemDefinition(rec, foundation.WithID(id+":item"))
}
