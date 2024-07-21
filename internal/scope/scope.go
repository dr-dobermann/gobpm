package scope

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// All Nodes supports the Node's Data Model.
//
// Node's data is used as followed:
//
//  1. LoadData loads data from incoming data associations and fills its inputs.
//
//  2. Node's data(properties, inputs) are registered in the execution Scope by
//     Register Data.
//     The path to the stored data created by external Scope and sends it to
//     NodeDataLoader for further access to stored data.
//
//     ... Execute the Node and fill Node's output parameters with results of
//     the execution.
//
//  3. On success execution all Node's output with not-Ready state are updated
//     from the Scope and then Node's UploadData is called to fill all outgoing
//     data associations from Node's outputs.
//
//  4. Clears scope from Node's data by LeaveScope call.

// Scope keeps all variables of the scope and returns its values.
type Scope interface {
	// Root returns the root dataPath of the Scope.
	Root() DataPath

	// Scopes returns list of scopes controlled by Scope.
	Scopes() []DataPath

	// LoadData loads data.Data into the Scope on
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

// NodeDataLoader is implemented by Nodes, which stores data in
// external Scope before Node execution to make them accessible in Node
// execution or condiitons checks.
type NodeDataLoader interface {
	// NodeDataLoader name is used to create the Node's scope name.
	flow.Node

	// RegisterData sends all Node Data (properties, inputs) to the Node's
	// scope.
	//
	// DataRegistration is called by Scope.LaodData.
	// DataPath is the path of the NodeDataLoader data in the Scope.
	// It should be saved for further getting data from it.
	RegisterData(DataPath, Scope) error
}

// NodeDataConsumer implemented by Nodes which load data from flow.DataObjects
// binded to Node over incoming data.Associations.
// This interface is called before Node execution and Node's RegisterData call.
type NodeDataConsumer interface {
	flow.Node

	// LoadData loads Node's data from its incoming data.Associations.
	LoadData(context.Context) error
}

// NodeDataProducer implemented by Nodes which needs to upload data to
// flow.DataObject binded to Node over outgoing data.Associations.
// This interface is called after the Node execution.
type NodeDataProducer interface {
	flow.Node

	// UploadData uploads Node's data into flow.DataObjects binded to Node over
	// its outgoing data.Association.
	UploadData(ctx context.Context, s Scope) error
}
