// Package dataobjects provides BPMN data object implementations.
package dataobjects

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

const errorClass = "DATA_OBJECTS_ERROR"

// DataObject class is an item-aware element. Data Object elements MUST be
// contained within Process or Sub-Process elements. Data Object elements are
// visually displayed on a Process diagram. Data Object References are a way to
// reuse Data Objects in the same diagram. They can specify different states of
// the same Data Object at different points in a Process. Data Object Reference
// cannot specify item definitions, and Data Objects cannot specify states. The
// names of Data Object References are derived by concatenating the name of the
// referenced Data Data Object the state of the Data Object Reference in square
// brackets as follows: <Data Object Name> [ <Data Object Reference State> ].
type DataObject struct {
	flow.BaseElement
	incoming *data.Association
	outgoing map[string]*data.Association
	data.ItemAwareElement
}

// New creates and returns a new DataObject and returns its pointer.
func New(
	name string,
	idef *data.ItemDefinition,
	state *data.SrcState,
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

	if err := data.CheckName(name, errorClass); err != nil {
		return nil, err
	}

	if err := data.CheckReservedName(name, errorClass); err != nil {
		return nil, err
	}

	if idef == nil {
		return nil,
			fmt.Errorf("empty ItemDefinition isn't allowed")
	}

	iae, err := data.NewItemAwareElement(idef, state, foundation.WithID(idef.ID()))
	if err != nil {
		return nil,
			fmt.Errorf("ItemAwareElement building failed: %w", err)
	}

	// Name the IAE after the DataObject so an association bound to it exposes the
	// DataObject's scope name (SRD-063 FR-5) — the runtime resolves the
	// per-instance DataObject in scope by this name, not by the ItemDefinition id
	// it shares with the activity param (§10.4.2 source/target type-match).
	iae.SetName(name)

	fe, err := flow.NewBaseElement(name, baseOpts...)
	if err != nil {
		return nil,
			fmt.Errorf("BaseElement building failed: %w", err)
	}

	do := DataObject{
		ItemAwareElement: *iae,
		BaseElement:      *fe,
		outgoing:         map[string]*data.Association{},
	}

	return &do, nil
}

// AssociateSource creates a new data association between the Node n as a
// source and the DataObject as a target.
func (do *DataObject) AssociateSource(
	n flow.AssociationSource,
	sourceIDs []string,
	transformation data.FormalExpression,
	shape ...options.Option,
) error {
	if n == nil {
		return fmt.Errorf("empty Node isn't allowed")
	}

	outputs := n.Outputs()
	opts := []options.Option{}

	for _, sID := range sourceIDs {
		sID = strings.TrimSpace(sID)

		idx := slices.IndexFunc(outputs,
			func(iae *data.ItemAwareElement) bool {
				return iae.ItemDefinition().ID() == sID
			})
		if idx == -1 {
			return fmt.Errorf("node %q doesn't have output with id %q",
				n.Name(), sID)
		}

		opts = append(opts, data.WithSource(outputs[idx]))
	}

	if transformation != nil {
		opts = append(opts, data.WithTransformation(transformation))
	}

	opts = append(opts, shape...)

	a, err := data.NewAssociation(&do.ItemAwareElement, opts...)
	if err != nil {
		return fmt.Errorf("association building failed: %w", err)
	}

	if err := n.BindOutgoing(a); err != nil {
		return fmt.Errorf(
			"couldn't bind outgoing data association to node %q: %w",
			n.Name(), err)
	}

	do.incoming = a

	return nil
}

// AssociateTarget creates a new data association from the DataObject a as a
// source and the Node n as a target — the node's input over the
// DataObject's item.
func (do *DataObject) AssociateTarget(
	n flow.AssociationTarget,
	transformation data.FormalExpression,
	shape ...options.Option,
) error {
	itemID := do.ItemDefinition().ID()

	return do.associateTarget(n, transformation, "#"+itemID,
		func(iae *data.ItemAwareElement) bool {
			return iae.ItemDefinition().ID() == itemID
		}, shape)
}

// AssociateTargetInput creates a new data association from the DataObject
// as a source into the Node n's input with id inputID — for a node whose
// inputs are not addressed by item: an event's data input carries its
// definition's item, so a file's association names the input itself
// (SRD-094 FR-7).
func (do *DataObject) AssociateTargetInput(
	n flow.AssociationTarget,
	inputID string,
	transformation data.FormalExpression,
	shape ...options.Option,
) error {
	return do.associateTarget(n, transformation, strconv.Quote(inputID),
		func(iae *data.ItemAwareElement) bool {
			return iae.ID() == inputID
		}, shape)
}

// associateTarget is the body of the two AssociateTarget forms: the node's
// input is the one pick accepts, want names it in the refusal.
func (do *DataObject) associateTarget(
	n flow.AssociationTarget,
	transformation data.FormalExpression,
	want string,
	pick func(*data.ItemAwareElement) bool,
	shape []options.Option,
) error {
	if n == nil {
		return fmt.Errorf("empty target")
	}

	if _, ok := do.outgoing[n.ID()]; ok {
		return fmt.Errorf("duplicate association to node %q", n.Name())
	}

	inputs := n.Inputs()

	idx := slices.IndexFunc(inputs, pick)
	if idx == -1 {
		return fmt.Errorf("node %q has no input %s", n.Name(), want)
	}

	opts := []options.Option{data.WithSource(&do.ItemAwareElement)}
	if transformation != nil {
		opts = append(opts, data.WithTransformation(transformation))
	}

	opts = append(opts, shape...)

	a, err := data.NewAssociation(inputs[idx], opts...)
	if err != nil {
		return fmt.Errorf("association building failed: %w", err)
	}

	if err := n.BindIncoming(a); err != nil {
		return fmt.Errorf(
			"couldn't bind incoming data association to node %q: %w",
			n.Name(), err)
	}

	do.outgoing[n.ID()] = a

	return nil
}

// Clone returns a deep copy of the DataObject for per-instance isolation
// (SRD-063 FR-2). The item-aware value is copied fresh, so no two instances
// share DataObject state; the identity (name/ID) is preserved so the snapshot
// wiring can match the clone to the original. The data associations are left
// empty — they are re-established against the cloned nodes by the snapshot's
// wiring pass, exactly as a node clones then re-wires its flows.
func (do *DataObject) Clone() (*DataObject, error) {
	iae, err := do.ItemAwareElement.Clone()
	if err != nil {
		return nil, errs.New(
			errs.M("couldn't clone DataObject %q item-aware element", do.Name()),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err))
	}

	return &DataObject{
		BaseElement:      do.BaseElement,
		ItemAwareElement: *iae,
		outgoing:         map[string]*data.Association{},
	}, nil
}

// CloneDataObjects deep-copies a slice of DataObjects for per-instance isolation
// (the data.CloneProperties peer). A nil slice clones to nil.
func CloneDataObjects(dos []*DataObject) ([]*DataObject, error) {
	if dos == nil {
		return nil, nil
	}

	cloned := make([]*DataObject, len(dos))
	for i, do := range dos {
		c, err := do.Clone()
		if err != nil {
			return nil, errs.New(
				errs.M("couldn't clone data object %q", do.Name()),
				errs.C(errorClass, errs.OperationFailed),
				errs.E(err))
		}

		cloned[i] = c
	}

	return cloned, nil
}

// ------------------ Element interface ----------------------------------------

// Name returns the DataObject name.
func (do *DataObject) Name() string {
	return do.BaseElement.Name()
}

// EType returns the element type of the DataObject (flow.Element). It MUST
// override the panicking BaseElement.EType so a DataObject can be added to a
// Process/SubProcess container (SRD-063 FR-1).
func (do *DataObject) EType() flow.ElementType {
	return flow.DataObjectElement
}

// -------------------- foundation.Documentator -------------------------------

// Docs returns the documentation of the DataObject.
func (do *DataObject) Docs() []*foundation.Documentation {
	return do.BaseElement.Docs()
}

// -------------------- foundation.Identifyer ---------------------------------

// ItemAware returns the DataObject's item-aware element — what an
// association names when it takes this object as a SOURCE (SRD-097 FR-7).
// A single-source association gets it from the object it is attached to;
// several sources need it by hand, because only one of them owns the
// attach.
func (do *DataObject) ItemAware() *data.ItemAwareElement {
	return &do.ItemAwareElement
}

// ID returns the identifier of the DataObject.
func (do *DataObject) ID() string {
	return do.BaseElement.ID()
}

// ----------------------------------------------------------------------------

// interfaces test for DataObject.
var (
	_ flow.Element = (*DataObject)(nil)
	_ data.Data    = (*DataObject)(nil)
)
