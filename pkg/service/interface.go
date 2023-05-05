package service

import "github.com/dr-dobermann/gobpm/pkg/common"

type Interface struct {
	common.NamedElement

	operations []OperationExecutor
	callables  []common.CallableElement

	implementation common.ItemDefinition
}
