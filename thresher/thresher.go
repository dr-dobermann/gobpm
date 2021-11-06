package thresher

import (
	"github.com/dr-dobermann/gobpm/ctr"
	"github.com/dr-dobermann/gobpm/model"
)

// ProcessInstance represents a single run-time process instance
type ProcessInstance struct {
	// the copy of the process model the instance is based on
	snapshot *model.Process
	vs       model.VarStore

	monitor *ctr.Monitor
	audit   *ctr.Audit
}

type Thresher struct {
	id        model.Id
	instances []*ProcessInstance
}

func NewThreshser() *Thresher {

	return nil
}
