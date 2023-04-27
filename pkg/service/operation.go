package service

import "github.com/dr-dobermann/gobpm/pkg/common"

type Operation struct {
	common.NamedElement

	inMessage  *common.Message
	outMessage *common.Message

	errors []common.Error

	implementation common.ItemDefinition
}
