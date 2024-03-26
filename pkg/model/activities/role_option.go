package activities

import (
	"errors"
	"reflect"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// RoleConfigurator is the interface for objects supported role access control.
type RoleConfigurator interface {
	options.Configurator

	AddRole(r *ResourceRole) error
}

type RoleOption func(cfg RoleConfigurator) error

// --------------------- options.Option interface ------------------------------
//
// Apply converts roleOption into options.Option.
func (ro RoleOption) Apply(cfg options.Configurator) error {
	if rc, ok := cfg.(RoleConfigurator); ok {
		return ro(rc)
	}

	return errs.New(
		errs.M("config doesn't suppurt RoleConfigurator"),
		errs.C(errorClass, errs.TypeCastingError),
		errs.D("config_type", reflect.TypeOf(cfg).String()))
}

// WithResources adds unique non-nil resources into the activityConfig.
func WithRoles(ress ...*ResourceRole) RoleOption {
	f := func(cfg RoleConfigurator) error {
		ee := []error{}
		for _, r := range ress {
			if r != nil {
				if err := cfg.AddRole(r); err != nil {
					ee = append(ee, err)
				}
			}
		}

		if len(ee) != 0 {
			return errors.Join(ee...)
		}

		return nil
	}

	return RoleOption(f)
}
