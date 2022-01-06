package excenv

import (
	"github.com/dr-dobermann/gobpm/model"
	"github.com/dr-dobermann/srvbus"
	"go.uber.org/zap"
)

type ExecutionEnvironment interface {
	InstanceID() model.Id

	// returns track logger
	Logger() *zap.SugaredLogger
	Snapshot() *model.Process
	VStore() *model.VarStore
	SrvBus() *srvbus.ServiceBus

	// returns a given queue name or
	// defautl instance's message queue name
	MSQueue(queue string) string
}
