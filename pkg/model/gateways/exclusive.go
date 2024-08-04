package gateways

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

type ExclusiveGateway struct {
	Gateway
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

// ------------------ flow.SequenceSource interface ----------------------------

// SuportOutgoingFlow checks if it possible to source sf SequenceFlow from
// the Gateway.
func (eg *ExclusiveGateway) SupportOutgoingFlow(sf *flow.SequenceFlow) error {
	// Gateway has no restrictions on outgoing flows
	return nil
}
