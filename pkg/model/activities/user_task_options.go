package activities

import (
	"fmt"
	"reflect"
	"slices"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/common"
	hi "github.com/dr-dobermann/gobpm/pkg/model/hinteraction"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

type (
	usrTaskConfig struct {
		name      string
		renderers []hi.Renderer
		taskOpts  []options.Option
		outputs   []*common.ResourceParameter
	}

	usrTaskOption func(cfg *usrTaskConfig) error
)

// newUsrTask tries to create new UserTask from user task config.
func (utc *usrTaskConfig) newUsrTask() (*UserTask, error) {
	if err := utc.Validate(); err != nil {
		return nil, err
	}

	t, err := NewTask(utc.name, utc.taskOpts...)
	if err != nil {
		return nil,
			errs.New(
				errs.M("user task building failed"),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(err))
	}

	r, err := common.NewResource(utc.name, utc.outputs...)
	if err != nil {
		return nil,
			errs.New(
				errs.M("resource creation failed"),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(err))
	}

	ut := UserTask{
		Task:      *t,
		renderers: append([]hi.Renderer{}, utc.renderers...),
		outputs:   r,
	}

	return &ut, nil
}

// WithRenderer adds new unique Render to user task config.
func WithRenderer(r hi.Renderer) usrTaskOption {
	f := func(cfg *usrTaskConfig) error {
		if r == nil {
			return fmt.Errorf("no renderer")
		}

		if slices.ContainsFunc(
			cfg.renderers,
			func(r2c hi.Renderer) bool {
				return r2c.Id() == r.Id() ||
					r2c.Implementation() == r.Implementation()
			}) {
			return fmt.Errorf("duplicate renderer: #%s", r.Id())
		}

		cfg.renderers = append(cfg.renderers, r)

		return nil
	}

	return usrTaskOption(f)
}

// WithOutput register new output parameter from renderer.
func WithOutput(name, pType string, required bool) usrTaskOption {
	f := func(cfg *usrTaskConfig) error {
		if slices.ContainsFunc(
			cfg.outputs,
			func(p *common.ResourceParameter) bool {
				return p.Name() == name
			}) {
			return fmt.Errorf("duplicate parameter %q", name)
		}

		p, err := common.NewResourceParameter(name, pType, required)
		if err != nil {
			return fmt.Errorf("couldn't create a parameter: %w", err)
		}

		cfg.outputs = append(cfg.outputs, p)

		return nil
	}

	return usrTaskOption(f)
}

// --------------------- options.Option interface ------------------------------

func (uto usrTaskOption) Apply(cfg options.Configurator) error {
	if utc, ok := cfg.(*usrTaskConfig); ok {
		return uto(utc)
	}

	return errs.New(
		errs.M("isn't usrTaskConfig"),
		errs.C(errorClass, errs.TypeCastingError),
		errs.D("cfg_type", reflect.TypeOf(cfg).String()))
}

// ------------------ options.Configurator interface ---------------------------

// Validate validates activityConfig fields.
func (utc *usrTaskConfig) Validate() error {
	if err := errs.CheckStr(
		utc.name,
		"UserTask should have a name",
		errorClass,
	); err != nil {
		return err
	}

	return nil
}

// ----------------------------------------------------------------------------
