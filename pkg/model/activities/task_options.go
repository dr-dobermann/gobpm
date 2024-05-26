package activities

import (
	"reflect"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// multyInstance is a configurator for multyInsatance flag of the Task.
type multyInstance bool

// Validate implements options.Configurator interface for multyInstance.
func (mi *multyInstance) Validate() error {
	return nil
}

// taskOption is task Task option configurator.
type taskOption func(cfg *multyInstance) error

// Apply implements options.Option interface for the taskOption.
func (to taskOption) Apply(cfg options.Configurator) error {
	if mi, ok := cfg.(*multyInstance); ok {
		return to(mi)
	}

	return errs.New(
		errs.M("not a multyInstance task config: %s",
			reflect.TypeOf(cfg).String()),
		errs.C(errorClass, errs.TypeCastingError))
}

// WithMultyInstance sets multyinstance flag of the Task.
func WithMultyInstance() options.Option {
	f := func(cfg *multyInstance) error {
		*cfg = multyInstance(true)

		return nil
	}

	return taskOption(f)
}
