package data

import (
	"fmt"
	"reflect"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

type ItemKind string

const (
	PhysicalKind    ItemKind = "Physical"
	InformationKind ItemKind = "Information"
)

// Validate checks ic value to comply with ItemKind restriction.
func (ic ItemKind) Validate() error {
	if ic != PhysicalKind && ic != InformationKind {
		return errs.New(
			errs.M("invalid ItemKind: %s", ic),
			errs.C(errorClass, errs.TypeCastingError))
	}

	return nil
}

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
		kind:        InformationKind,
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
				errs.New(
					errs.M("invalid option type: %s", reflect.TypeOf(o).String()),
					errs.C(errorClass, errs.InvalidObject))
		}
	}

	return cfg.itemDef()
}

// MustItemDefinition tries to create a new ItemDefinition and returns its
// pointer on success or fires panic on error.
func MustItemDefinition(value Value, opts ...options.Option) *ItemDefinition {
	iDef, err := NewItemDefinition(value, opts...)
	if err != nil {
		errs.Panic(err)
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

func (idef *ItemDefinition) String() string {
	val := "<nil>"

	if idef.structure != nil {
		val = fmt.Sprint(idef.structure.Get())
	}

	return fmt.Sprintf("Id: %s\nValue: %s\nIsCollection: %t\nKind: %s\n",
		idef.Id(), val, idef.isCollection, idef.kind)
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
	itemSubject *ItemDefinition

	dataState DataState
}

// NewItemAwareElement creates a new DataAwareItem and returns its pointer.
func NewItemAwareElement(
	item *ItemDefinition,
	state *DataState,
	baseOpts ...options.Option,
) (*ItemAwareElement, error) {
	if item == nil {
		return nil,
			errs.New(
				errs.M("empty item isn't allowed"),
				errs.C(errorClass, errs.InvalidParameter))
	}

	if state == nil {
		if UnavailableDataState == nil {
			return nil,
				errs.New(
					errs.M("default DataStates are not set.\n"+
						"if you need to use default DataStates, "+
						"run data.CreateDefaultStates"),
					errs.C(errorClass, errs.BulidingFailed))
		}

		state = UnavailableDataState
	}

	be, err := foundation.NewBaseElement(baseOpts...)
	if err != nil {
		return nil, err
	}

	return &ItemAwareElement{
			BaseElement: *be,
			itemSubject: item,
			dataState:   *state,
		},
		nil
}

// MustItemAwareElement creates a new ItemAwareElement and returns its pointer
// or panics on failure.
func MustItemAwareElement(
	item *ItemDefinition,
	state *DataState,
	baseOpts ...options.Option,
) *ItemAwareElement {
	iae, err := NewItemAwareElement(item, state, baseOpts...)
	if err != nil {
		errs.Panic(err.Error())
	}

	return iae
}

// State returns a copy of the ItemAwareElement DataState.
func (iae *ItemAwareElement) State() DataState {
	return iae.dataState
}

// Update
func (iae *ItemAwareElement) UpdateState(newState *DataState) error {
	if newState == nil {
		return errs.New(
			errs.M("empty data state"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	iae.dataState = *newState

	return nil
}

// Subject returns internal representeation of the ItemAwareElement.
func (iae *ItemAwareElement) Subject() *ItemDefinition {
	return iae.itemSubject
}

// IsCollection returns flag is the ItemAwareElement collection or not.
func (iae *ItemAwareElement) IsCollection() bool {
	if iae.itemSubject == nil {
		return false
	}

	return iae.itemSubject.isCollection
}

func (iae *ItemAwareElement) String() string {
	return fmt.Sprintf("Id: %s\nState: %s\nValue: %s\n",
		iae.Id(), iae.dataState.name, iae.itemSubject.String())
}

// ----------------- data.Data interface ---------------------------------------

// Value returns underlaying structure value of the ItemAvareElement.
func (iae *ItemAwareElement) Value() Value {
	if iae.itemSubject == nil {
		return nil
	}

	return iae.itemSubject.structure
}

// ItemDefinition returns the Data's underlaying ItemDefinition.
func (iae *ItemAwareElement) ItemDefinition() *ItemDefinition {
	return iae.itemSubject
}

// -----------------------------------------------------------------------------
