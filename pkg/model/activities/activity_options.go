package activities

import (
	"reflect"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

type (
	activityConfig struct {
	}

	activityOption func(cfg *activityConfig) error
)

func (ao activityOption) Apply(cfg options.Configurator) error {
	if ac, ok := cfg.(*activityConfig); ok {
		return ao(ac)
	}

	return &errs.ApplicationError{
		Message: "cfg isn't an activityConfig",
		Classes: []string{
			errorClass,
			errs.InvalidParameter},
		Details: map[string]string{
			"cfg_type": reflect.TypeOf(cfg).Name()},
	}
}

func (ac activityConfig) Validate() error {
	return nil
}
