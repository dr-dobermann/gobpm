package scope

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// Scope keeps all variables of the scope and returns its values.
//
//go:generate mockery --name Scope
type Scope interface {
	// Root returns the root dataPath of the Scope.
	Root() DataPath

	// Scopes returns list of scopes controlled by Scope.
	Scopes() []DataPath

	// LoadData loads a data data.Data into the Scope into
	// the NodeDataLoader's DataPath.
	LoadData(NodeDataLoader, ...data.Data) error

	// GetData tries to return value of data.Data object with name Name.
	// dataPath selects the initial scope to look for the name.
	// If current Scope doesn't find the name, then it looks in upper
	// Scope until find or failed to find.
	GetData(dataPath DataPath, name string) (data.Data, error)

	// GetDataById tries to find data.Data in the Scope by its ItemDefinition
	// id.
	// It starts looking for the data from dataPath and continues to locate
	// it until Scope root.
	GetDataById(dataPath DataPath, id string) (data.Data, error)

	// AddData adds data.Data to the NodeDataLoader scope or to rootScope
	// if NodeDataLoader is nil.
	AddData(NodeDataLoader, ...data.Data) error

	// ExtendScope adds a new child Scope to the Scope and returns
	// its full path.
	ExtendScope(NodeDataLoader) error

	// LeaveScope calls the Scope to clear all data saved by NodeDataLoader.
	LeaveScope(NodeDataLoader) error
}

// NodeDataLoader is implemented by those nodes, which stores data while
// its execution.
type NodeDataLoader interface {
	// Name returns NodeDataLoader name to create a scope name.
	flow.Node

	// RegisterData sends all Node Data to the scope.
	//
	// DataRegistration is made by Scope.LaodData call.
	// DataPath is the path of the NodeDataLoader in the Scope. It could
	// be saved for further use (getting data from it)
	RegisterData(DataPath, Scope) error
}

// NodeDataProducer implemented by Nodes which needs to load data from
// flow.DataObject over its incoming data.Associations.
// This interface is used beffore Node execution and Node's RegisterData call.
//
// So Node's data is used as followed:
//
//  1. LoadData loads data from incoming data associations and fills its inputs.
//
//  2. Registers all data in the execution Scope (properties, inputs).
//
//     ... Execute the Node and fill Node's output parameters with results of
//     the execution.
//
//  3. On success execution all Node's output with not-Ready state are updated
//     from the Scope and then Node's UploadData is called to fill all outgoing
//     data associations from Node's outputs.
//
//  4. Clears scope from Node's data.
type NodeDataConsumer interface {
	flow.Node

	// LoadData loads Node's data from its incoming data.Associations.
	LoadData(context.Context) error
}

// NodeDataConsumer implemented by Nodes which upload data to flow.DataObjects
// over its outgoing data.Associations.
// This interface is used after Node execution.
type NodeDataProducer interface {
	flow.Node

	// UploadData uploads Node's data onto its outgoing data.Association.
	UploadData(ctx context.Context, s Scope) error
}
