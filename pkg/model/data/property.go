package data

import "github.com/dr-dobermann/gobpm/pkg/model/options"

// PropertyOwner is an interface of objects which could have Properties.
type PropertyOwner interface {
	Properties() []Property
}

// Properties, like Data Objects, are item-aware elements. But, unlike Data
// Objects, they are not visually displayed on a Process diagram. Certain flow
// elements MAY contain properties, in particular only Processes, Activities,
// and Events MAY contain Properties.
type Property struct {
	ItemAwareElement

	// Defines the name of the Property.
	name string
}

// NewProperty creates a new Property object and returns its pointer.
func NewProperty(
	name string,
	item *ItemDefinition,
	state *DataState,
	baseOpts ...options.Option,
) (*Property, error) {
	name = trim(name)
	if err := checkStr(name, "property should has non-empty name"); err != nil {
		return nil, err
	}

	iae, err := NewItemAwareElement(item, state, baseOpts...)
	if err != nil {
		return nil, err
	}

	return &Property{
			ItemAwareElement: *iae,
			name:             name},
		nil
}

// Name returns the Property name.
func (p *Property) Name() string {
	return p.name
}
