package gateways

import (
	"context"

	"github.com/dr-dobermann/gobpm/internal/exec"
	"github.com/dr-dobermann/gobpm/internal/renv"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

type ExclusiveGateway struct {
	Gateway

	scope scope.Scope
}

// NewExclusiveGateway creates a new ExclusiveGateway.
func NewExclusiveGateway(opts ...options.Option) (*ExclusiveGateway, error) {
	g, err := New(opts...)
	if err != nil {
		return nil,
			errs.New(
				errs.M("gate building failed"),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(err))
	}

	return &ExclusiveGateway{
			Gateway: *g,
		},
		nil
}

// Exec runs single node and returns its valid
// output sequence flows on success or error on failure.
//
// NOTE: Now fails during evaluation stops process execution.
// It's possible to consider evaluation fails as condition failure and
// continue process execution which is not passing the flow with failed
// condition.
func (eg *ExclusiveGateway) Exec(
	ctx context.Context,
	re renv.RuntimeEnvironment,
) ([]*flow.SequenceFlow, error) {
	flows := []*flow.SequenceFlow{}

	if re.Scope() == nil {
		return nil,
			errs.New(
				errs.M("runtime environment has no Scope"),
				errs.C(errorClass, errs.InvalidObject),
				errs.D("runtime_id", re.InstanceId()),
				errs.D("exclusive_gateway_id", eg.Id()))
	}

	eg.scope = re.Scope()

	for _, of := range eg.Outgoing() {
		cond := of.Condition()
		if cond == nil && of.Id() == eg.defaultFlow.Id() {
			continue
		}

		if cond.ResultType() != "bool" {
			return nil,
				errs.New(
					errs.M("invalid condition expression type"),
					errs.C(errorClass, errs.TypeCastingError),
					errs.D("outgoing_flow_id", of.Id()),
					errs.D("exclusive_gateway_id", eg.Id()))
		}

		res, err := cond.Evaluate(eg)
		if err != nil {
			return nil,
				errs.New(
					errs.M("flow condition evaluation failed"),
					errs.C(errorClass, errs.OperationFailed),
					errs.D("outgoing_flow_id", of.Id()),
					errs.D("exclusive_gateway_id", eg.Id()),
					errs.E(err))
		}

		if res.Get() == true {
			flows = append(flows, of)
		}
	}

	// if there is no path with successful condition, default flow should be
	// used. If there is no available outgoing flows the error returned.
	if len(flows) == 0 {
		if eg.defaultFlow == nil {
			return nil,
				errs.New(
					errs.M("no available outgoing flows"),
					errs.C(errorClass, errs.InvalidState),
					errs.D("exclusive_gateway_id", eg.Id()))
		}

		flows = append(flows, eg.defaultFlow)
	}

	return flows, nil
}

// ----------------------- data.Source interface ------------------------------

// Get returns Data object named name.
func (eg *ExclusiveGateway) Find(
	ctx context.Context,
	name string,
) (data.Data, error) {
	if eg.scope == nil {
		return nil,
			errs.New(
				errs.M("object Scope isn't set"),
				errs.C(errorClass, errs.InvalidState),
				errs.D("exclusive_gate_id", eg.Id()))
	}

	d, err := eg.scope.GetData(eg.scope.Root(), name)
	if err != nil {
		return nil, err
	}

	return d, nil
}

// ----------------------------------------------------------------------------

// interface check
var (
	_ exec.NodeExecutor = (*ExclusiveGateway)(nil)

	_ data.Source = (*ExclusiveGateway)(nil)
)
