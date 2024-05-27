package flow

import (
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/helpers"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// The Data Object class is an item-aware element. Data Object elements MUST be
// contained within Process or Sub-Process elements. Data Object elements are
// visually displayed on a Process diagram. Data Object References are a way to
// reuse Data Objects in the same diagram. They can specify different states of
// the same Data Object at different points in a Process. Data Object Reference
// cannot specify item definitions, and Data Objects cannot specify states. The
// names of Data Object References are derived by concatenating the name of the
// referenced Data Data Object the state of the Data Object Reference in square
// brackets as follows: <Data Object Name> [ <Data Object Reference State> ].
type DataObject struct {
	data.ItemAwareElement

	FlowElement

	// Defines if the Data Object represents a collection of elements. It is
	// needed when no itemDefinition is referenced. If an itemDefinition is
	// referenced, then this attribute MUST have the same value as the
	// isCollection attribute of the referenced itemDefinition. The default
	// value for this attribute is false.
	//
	// DEV_NOTE: Since flag IsCollection depends on internal ItemAwareElement
	// state, the dedicated value isn't necessary.
	// IsCollection bool
}

// ------------------ Element interface ----------------------------------------

// Type returns the element type of the DataObject.
func (do *DataObject) Type() ElementType {
	return DataObjectElement
}

// -----------------------------------------------------------------------------

// NewDataOpject creates and returns a new DataObject and returns its pointer.
func NewDataOpject(
	name string,
	idef *data.ItemDefinition,
	state *data.DataState,
	baseOpts ...options.Option,
) (*DataObject, error) {
	name = strings.TrimSpace(name)
	if err := helpers.CheckStr(
		name,
		"DataObject should have non-empty name",
		errorClass,
	); err != nil {
		return nil, err
	}

	iae, err := data.NewItemAwareElement(idef, state, baseOpts...)
	if err != nil {
		return nil, err
	}

	do := DataObject{
		ItemAwareElement: *iae,
	}

	return &do, nil
}

// interfaces test for DataObject.
var (
	_ Element   = (*DataObject)(nil)
	_ data.Data = (*DataObject)(nil)
)
