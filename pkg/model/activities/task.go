package activities

import (
	"reflect"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

type Task struct {
	Activity

	multyInstance bool
}

type multyInstance bool

func (mi *multyInstance) Validate() error {
	return nil
}

type taskOption func(cfg *multyInstance) error

func (to taskOption) Apply(cfg options.Configurator) error {
	if mi, ok := cfg.(*multyInstance); ok {
		return to(mi)
	}

	return &errs.ApplicationError{
		Message: "not a multyInstance task config",
		Classes: []string{
			errorClass,
			errs.TypeCastingError},
		Details: map[string]string{
			"cfg_type": reflect.TypeOf(cfg).Name()},
	}
}

// NewTask creates a new Task and returns its pointer on success or
// error on failure.
func NewTask(
	name string,
	taskOpts ...options.Option,
) (*Task, error) {
	var (
		actOpts = make([]options.Option, 0, len(taskOpts))
		mInst   = multyInstance(false)
	)

	for _, to := range taskOpts {
		switch o := to.(type) {
		case taskOption:
			err := o.Apply(&mInst)
			if err != nil {
				return nil,
					&errs.ApplicationError{
						Err:     err,
						Message: "couldn't create a Task",
						Classes: []string{
							errorClass,
							errs.BulidingFailed}}
			}
		default:
			actOpts = append(actOpts, to)
		}
	}

	a, err := NewActivity(name, actOpts...)
	if err != nil {
		return nil, err
	}

	return &Task{
			Activity:      *a,
			multyInstance: bool(mInst)},
		err
}

func WithMultyInstance() options.Option {
	f := func(cfg *multyInstance) error {
		*cfg = multyInstance(true)

		return nil
	}

	return taskOption(f)
}
