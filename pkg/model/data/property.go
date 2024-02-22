package data

import "github.com/dr-dobermann/gobpm/pkg/model/options"

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
) *Property {
	return &Property{
		ItemAwareElement: *NewItemAwareElement(
			item,
			state,
			baseOpts...),
		name: name,
	}
}

// Name returns the Property name.
func (p *Property) Name() string {
	return p.name
}
