package service

import "github.com/dr-dobermann/gobpm/pkg/model/common"

type Interface struct {
	common.NamedElement

	operations []OperationExecutor
	callables  []common.CallableElement

	implementation common.ItemDefinition
}

// type Executor interface {
// 	Execute(ctx context.Context) error
// }
