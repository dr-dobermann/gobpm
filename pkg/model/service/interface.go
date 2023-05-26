package service

import "github.com/dr-dobermann/gobpm/pkg/model/common"

type Interface struct {
	common.NamedElement

	// Operations supported by the Interface
	operations []OperationExecutor

	// callables consists of the references to CallableElements which are
	// use the Interface.
	// This link is a bad design, becouse it makes hard-link between
	// the Interface and its users.
	// callables  []common.CallableElement

	implementation common.ItemDefinition
}

// type Executor interface {
// 	Execute(ctx context.Context) error
// }
