package data

import "github.com/dr-dobermann/gobpm/pkg/model/foundation"

type ItemKind string

const (
	Physical    ItemKind = "Physical"
	Information ItemKind = "Information"
)

// BPMN elements, such as DataObjects and Messages, represent items that are
// manipulated, transferred, transformed, or stored during Process flows.
// These items can be either physical items, such as the mechanical part of a
// vehicle, or information items such the catalog of the mechanical parts of a
// vehicle.
// An important characteristics of items in Process is their structure. BPMN
// does not require a particular format for this data structure, but it does
// designate XML Schema as its default. The structure attribute references the
// actual data structure.
// The default format of the data structure for all elements can be specified
// in the Definitions element using the typeLanguage attribute. For example, a
// typeLanguage value of http://www.w3.org/2001/XMLSchema” indicates that the
// data structures using by elements within that Definitions are in the form
// of XML Schema types. If unspecified, the default is XML schema. An Import is
// used to further identify the location of the data structure (if applicable).
// For example, in the case of data structures contributed by an XML schema,
// an Import would be used to specify the file location of that schema.
type ItemDefinition struct {
	foundation.BaseElement

	// This defines the nature of the Item. Possible values are physical or
	// information. The default value is information.
	Kind ItemKind

	// Identifies the location of the data structure and its format. If the
	// importType attribute is left unspecified, the typeLanguage specified
	// in the Definitions that contains this ItemDefinition is assumed
	Import *foundation.Import

	// The concrete data structure to be used.
	Structure any

	// Setting this flag to true indicates that the actual data type is a
	// collection.
	IsCollection bool
}

// NewItemDefinition creates a new ItemDefinition object and returns
// its pointer.
func NewItemDefinition(
	id string,
	kind ItemKind,
	str any,
	collection bool,
	imprt *foundation.Import,
	docs ...*foundation.Documentation,
) *ItemDefinition {
	return &ItemDefinition{
		BaseElement:  *foundation.NewBaseElement(id, docs...),
		Kind:         kind,
		Import:       imprt,
		Structure:    str,
		IsCollection: collection,
	}
}