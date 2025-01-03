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

// Sequence Flow Considerations
//
// This sub clause applies to all Gateways. Additional Sequence Flow Connection
// rules are specified for each type of Gateway in the sub clauses below.
//
//	A Gateway MAY be a target for a Sequence Flow. It can have zero (0),
//	one (1), or more incoming Sequence Flows.
//	If the Gateway does not have an incoming Sequence Flow, and there is no
//	Start Event for the Process, then the Gateway’s divergence behavior,
//	depending on the type of Gateway (see below), SHALL be performed when
//	the Process is instantiated.
//	NOTE: This rule isn't supported by goBpm and it's prohibited to have an
//	gateways without incoming sequence flow.
//
//	A Gateway MAY be a source of a Sequence Flow; it can have zero, one, or
//	more outgoing Sequence Flows.
//	A Gateway MUST have either multiple incoming Sequence Flows or multiple
//	outgoing Sequence Flows (i.e., it MUST merge or split the flow).
//	  - A Gateway with a gatewayDirection of unspecified MAY have both multiple
//	    incoming and outgoing Sequence Flows.
//	  - A Gateway with a gatewayDirection of mixed MUST have both multiple
//	    incoming and outgoing Sequence Flows.
//	  - A Gateway with a gatewayDirection of converging MUST have multiple
//	    incoming Sequence Flows, but MUST NOT have multiple outgoing Sequence
//	    Flows.
//	  - A Gateway with a gatewayDirection of diverging MUST have multiple
//	    outgoing Sequence Flows, but MUST NOT have multiple incoming Sequence
//	    Flows.
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

// Gateways are used to control how Sequence Flows interact as they converge
// and diverge within a Process. If the flow does not need to be controlled,
// then a Gateway is not needed. The term “Gateway” implies that there is a
// gating mechanism that either allows or disallows passage through the Gateway.
// As tokens arrive at a Gateway they can be merged together on input and/or
// split apart on output as the Gateway mechanisms are invoked.
//
// Gateways, like Activities, are capable of consuming or generating additional
// tokens, effectively controlling the execution semantics of a given Process.
// The main difference is that Gateways do not represent ‘work’ being done and
// they are considered to have zero effect on the operational measures of the
// Process being executed (cost, time, etc.).
// Gateways can define all the types of Business Process Sequence Flow behavior:
// Decisions/branching (exclusive, inclusive, and complex), merging, forking,
// and joining. Thus, while the diamond has been used traditionally for
// exclusive decisions, BPMN extends the behavior of the diamonds to reflect any
// type of Sequence Flow control. Each type of Gateway will have an internal
// indicator or marker to show the type of Gateway that is being used.
//
// The Gateway controls the flow of both diverging and converging Sequence
// Flows. That is, a single Gateway could have multiple input and multiple
// output flows. Modelers and modeling tools might want to enforce a best
// practice of a Gateway only performing one of these functions. Thus, it would
// take two sequential Gateways to first converge and then to diverge the
// Sequence Flows.
type Gateway struct {
	flow.FlowNode

	direction GDirection

	defaultFlow *flow.SequenceFlow
}

// New creates a new Gateway with options opts.
//
// Available options are:
//   - foundation.WithId
//   - foundation.WithDoc
//   - options.WithName
//   - gateways.WithDirection
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

// DefaultFlow return default flow of the gateway.
func (g *Gateway) DefaultFlow() *flow.SequenceFlow {
	return g.defaultFlow
}

// UpdateDefaultFlow updates the Gateway's default flow.
// if f is nil, then defaultFlow also sets to nil.
// According to standard, default flow SHOULD NOT have
// condiiion.
func (g *Gateway) UpdateDefaultFlow(f *flow.SequenceFlow) error {
	if f == nil {
		g.defaultFlow = nil

		return nil
	}

	for _, sf := range g.Outgoing() {
		if f.Id() == sf.Id() {
			if sf.Condition() != nil {
				return errs.New(
					errs.M("default flow shouldn't have a condition expression"),
					errs.C(errorClass, errs.InvalidObject))
			}

			g.defaultFlow = f

			return nil
		}
	}

	return errs.New(
		errs.M("there is no outgoing flow #%s", f.Id()),
		errs.C(errorClass, errs.ObjectNotFound))
}

// Direction returns the gateway's direction.
func (g *Gateway) Direction() GDirection {
	return g.direction
}

// TestFlows check if flows is comply gateway's direction rules.
// If everything is ok it returns error.
func (g *Gateway) TestFlows() error {
	errM := ""

	switch g.direction {
	case Unspecified:
		if len(g.Incoming()) < 1 || len(g.Outgoing()) < 1 {
			errM = "unspecified gateway should have at least one incoming and one outgoing flows"
		}

	case Mixed:
		if len(g.Outgoing()) < 2 ||
			len(g.Incoming()) < 2 {
			errM = "mixed gateway MUST have multiple incoming and outgouing flows"
		}

	case Converging:
		if (len(g.Outgoing()) > 1 || len(g.Outgoing()) == 0) ||
			len(g.Incoming()) < 2 {
			errM = "converging gateway MUST have multiple incoming and not have " +
				"multiple outgouing flows"
		}

	case Diverging:
		if len(g.Outgoing()) < 2 ||
			(len(g.Incoming()) > 1 || len(g.Incoming()) == 0) {
			errM = "diverging gateway MUST have multiple outgoing and not have " +
				"multiple incoming flows"
		}
	}

	if errM != "" {
		return errs.New(
			errs.M(errM),
			errs.C(errorClass, errs.InvalidObject),
			errs.D("incoming_count", len(g.Incoming())),
			errs.D("outgoing_count", len(g.Outgoing())))
	}

	return nil
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
	if g.direction == Diverging && len(g.Incoming()) > 0 {
		return errs.New(
			errs.M("diverging gateway MUST NOT have multiple incoming flows"),
			errs.C(errorClass, errs.BulidingFailed))
	}

	return nil
}

// ------------------ flow.SequenceSource interface ----------------------------

// SuportOutgoingFlow checks if it possible to source sf SequenceFlow from
// the Gateway.
func (g *Gateway) SupportOutgoingFlow(sf *flow.SequenceFlow) error {
	if g.direction == Converging && len(g.Outgoing()) > 0 {
		return errs.New(
			errs.M("converging gateway MUST NOT have multiple outgoing flows"),
			errs.C(errorClass, errs.BulidingFailed))
	}

	return nil
}

// -----------------------------------------------------------------------------

// interfaces check
var (
	_ flow.Node = (*Gateway)(nil)

	_ flow.SequenceSource = (*Gateway)(nil)
	_ flow.SequenceTarget = (*Gateway)(nil)
)
