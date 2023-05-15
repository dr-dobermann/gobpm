package service

import "github.com/dr-dobermann/gobpm/pkg/model/common"

type Operation[inMsgT, outMsgT any] struct {
	common.NamedElement

	inMessage  *common.Message[inMsgT]
	outMessage *common.Message[outMsgT]

	errors []common.Error

	implementation common.ItemDefinition
}

type OperationExecutor interface {
	ExecOperation() error
}
