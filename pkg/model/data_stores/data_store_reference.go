// Package datastores provides the BPMN Data Store Reference: the flow-scope
// handle to the engine-global Data Store (BPMN §10.4.1, ADR-030 §2.6). Unlike a
// DataObject (a per-instance scope container), a DataStoreReference's data lives
// in the engine-global datastore.DataStore, so it is shared across instances and
// is never seeded into per-instance scope.
package datastores

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

const errorClass = "DATA_STORES_ERROR"

// DataStoreReference is a flow-scope ItemAwareElement that names an
// engine-global Data Store (ADR-030 §2.6). Data flowing into/out of it flows
// into/out of the store named by its dataStoreRef, keyed by its Name(); it participates in
// DataAssociations exactly like a DataObject, but its backing store is
// engine-global, not scope-resident.
type DataStoreReference struct {
	incoming     *data.Association
	outgoing     map[string]*data.Association
	dataStoreRef string
	data.ItemAwareElement
	flow.BaseElement
}

// New creates a DataStoreReference named name that targets the Data Store
// identified by dataStoreRef.
func New(
	name, dataStoreRef string,
	idef *data.ItemDefinition,
	state *data.SrcState,
	baseOpts ...options.Option,
) (*DataStoreReference, error) {
	name = strings.TrimSpace(name)
	if err := errs.CheckStr(
		name, "DataStoreReference should have non-empty name", errorClass,
	); err != nil {
		return nil, err
	}

	if err := data.CheckName(name, errorClass); err != nil {
		return nil, err
	}

	if err := data.CheckReservedName(name, errorClass); err != nil {
		return nil, err
	}

	dataStoreRef = strings.TrimSpace(dataStoreRef)
	if err := errs.CheckStr(
		dataStoreRef,
		"DataStoreReference should have a non-empty dataStoreRef", errorClass,
	); err != nil {
		return nil, err
	}

	if idef == nil {
		return nil, errs.New(
			errs.M("empty ItemDefinition isn't allowed for %q", name),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	iae, err := data.NewItemAwareElement(idef, state, foundation.WithID(idef.ID()))
	if err != nil {
		return nil, fmt.Errorf("ItemAwareElement building failed: %w", err)
	}

	iae.SetName(name)

	fe, err := flow.NewBaseElement(name, baseOpts...)
	if err != nil {
		return nil, fmt.Errorf("BaseElement building failed: %w", err)
	}

	return &DataStoreReference{
		BaseElement:      *fe,
		ItemAwareElement: *iae,
		outgoing:         map[string]*data.Association{},
		dataStoreRef:     dataStoreRef,
	}, nil
}

// DataStoreRef is the id of the engine Data Store this reference targets
// (SRD-068 FR-4): the task reroute resolves it from the engine registry, then
// reads/writes the store by this reference's Name().
func (r *DataStoreReference) DataStoreRef() string {
	return r.dataStoreRef
}

// Name returns the DataStoreReference name.
func (r *DataStoreReference) Name() string {
	return r.BaseElement.Name()
}

// ID returns the DataStoreReference id, disambiguating the flow.BaseElement and
// data.ItemAwareElement embeddings (both promote ID).
func (r *DataStoreReference) ID() string {
	return r.BaseElement.ID()
}

// EType returns the element type so a DataStoreReference can be added to a
// Process/SubProcess container (SRD-068 FR-3).
func (r *DataStoreReference) EType() flow.ElementType {
	return flow.DataStoreReferenceElement
}

// Docs returns the documentation of the DataStoreReference.
func (r *DataStoreReference) Docs() []*foundation.Documentation {
	return r.BaseElement.Docs()
}

// AssociateSource binds Node n's output (by source id) into this reference —
// the produced value is written to the engine Data Store (Node → DataStore).
// The association carries this reference's dataStoreRef so the task reroute writes
// the store instead of scope (SRD-068 FR-4).
func (r *DataStoreReference) AssociateSource(
	n flow.AssociationSource,
	sourceIDs []string,
	transformation data.FormalExpression,
) error {
	if n == nil {
		return errs.New(
			errs.M("empty Node isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	outputs := n.Outputs()
	opts := []options.Option{data.WithDataStoreRef(r.dataStoreRef)}

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

	a, err := data.NewAssociation(&r.ItemAwareElement, opts...)
	if err != nil {
		return fmt.Errorf("association building failed: %w", err)
	}

	if err := n.BindOutgoing(a); err != nil {
		return fmt.Errorf(
			"couldn't bind outgoing data association to node %q: %w",
			n.Name(), err)
	}

	r.incoming = a

	return nil
}

// AssociateTarget binds this reference as a source into Node n's input — the
// store value fills the input (DataStore → Node). The association carries this
// reference's dataStoreRef so the task reroute reads the store (SRD-068 FR-4).
func (r *DataStoreReference) AssociateTarget(
	n flow.AssociationTarget,
	transformation data.FormalExpression,
) error {
	itemID := r.ItemDefinition().ID()

	return r.associateTarget(n, transformation, "#"+itemID,
		func(iae *data.ItemAwareElement) bool {
			return iae.ItemDefinition().ID() == itemID
		})
}

// AssociateTargetInput binds this reference as a source into the Node n's
// input with id inputID — for a node whose inputs are not addressed by
// item: an event's data input carries its definition's item (SRD-094
// FR-7).
func (r *DataStoreReference) AssociateTargetInput(
	n flow.AssociationTarget,
	inputID string,
	transformation data.FormalExpression,
) error {
	return r.associateTarget(n, transformation, strconv.Quote(inputID),
		func(iae *data.ItemAwareElement) bool {
			return iae.ID() == inputID
		})
}

// associateTarget is the body of the two AssociateTarget forms: the node's
// input is the one pick accepts, want names it in the refusal.
func (r *DataStoreReference) associateTarget(
	n flow.AssociationTarget,
	transformation data.FormalExpression,
	want string,
	pick func(*data.ItemAwareElement) bool,
) error {
	if n == nil {
		return errs.New(
			errs.M("empty target"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if _, ok := r.outgoing[n.ID()]; ok {
		return fmt.Errorf("duplicate association to node %q", n.Name())
	}

	inputs := n.Inputs()

	idx := slices.IndexFunc(inputs, pick)
	if idx == -1 {
		return fmt.Errorf("node %q has no input %s", n.Name(), want)
	}

	opts := []options.Option{
		data.WithSource(&r.ItemAwareElement),
		data.WithDataStoreRef(r.dataStoreRef),
	}
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

	r.outgoing[n.ID()] = a

	return nil
}
