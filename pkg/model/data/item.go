package data

import (
	"reflect"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// ******************************************************************************

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
	kind ItemKind

	// Identifies the location of the data structure and its format. If the
	// importType attribute is left unspecified, the typeLanguage specified
	// in the Definitions that contains this ItemDefinition is assumed
	importRef *foundation.Import

	// The concrete data structure to be used.
	structure Value

	// Setting this flag to true indicates that the actual data type is a
	// collection.
	isCollection bool
}

// NewItemDefinition creates a new ItemDefinition object and returns
// its pointer.
func NewItemDefinition(
	value Value,
	opts ...options.Option,
) (*ItemDefinition, error) {
	cfg := itemConfig{
		baseOptions: []options.Option{},
		kind:        Information,
		str:         value,
		collection:  false,
	}

	// check if value is a collection
	if value != nil {
		_, ok := value.(Collection)
		cfg.collection = ok
	}

	for _, opt := range opts {
		switch o := opt.(type) {
		case foundation.BaseOption:
			cfg.baseOptions = append(cfg.baseOptions, opt)

		case itemOption:
			if err := opt.Apply(&cfg); err != nil {
				return nil, err
			}

		default:
			return nil,
				&errs.ApplicationError{
					Message: "invalid option type (only BaseOption and itemOption expected)",
					Classes: []string{
						errorClass,
						errs.InvalidObject,
					},
					Details: map[string]string{
						"wrong_type": reflect.TypeOf(o).String(),
					},
				}
		}
	}

	return cfg.itemDef()
}

// MustItemDefinition tries to create a new ItemDefinition and returns its
// pointer on success or fires panic on error.
func MustItemDefinition(value Value, opts ...options.Option) *ItemDefinition {
	iDef, err := NewItemDefinition(value, opts...)
	if err != nil {
		panic(err.Error())
	}

	return iDef
}

// Kind returns kind of the ItemDefinition.
func (idef *ItemDefinition) Kind() ItemKind {
	return idef.kind
}

// Import returns import definition for the ItemDefinition.
func (idef *ItemDefinition) Import() *foundation.Import {
	return idef.importRef
}

// Value returns the ItemDefinition value.
func (idef *ItemDefinition) Structure() Value {
	return idef.structure
}

// IsCollection returns if the ItemDefinition object is collection.
func (idef *ItemDefinition) IsCollection() bool {
	return idef.isCollection
}

// ******************************************************************************

// Several elements in BPMN are subject to store or convey items during process
// execution. These elements are referenced generally as “item-aware elements.”
// This is similar to the variable construct common to many languages. As with
// variables, these elements have an ItemDefinition.
//
// The data structure these elements hold is specified using an associated
// ItemDefinition. An ItemAwareElement MAY be underspecified, meaning that the
// structure attribute of its ItemDefinition is optional if the modeler does not
// wish to define the structure of the associated data.
//
// The elements in the specification defined as item-aware elements are:
// Data Objects, Data Object References, Data Stores, Properties, DataInputs
// and DataOutputs.
type ItemAwareElement struct {
	foundation.BaseElement

	// Specification of the items that are stored or conveyed by the
	// ItemAwareElement.
	ItemSubject *ItemDefinition

	dataState *DataState
}

// NewItemAwareElement creates a new DataAwareItem and returns its pointer.
func NewItemAwareElement(
	item *ItemDefinition,
	state *DataState,
	baseOpts ...options.Option,
) *ItemAwareElement {
	if state == nil {
		state = &UndefinedDataState
	}

	return &ItemAwareElement{
		BaseElement: *foundation.MustBaseElement(baseOpts...),
		ItemSubject: item,
		dataState:   state,
	}
}

// DataState returns a copy of the ItemAwareElement DataState.
func (iae *ItemAwareElement) DataState() DataState {
	return *iae.dataState
}
