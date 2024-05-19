package activities

import (
	"reflect"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// Task is common parent of all Tasks.
type Task struct {
	Activity

	multyInstance bool
}

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
				return nil, err
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

// -----------------------------------------------------------------------------

// WithMultyInstance sets multyinstance flag of the Task.
func WithMultyInstance() options.Option {
	f := func(cfg *multyInstance) error {
		*cfg = multyInstance(true)

		return nil
	}

	return taskOption(f)
}

// IsMultyinstance returns Task multyinstance settings.
func (t *Task) IsMultyinstance() bool {
	return t.multyInstance
}

// --------------------- flow.ActivityNode interface ---------------------------

func (t *Task) ActivityType() flow.ActivityType {
	return flow.TaskActivity
}
