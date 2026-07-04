package activities

import (
	"errors"

	hi "github.com/dr-dobermann/gobpm/pkg/model/hinteraction"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// RoleConfigurator is the interface for objects supported role access control.
type RoleConfigurator interface {
	options.Configurator

	AddRole(r *hi.ResourceRole) error
}

// RoleOption is a function type for configuring role-based access control.
type RoleOption func(cfg RoleConfigurator) error

// Option marks RoleOption as an options.Option; the dispatching constructor
// applies it by calling the func with a config that implements RoleConfigurator.
func (RoleOption) Option() {}

// WithRoles adds unique non-nil resources into the activityConfig.
func WithRoles(ress ...*hi.ResourceRole) RoleOption {
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
