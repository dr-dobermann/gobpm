package data

import "github.com/dr-dobermann/gobpm/pkg/model/foundation"

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
	id, name string,
	item *ItemDefinition,
	state *DataState,
	docs ...*foundation.Documentation,
) *Property {
	return &Property{
		ItemAwareElement: *NewItemAwareElement(id, item, state, docs...),
		name:             name,
	}
}

// Name returns the Property name.
func (p *Property) Name() string {
	return p.name
}
