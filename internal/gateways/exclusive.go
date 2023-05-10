package gateways

import (
	"context"

	"github.com/dr-dobermann/gobpm/internal/errs"
	"github.com/dr-dobermann/gobpm/pkg/common"
	"github.com/dr-dobermann/gobpm/pkg/thresher/executor"
)

type ExclusiveGatewayExecutor struct {
	gateways.ExclusiveGateway
}

func GetExclusiveGExecutor(
	eg *gateways.ExclusiveGateway) *ExclusiveGatewayExecutor {

	if eg == nil {
		return nil
	}

	return &ExclusiveGatewayExecutor{*eg}
}

func (ege *ExclusiveGatewayExecutor) Exec(
	_ context.Context,
	exEnv executor.ExecutionEnvironment) ([]*common.SequenceFlow, error) {

	return nil, errs.ErrNotImplementedYet
}
