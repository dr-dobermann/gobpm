package instance

import (
	"strings"

	"github.com/dr-dobermann/gobpm/internal/gateways"
	"github.com/dr-dobermann/gobpm/pkg/identity"
	"github.com/dr-dobermann/gobpm/pkg/model"
	"github.com/dr-dobermann/gobpm/pkg/variables"
	"github.com/dr-dobermann/srvbus"
	"go.uber.org/zap"
)

// implementation of the executror.ExecutionEnvironment interface for track

func (tr *track) InstanceID() identity.Id {
	return tr.instance.id
}

func (tr *track) Logger() *zap.SugaredLogger {
	return tr.log
}

func (tr *track) Snapshot() *model.Process {
	return tr.instance.snapshot
}

func (tr *track) VStore() *variables.VarStore {
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
	ge gateways.GatewayExecutor) gateways.GatewayExecutor {

	return tr.instance.getGExInstance(ge)
}
