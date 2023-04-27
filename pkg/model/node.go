package model

import (
	"github.com/dr-dobermann/gobpm/pkg/common"
	mid "github.com/dr-dobermann/gobpm/pkg/identity"
)

type Node interface {
	ID() mid.Id
	Name() string
	Type() common.FlowElementType
	LaneName() string
	ProcessID() mid.Id
	PutOnLane(lane *Lane) error
	Connect(fn Node, sName string) (common.SequenceFlow, error)

	HasIncoming() bool

	// deletes all incoming and outcoming flows when copying the node
	// only calls from proccess.Copy method to avoid duplication flows
	// on copied node.
	//
	// DO NOT CALL directly!
	//
	ClearFlows()
}
