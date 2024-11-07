package flow

import "context"

type (
	DataNode interface {
		// Update recalculates the value of the DataNode according
		// to values of incoming data association and updates value and
		// state of outgoing data associations.
		Update(ctx context.Context) error
	}

	// AssociationSource is implemented by Nodes which could use
	// associated DataObject as a data source over incoming data association.
	AssociationSource interface {
		// AssociateFrom binds do as a data source object and
		// makes incoming data association to Node.
		AssociateFrom(dn DataNode, iDefId string) error
	}

	// AssociationTarget is implemented by Nodes which could
	// be a data source for associated over data associations DataObjects.
	AssociationTarget interface {
		// AssociateTo binds do as a target and creates
		// outgoing data association from Node.
		AssociateTo(dn DataNode, iDefId string) error
	}
)
