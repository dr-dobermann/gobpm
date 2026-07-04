// Package options provides configuration options for BPMN model elements.
package options

import (
	"strings"
)

// NameConfigurator interface extends Configurator to support setting names.
type NameConfigurator interface {
	Configurator

	SetName(string) error
}

// NameOption is a function type for setting name configuration options.
type NameOption func(cfg NameConfigurator) error

// Option marks NameOption as an Option; the dispatching constructor applies it
// by calling the func with a config that implements NameConfigurator.
func (NameOption) Option() {}

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
