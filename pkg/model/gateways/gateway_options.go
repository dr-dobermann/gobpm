package gateways

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

type (
	gatewayConfig struct {
		name      string
		direction GDirection
		baseOpts  []options.Option
	}

	// GatewayOption is a function type for configuring gateways.
	GatewayOption func(cft *gatewayConfig) error
)

// newGateway creates a new Gateway from the gatewayConfig.
func (gc *gatewayConfig) newGateway() (*Gateway, error) {
	if err := gc.Validate(); err != nil {
		return nil, err
	}

	fn, err := flow.NewBaseNode(gc.name, gc.baseOpts...)
	if err != nil {
		return nil, err
	}

	g := Gateway{
		BaseNode:  *fn,
		direction: gc.direction,
	}

	return &g, nil
}

// WithDirection implement gateway direction updating option.
func WithDirection(dir GDirection) GatewayOption {
	f := func(cfg *gatewayConfig) error {
		if err := dir.Validate(); err != nil {
			return err
		}

		cfg.direction = dir

		return nil
	}

	return GatewayOption(f)
}

// --------------------- option.Option interface ------------------------------

// Option marks GatewayOption as an options.Option; New applies it by calling
// the func directly after its type-switch matches.
func (GatewayOption) Option() {}

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
