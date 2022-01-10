package instance

import (
	"strings"

	"github.com/dr-dobermann/gobpm/model"
	"github.com/dr-dobermann/gobpm/pkg/executor"
	"github.com/dr-dobermann/srvbus"
	"go.uber.org/zap"
)

// implementation of the excenv interface for track

func (tr *track) InstanceID() model.Id {
	return tr.instance.id
}

func (tr *track) Logger() *zap.SugaredLogger {
	return tr.log
}

func (tr *track) Snapshot() *model.Process {
	return tr.instance.snapshot
}

func (tr *track) VStore() *model.VarStore {
	return tr.instance.vs
}

func (tr *track) SrvBus() *srvbus.ServiceBus {
	return tr.instance.sBus
}

func (tr *track) MSQueue(queue string) string {
	if strings.Trim(queue, " ") == "" {
		return tr.instance.mQueue
	}

	return queue
}

// implements GateKeeper interface
func (tr *track) GetGExecutorInstance(
	ge executor.GatewayExecutor) executor.GatewayExecutor {

	return tr.instance.getGExInstance(ge)
}
