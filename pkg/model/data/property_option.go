package data

import (
	"errors"
	"reflect"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// PropertyConfigurator is the interface for objects with Properties.
type PropertyConfigurator interface {
	options.Configurator

	AddProperty(p *Property) error
}

type PropertyOption func(cfg PropertyConfigurator) error

// --------------------- options.Option interface ------------------------------
//
// Apply converts roleOption into options.Option.
func (po PropertyOption) Apply(cfg options.Configurator) error {
	if pc, ok := cfg.(PropertyConfigurator); ok {
		return po(pc)
	}

	return errs.New(
		errs.M("config doesn't suppurt PropertyConfigurator"),
		errs.C(errorClass, errs.TypeCastingError),
		errs.D("config_type", reflect.TypeOf(cfg).String()))
}

// WithProperties adds properties to the activityConfig.
// Duplicat properties (by Id) are ignored.
func WithProperties(props ...*Property) PropertyOption {
	f := func(cfg PropertyConfigurator) error {
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
