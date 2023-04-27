package service

import "github.com/dr-dobermann/gobpm/pkg/common"

type Interface struct {
	common.NamedElement

	operations []Operation
	callables  []common.CallableElement

	implementation common.ItemDefinition
}
