package gateways

import "github.com/dr-dobermann/gobpm/pkg/model/common"

type Direction string

const (
	Unspecified Direction = "Unspecified"
	Converging  Direction = "Converging"
	Diverging   Direction = "Diverging"
	Mixed       Direction = "Mixed"
)

type Type string

const (
	Parallel  Type = "Parallel"
	Exclusive Type = "Exclusive"
)

// *****************************************************************************
type Gateway struct {
	common.FlowNode

	Direction Direction
}
