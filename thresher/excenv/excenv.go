// Package provide interface to access from executed
// node to the run-time environment.
package excenv

import (
	"github.com/dr-dobermann/gobpm/model"
	"github.com/dr-dobermann/gobpm/pkg/identity"
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
