// Package provide interface to access to the run-time environment
// from executed node.
package executor

import (
	"github.com/dr-dobermann/gobpm/pkg/identity"
	"github.com/dr-dobermann/gobpm/pkg/model"
	"github.com/dr-dobermann/gobpm/pkg/variables"
	"github.com/dr-dobermann/srvbus"
	"go.uber.org/zap"
)

type ExecutionEnvironment interface {
	InstanceID() identity.Id

	// returns track logger
	Logger() *zap.SugaredLogger
	Snapshot() *model.Process
	VStore() *variables.VarStore
	SrvBus() *srvbus.ServiceBus

	// returns a given queue name or
	// defautl instance's message queue name
	MSQueue(queue string) string
}
