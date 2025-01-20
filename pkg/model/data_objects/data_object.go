package dataobjects

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
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

	// DataObject could have no more than one incoming data association.
	incoming *data.Association

	// There could be more than one outgoing data association from DataObject.
	// outgoing associations are indexed by associated Node Id.
	outgoing map[string]*data.Association
}

// New creates and returns a new DataObject and returns its pointer.
func New(
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

	if idef == nil {
		return nil,
			fmt.Errorf("empty ItemDefinition isn't allowed")
	}

	iae, err := data.NewItemAwareElement(idef, state, foundation.WithId(idef.Id()))
	if err != nil {
		return nil,
			fmt.Errorf("ItemAwareElement building failed: %w", err)
	}

	fe, err := flow.NewFlowElement(name, baseOpts...)
	if err != nil {
		return nil,
			fmt.Errorf("FlowElement building failed: %w", err)
	}

	do := DataObject{
		ItemAwareElement: *iae,
		FlowElement:      *fe,
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
) error {
	if n == nil {
		return fmt.Errorf("empty Node isn't allowed")
	}

	outputs := n.Outputs()
	opts := []options.Option{}

	for _, sId := range sourceIDs {
		sId = strings.TrimSpace(sId)

		idx := slices.IndexFunc(outputs,
			func(iae *data.ItemAwareElement) bool {
				return iae.ItemDefinition().Id() == sId
			})
		if idx == -1 {
			return fmt.Errorf("node %q doesn't have output with id %q",
				n.Name(), sId)
		}

		opts = append(opts, data.WithSource(outputs[idx]))
	}

	if transformation != nil {
		opts = append(opts, data.WithTransformation(transformation))
	}

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
// source and the Node n as a target.
func (do *DataObject) AssociateTarget(
	n flow.AssociationTarget,
	transformation data.FormalExpression,
) error {
	if n == nil {
		return fmt.Errorf("empty target")
	}

	if _, ok := do.outgoing[n.Id()]; ok {
		return fmt.Errorf("duplicate association to node %q", n.Name())
	}

	inputs := n.Inputs()
	idx := slices.IndexFunc(
		inputs,
		func(iae *data.ItemAwareElement) bool {
			return iae.ItemDefinition().Id() == do.ItemDefinition().Id()
		})

	if idx == -1 {
		return fmt.Errorf("node %q has no input #%s",
			n.Name(), do.ItemDefinition().Id())
	}

	opts := []options.Option{data.WithSource(&do.ItemAwareElement)}
	if transformation != nil {
		opts = append(opts, data.WithTransformation(transformation))
	}

	a, err := data.NewAssociation(inputs[idx], opts...)
	if err != nil {
		return fmt.Errorf("association building failed: %w", err)
	}

	if err := n.BindIncoming(a); err != nil {
		return fmt.Errorf(
			"couldn't bind incoming data association to node %q: %w",
			n.Name(), err)
	}

	do.outgoing[n.Id()] = a

	return nil
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

// -------------------- foundation.Documentator -------------------------------

func (do *DataObject) Docs() []*foundation.Documentation {
	return do.FlowElement.Docs()
}

// -------------------- foundation.Identifyer ---------------------------------

func (do *DataObject) Id() string {
	return do.FlowElement.Id()
}

// ------------------------ flow.DataNode -------------------------------------

func (do *DataObject) Update(ctx context.Context) error {
	if do.incoming != nil {
		if err := do.UpdateState(data.UnavailableDataState); err != nil {
			return fmt.Errorf("DataObject state updating failed: %w", err)
		}

		v, err := do.incoming.Value(ctx)
		if err != nil {
			return fmt.Errorf(
				"couldn't get value of incoming data association: %w",
				err)
		}

		if err := do.ItemDefinition().
			Structure().
			Update(ctx, v.Structure().Get(ctx)); err != nil {
			return fmt.Errorf("DataObject value updating failed: %w", err)
		}

		if err := do.UpdateState(data.ReadyDataState); err != nil {
			return fmt.Errorf("DataObject state updating failed: %w", err)
		}
	}

	if do.State().Name() != data.ReadyDataState.Name() {
		return fmt.Errorf(
			"DataObject state isn't Ready (actual state: %s)",
			do.State().Name())
	}

	for _, oa := range do.outgoing {
		if err := oa.UpdateSource(ctx, do.ItemDefinition(), data.Recalculate); err != nil {
			return fmt.Errorf(
				"association #%s source #%q updating failed: %w",
				oa.Id(), do.ItemDefinition().Id(), err)
		}
	}

	return nil
}

// ----------------------------------------------------------------------------

// interfaces test for DataObject.
var (
	_ flow.Element  = (*DataObject)(nil)
	_ data.Data     = (*DataObject)(nil)
	_ flow.DataNode = (*DataObject)(nil)
)
