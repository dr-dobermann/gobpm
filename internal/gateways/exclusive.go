package gateways

import (
	"context"

	"github.com/dr-dobermann/gobpm/internal/errs"
	"github.com/dr-dobermann/gobpm/pkg/model"
	"github.com/dr-dobermann/gobpm/pkg/thresher/executor"
)

type ExclusiveGatewayExecutor struct {
	model.ExclusiveGateway
}

func GetExclusiveGExecutor(
	eg *model.ExclusiveGateway) *ExclusiveGatewayExecutor {

	if eg == nil {
		return nil
	}

	return &ExclusiveGatewayExecutor{*eg}
}

func (ege *ExclusiveGatewayExecutor) Exec(
	_ context.Context,
	exEnv executor.ExecutionEnvironment) ([]*model.SequenceFlow, error) {

	return nil, errs.ErrNotImplementedYet
}
