package service

import "github.com/dr-dobermann/gobpm/pkg/model/common"

type Operation struct {
	common.NamedElement

	inMessage  *common.Message
	outMessage *common.Message

	errors []common.Error

	implementation common.ItemDefinition
}

type OperationExecutor interface {
	ExecOperation() error
}
