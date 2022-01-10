package gateways

import (
	"context"

	"github.com/dr-dobermann/gobpm/internal/errs"
	"github.com/dr-dobermann/gobpm/model"
	"github.com/dr-dobermann/gobpm/pkg/excenv"
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
	exEnv excenv.ExecutionEnvironment) ([]*model.SequenceFlow, error) {

	return nil, errs.ErrNotImplementedYet
}
