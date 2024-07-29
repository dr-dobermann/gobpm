package options

import (
	"reflect"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
)

type NameConfigurator interface {
	Configurator

	SetName(string) error
}

type NameOption func(cfg NameConfigurator) error

// ------------------ Option interface -----------------------------------------
func (no NameOption) Apply(cfg Configurator) error {
	if nc, ok := cfg.(NameConfigurator); ok {
		return no(nc)
	}

	return errs.New(
		errs.M("config doesn't implement NameConfigurator"),
		errs.C(errorClass, errs.TypeCastingError),
		errs.D("config_type", reflect.TypeOf(cfg).String()))
}

// -----------------------------------------------------------------------------

// WithProperties adds properties to the activityConfig.
// Duplicat properties (by Id) are ignored.
func WithName(name string) NameOption {
	f := func(cfg NameConfigurator) error {
		name = strings.Trim(name, " ")

		return cfg.SetName(name)
	}

	return NameOption(f)
}
