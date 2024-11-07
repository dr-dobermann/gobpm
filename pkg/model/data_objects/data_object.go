package dataobjects

import (
	"fmt"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

const errorClass = "DATA_OBJECTS_ERROR"

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

	flow.FlowElement

	// Defines if the Data Object represents a collection of elements. It is
	// needed when no itemDefinition is referenced. If an itemDefinition is
	// referenced, then this attribute MUST have the same value as the
	// isCollection attribute of the referenced itemDefinition. The default
	// value for this attribute is false.
	//
	// DEV_NOTE: Since the flag IsCollection depends on internal ItemAwareElement
	// ItemDefinition, this flag could be checked from there.
	// IsCollection bool

	// associations keeps all DataObject's association to Nodes.
	associations map[data.Direction][]*data.Association
}

// ------------------ Element interface ----------------------------------------

// Name returns the DataObject name.
func (do *DataObject) Name() string {
	return do.FlowElement.Name()
}

// Type returns the element type of the DataObject.
func (do *DataObject) Type() flow.ElementType {
	return flow.DataObjectElement
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
	if err := errs.CheckStr(
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
		associations:     map[data.Direction][]*data.Association{},
	}

	return &do, nil
}

// AssociateSource creates a new data association between the Node n as a
// source and the DataObject as a target.
func (do *DataObject) AssociateSource(
	n flow.AssociationSource,
	sourceIDs []string,
	transformation data.FormalExpression,
) error {
	if n == nil {
		return fmt.Errorf("empty Node isn't allowed")
	}

	return fmt.Errorf("not implemented yet")
}

// AssociateTarget creates a new data association from the DataObject a as a
// source and the Node n as a target.
func (do *DataObject) AssociateTarget(
	n flow.AssociationTarget,
	transformation data.FormalExpression,
) error {
	return fmt.Errorf("not implemented yet")
}

// ----------------------------------------------------------------------------

// interfaces test for DataObject.
var (
	_ flow.Element = (*DataObject)(nil)
	_ data.Data    = (*DataObject)(nil)
)
