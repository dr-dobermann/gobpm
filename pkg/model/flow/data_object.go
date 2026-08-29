package flow

import (
	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

type (
	// AssociationSource is implemented by Nodes which provides output parameters
	// as sources for DataNode over outgoing data associations.
	AssociationSource interface {
		Node

		// Outputs returns list of Node's output parameters.
		Outputs() []*data.ItemAwareElement

		// BindOutgoing adds new data association to a Node.
		BindOutgoing(oa *data.Association) error
	}

	// AssociationTarget is implemented by Nodes which use value of the DataNode
	// as source for incoming parameters over incoming data associations.
	AssociationTarget interface {
		Node

		// Inputs returns the Node's input parameters.
		Inputs() []*data.ItemAwareElement

		// BindIncoming adds incoming data association to a Node.
		BindIncoming(ia *data.Association) error
	}
)
