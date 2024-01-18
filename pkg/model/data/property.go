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

func NewProperty(
	id, name string,
	subj *ItemDefinition,
	state *DataState,
	docs ...*foundation.Documentation,
) *Property {
	return &Property{
		ItemAwareElement: *NewItemAwareElement(id, subj, state, docs...),
		name:             name,
	}
}
