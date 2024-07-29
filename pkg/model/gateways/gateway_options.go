package gateways

import (
	"fmt"
	"reflect"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

type (
	gatewayConfig struct {
		name      string
		direction GDirection
		baseOpts  []options.Option
	}

	gatewayOption func(cft *gatewayConfig) error
)

// newGateway creates a new Gateway from the gatewayConfig.
func (gc *gatewayConfig) newGateway() (*Gateway, error) {
	if err := gc.Validate(); err != nil {
		return nil, err
	}

	be, err := foundation.NewBaseElement(gc.baseOpts...)
	if err != nil {
		return nil, err
	}

	g := Gateway{
		BaseElement: *be,
		FlowNode:    *flow.NewFlowNode(),
		FlowElement: *flow.NewFlowElement(gc.name),
		direction:   gc.direction,
		defaultFlow: &flow.SequenceFlow{},
	}

	return &g, nil
}

// WithDirection implement gateway direction updating option.
func WithDirection(dir GDirection) gatewayOption {
	f := func(cfg *gatewayConfig) error {
		if err := dir.Validate(); err != nil {
			return err
		}

		cfg.direction = dir

		return nil
	}

	return gatewayOption(f)
}

// --------------------- option.Option interface ------------------------------

// Apply updates the gateway configuration by gatewayOption.
func (gOpt gatewayOption) Apply(cfg options.Configurator) error {
	if gc, ok := cfg.(*gatewayConfig); ok {
		return gOpt(gc)
	}

	return errs.New(
		errs.M("cfg isn't an gatewayConfig"),
		errs.C(errorClass, errs.InvalidParameter, errs.TypeCastingError),
		errs.D("cfg_type", reflect.TypeOf(cfg).String()))
}

// -------------------- options.Configurator interface ------------------------

// Validate checks gateway configuration integrity.
func (gc *gatewayConfig) Validate() error {
	return errs.CheckStr(
		string(gc.direction),
		fmt.Sprintf("invalid gateway direction specification: %q", gc.direction),
		errorClass, errs.InvalidParameter)
}

// -------------------- options.NameConfigurator ------------------------------

// SetName sets gateway name either empty or not-empty.
func (gc *gatewayConfig) SetName(name string) error {
	gc.name = name

	return nil
}

// ----------------------------------------------------------------------------
