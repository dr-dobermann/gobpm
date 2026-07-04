package activities

import (
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

// Option marks taskOption as an options.Option; newTask applies it by calling
// the func directly after its type-switch matches.
func (taskOption) Option() {}

// WithMultyInstance sets multyinstance flag of the Task.
func WithMultyInstance() options.Option {
	f := func(cfg *multyInstance) error {
		*cfg = multyInstance(true)

		return nil
	}

	return taskOption(f)
}
