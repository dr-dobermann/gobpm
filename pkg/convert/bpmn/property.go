package bpmn

import (
	"encoding/xml"
	"strconv"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// propSpec is a <property> as pass 1 read it.
//
// It carries no container id: a property reaches its owner as a
// CONSTRUCTION option (data.WithProperties), so it is buffered by the
// owner being built — procBuild for the process, nodeBody for an activity
// or an event — rather than placed afterwards (§4.6).
type propSpec struct {
	id, name string
	itemRef  string
	state    string
	docs     []docSpec
}

// parsePropertyElem records one <property> child of a flow node.
func parsePropertyElem(p *parser, body *nodeBody, se xml.StartElement) error {
	spec, err := p.parsePropertySpec(se)
	if err != nil {
		return err
	}

	body.props = append(body.props, spec)

	return nil
}

// parsePropertySpec reads one <property> into a propSpec.
//
// A property is an ItemAwareElement, so its content model is a data
// element's — documentation and <dataState> — and the same body reader
// serves both; the dataSpec is only the reading carrier.
func (p *parser) parsePropertySpec(se xml.StartElement) (propSpec, error) {
	id, err := requiredID(se)
	if err != nil {
		return propSpec{}, err
	}

	err = p.claimID(id, tagProperty)
	if err != nil {
		return propSpec{}, err
	}

	carrier := dataSpec{
		local:   tagProperty,
		id:      id,
		name:    strings.TrimSpace(attrValue(se, "name")),
		itemRef: strings.TrimSpace(attrValue(se, attrItemSubjectRef)),
	}

	// The element's own id owns whatever its subtree reports.
	outer := p.owner
	p.owner = id

	err = p.parseDataBody(&carrier, se)

	p.owner = outer

	if err != nil {
		return propSpec{}, err
	}

	p.reportUnmappedAttrs(se, id, nil)

	return propSpec{
		id:      carrier.id,
		name:    carrier.name,
		itemRef: carrier.itemRef,
		state:   carrier.state,
		docs:    carrier.docs,
	}, nil
}

// buildProperties builds the model Property behind each spec.
//
// It runs in pass 2, when the document's item definitions are complete —
// which is also why the process itself is now built there (§4.6): a
// property's itemSubjectRef may name an <itemDefinition> declared after
// the <process> carrying it.
func buildProperties(
	p *parser, asm *assembly, specs []propSpec,
) ([]*data.Property, error) {
	props := make([]*data.Property, 0, len(specs))

	for i := range specs {
		s := &specs[i]

		if s.state != "" {
			p.report(s.id, tagDataState, dataStateLoss)
		}

		from := tagProperty + " " + strconv.Quote(s.id)

		item, err := itemFor(p, asm, from, s.id, s.itemRef)
		if err != nil {
			return nil, err
		}

		opts := append(
			[]options.Option{foundation.WithID(s.id)}, docOptions(s.docs)...)

		// The state is nil — the model's default: a <dataState> was
		// reported above, never mapped (§4.7).
		prop, err := data.NewProperty(fallbackName(s.id, s.name), item, nil, opts...)
		if err != nil {
			return nil, wrapErr(
				"bpmn: couldn't create "+from, errs.BulidingFailed, err)
		}

		props = append(props, prop)
	}

	return props, nil
}
