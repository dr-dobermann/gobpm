package model

import (
	"github.com/dr-dobermann/gobpm/ctr"
)

type Lane struct {
}

type Process struct {
	FlowElementsContainer
	version     uint64
	supportedBy []string // processes supported this one
	lanes       []Lane

	monitor *ctr.Monitor
	audit   *ctr.Audit
}
