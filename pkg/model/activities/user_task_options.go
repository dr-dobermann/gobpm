package activities

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	hi "github.com/dr-dobermann/gobpm/pkg/model/hinteraction"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

type (
	usrTaskConfig struct {
		impl      string
		name      string
		renderers []hi.Renderer
		taskOpts  []options.Option
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

	ut := UserTask{
		Task:      *t,
		impl:      utc.impl,
		renderers: append([]hi.Renderer{}, utc.renderers...),
	}

	return &ut, nil
}

// WithImplementation changes implementation of UserTask in
// user task config.
func WithImplementation(impl string) usrTaskOption {
	f := func(cfg *usrTaskConfig) error {
		impl = strings.TrimSpace(impl)
		if impl != "" {
			cfg.impl = impl
		}

		return nil
	}

	return usrTaskOption(f)
}

// WithRenderer adds new unique Render to user task config.
func WithRenderer(r hi.Renderer) usrTaskOption {
	f := func(cfg *usrTaskConfig) error {
		if slices.ContainsFunc(
			cfg.renderers,
			func(r2c hi.Renderer) bool {
				return r2c.Id() == r.Id()
			}) {
			return fmt.Errorf("duplicate renderer: #%s", r.Id())
		}

		cfg.renderers = append(cfg.renderers, r)

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
