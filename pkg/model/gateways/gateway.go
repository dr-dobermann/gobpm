package gateways

import (
	"errors"
	"reflect"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

type GDirection string

const (
	Unspecified GDirection = "Unspecified"
	Converging  GDirection = "Converging"
	Diverging   GDirection = "Diverging"
	Mixed       GDirection = "Mixed"
)

func (d GDirection) Validate() error {
	if d == Unspecified || d == Converging ||
		d == Diverging || d == Mixed {
		return nil
	}

	return errs.New(
		errs.M("invalid gateway direction"),
		errs.C(errorClass, errs.InvalidObject),
		errs.D("direction", string(d)))
}

type Gateway struct {
	foundation.BaseElement

	flow.FlowNode

	flow.FlowElement

	direction GDirection

	defaultFlow *flow.SequenceFlow
}

// New creates a new Gateway with options opts.
func New(opts ...options.Option) (*Gateway, error) {
	gc := gatewayConfig{
		direction: Unspecified,
		baseOpts:  []options.Option{},
	}

	ee := []error{}

	for _, opt := range opts {
		switch o := opt.(type) {
		case foundation.BaseOption:
			gc.baseOpts = append(gc.baseOpts, o)

		case gatewayOption, options.NameOption:
			if err := o.Apply(&gc); err != nil {
				ee = append(ee,
					errs.New(
						errs.M("gateway option failed"),
						errs.C(errorClass, errs.BulidingFailed),
						errs.E(err)))
			}

		default:
			ee = append(ee,
				errs.New(
					errs.M("invalid gateway option"),
					errs.C(errorClass, errs.TypeCastingError),
					errs.D("option_type", reflect.TypeOf(o).String())))
		}
	}

	if len(ee) != 0 {
		return nil, errors.Join(ee...)
	}

	return gc.newGateway()
}

// Direction returns the gateway's direction.
func (g *Gateway) Direction() GDirection {
	return g.direction
}

// ------------------ flow.Node interface --------------------------------------

func (g *Gateway) Node() flow.Node {
	return g
}

// NodeType returns Activity's node type.
func (g *Gateway) NodeType() flow.NodeType {
	return flow.GatewayNodeType
}

// --------------------- flow.SequenceTarget interface ------------------------

// AcceptIncomingFlow checks if it possible to use sf as IncomingFlow for the
// Activity.
func (g *Gateway) AcceptIncomingFlow(sf *flow.SequenceFlow) error {
	// Gateway has no restrictions on incoming floes
	return nil
}

// ------------------ flow.SequenceSource interface ----------------------------

// SuportOutgoingFlow checks if it possible to source sf SequenceFlow from
// the Gateway.
func (g *Gateway) SupportOutgoingFlow(sf *flow.SequenceFlow) error {
	// Gateway has no restrictions on outgoing flows
	return nil
}

// -----------------------------------------------------------------------------
