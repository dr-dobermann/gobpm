// Package options provides configuration options for BPMN model elements.
package options

import (
	"reflect"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
)

// NameConfigurator interface extends Configurator to support setting names.
type NameConfigurator interface {
	Configurator

	SetName(string) error
}

// NameOption is a function type for setting name configuration options.
type NameOption func(cfg NameConfigurator) error

// Apply implements the Option interface for NameOption.
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

// WithName sets the name configuration option.
// Duplicat properties (by Id) are ignored.
func WithName(name string) NameOption {
	f := func(cfg NameConfigurator) error {
		name = strings.Trim(name, " ")

		return cfg.SetName(name)
	}

	return NameOption(f)
}
