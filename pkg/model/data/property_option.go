package data

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// PropertyAdder is the interface for objects with Properties.
type PropertyAdder interface {
	options.Configurator

	AddProperty(p *Property) error
}

type PropertyOption func(cfg PropertyAdder) error

// WithProperties adds properties to the activityConfig.
// Duplicat properties (by Id) are ignored.
func WithProperties(props ...*Property) PropertyOption {
	f := func(cfg PropertyAdder) error {
		ee := []error{}

		for _, p := range props {
			if p != nil {
				if err := cfg.AddProperty(p); err != nil {
					ee = append(ee, err)
				}
			}
		}

		if len(ee) != 0 {
			return errors.Join(ee...)
		}

		return nil
	}

	return PropertyOption(f)
}

// WithProperty builds and adds single property to the configuration.
func WithProperty(name string, iaeOpt iaeAdderOption) PropertyOption {
	name = strings.TrimSpace(name)

	f := func(cfg PropertyAdder) error {
		if name == "" {
			return fmt.Errorf("property should have a non-empty name")
		}

		if iaeOpt == nil {
			return fmt.Errorf("no IAE option")
		}

		p, err := NewProp(name, iaeOpt)
		if err != nil {
			return fmt.Errorf("property building failed: %w", err)
		}

		return cfg.AddProperty(p)
	}

	return PropertyOption(f)
}

// --------------------- options.Option interface ------------------------------
//
// Apply applies propertyOption on to PropertyAdder configuration.
func (po PropertyOption) Apply(cfg options.Configurator) error {
	if pc, ok := cfg.(PropertyAdder); ok {
		return po(pc)
	}

	return errs.New(
		errs.M("config doesn't suppurt PropertyConfigurator"),
		errs.C(errorClass, errs.TypeCastingError),
		errs.D("config_type", reflect.TypeOf(cfg).String()))
}

// -----------------------------------------------------------------------------
