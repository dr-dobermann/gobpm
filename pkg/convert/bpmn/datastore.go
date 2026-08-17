package bpmn

import (
	"encoding/xml"
	"strings"
)

// dataStoreSpec is a definitions-level <dataStore> as read. It is never
// built: the store it declares is engine infrastructure, and the spec
// exists only to carry what the report must say (§4.5).
type dataStoreSpec struct {
	id string
	// capacity and isUnlimited ride the report verbatim — ADR-030 §2.6
	// makes capacity advisory, so importing it would assert an
	// enforcement the engine does not perform.
	capacity, isUnlimited string
}

// parseDataStoreElem parses one definitions-level <dataStore>.
func parseDataStoreElem(
	p *parser, _ *assembly, se xml.StartElement,
) (*assembly, error) {
	return nil, p.parseDataStore(se)
}

// parseDataStore records a <dataStore> for the host-obligation report.
//
// The id is required even though nothing is built from it: it is what
// every <dataStoreReference dataStoreRef> in the file names, and the id
// the host must register a store under — a declaration without one
// obliges nobody to anything.
func (p *parser) parseDataStore(se xml.StartElement) error {
	id, err := requiredID(se)
	if err != nil {
		return err
	}

	err = p.claimID(id, tagDataStore)
	if err != nil {
		return err
	}

	spec := dataStoreSpec{
		id:          id,
		capacity:    strings.TrimSpace(attrValue(se, attrCapacity)),
		isUnlimited: strings.TrimSpace(attrValue(se, attrIsUnlimited)),
	}

	// The element's own id owns whatever its subtree reports, exactly as
	// an itemDefinition's does.
	outer := p.owner
	p.owner = id

	// A <dataStore> is a RootElement → BaseElement, so its content model
	// is a catalog element's: <documentation>, <extensionElements>, and
	// nothing executable. The docs are dropped with the element — there
	// is no model object to carry them.
	_, err = p.parseCatalogBody(se)

	p.owner = outer

	if err != nil {
		return err
	}

	p.stores = append(p.stores, spec)
	p.cat.kinds[id] = tagDataStore

	return nil
}

// reportDataStores names every <dataStore> the document declared as the
// host obligation it is: gobpm's store is an engine-level infrastructure
// port supplied through the runtime environment (ADR-030 §2.5), which no
// import can create. The report is what tells a host which store ids the
// file expects to exist (§4.5).
func (p *parser) reportDataStores() {
	for _, s := range p.stores {
		p.report(s.id, tagDataStore, storeObligation(s))
	}
}

// storeObligation renders one store's report, carrying the declared
// capacity so the host learns what the file expects without re-reading
// it.
func storeObligation(s dataStoreSpec) string {
	msg := "declares a data store, which no import can create: a store is " +
		"engine infrastructure the host supplies through the engine's " +
		"registry — register a store under this id before running the process"

	if s.capacity != "" {
		msg += "; the declared capacity of " + s.capacity +
			" is advisory and not enforced"
	}

	if s.isUnlimited != "" {
		msg += "; isUnlimited=" + s.isUnlimited + " is advisory and not enforced"
	}

	return msg
}
