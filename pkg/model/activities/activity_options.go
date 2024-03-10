package activities

import (
	"reflect"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

type (
	activityConfig struct {
		name           string
		compensation   bool
		loop           *LoopCharacteristics
		resources      []*ResourceRole
		props          []*data.Property
		startQ, complQ int
		baseOpts       []options.Option
	}

	activityOption func(cfg *activityConfig) error
)

// Apply implements options.Option interface for the activityOption.
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

// Validate implements options.Configurator interface for the activityConfig.
func (ac *activityConfig) Validate() error {
	ac.name = trim(ac.name)
	if err := checkStr(
		ac.name,
		"Activity name couldn't be empty"); err != nil {
		return err
	}

	return nil
}

// newActivity creates a new Activity from the activityConfig.
func (ac *activityConfig) newActivity() (*Activity, error) {
	if err := ac.Validate(); err != nil {
		return nil, err
	}

	n, err := flow.NewNode(ac.name, ac.baseOpts...)
	if err != nil {
		return nil,
			&errs.ApplicationError{
				Err:     err,
				Message: "couldn't create a FlowNode for the Activity",
				Classes: []string{
					errorClass,
					errs.BulidingFailed}}
	}

	a := Activity{
		Node:                   *n,
		isForCompensation:      ac.compensation,
		resources:              loadPSlice(ac.resources),
		properties:             loadPSlice(ac.props),
		startQuantity:          ac.startQ,
		completionQuantity:     ac.complQ,
		boundaryEvents:         []flow.Event{},
		dataInputAssociations:  []*data.Association{},
		dataOutputAssociations: []*data.Association{},
	}

	if ac.loop != nil {
		a.loopCharacteristics = *ac.loop
	}

	return &a, nil
}

// loadPSlice creates a slice of objects from slice of object pointers.
func loadPSlice[T any](src []*T) []T {
	dest := make([]T, 0, len(src))

	for _, e := range src {
		if e != nil {
			dest = append(dest, *e)
		}
	}

	return dest
}

// WithCompensation sets isForCompensation Activity flag to true.
func WithCompensation() activityOption {
	f := func(cfg *activityConfig) error {
		cfg.compensation = true

		return nil
	}

	return activityOption(f)
}

// WithLoop adds loop characteristics to the Activity.
func WithLoop(lc *LoopCharacteristics) activityOption {
	f := func(cfg *activityConfig) error {
		if lc != nil {
			cfg.loop = lc
		}

		return nil
	}

	return activityOption(f)
}

// WithProperties adds properties to the activityConfig.
func WithProperties(props ...*data.Property) activityOption {
	f := func(cfg *activityConfig) error {
		for _, p := range props {
			if p != nil {
				found := false
				for _, cp := range cfg.props {
					if cp.Id() == p.Id() {
						found = true

						break
					}
				}

				if !found {
					cfg.props = append(cfg.props, p)
				}
			}
		}

		return nil
	}

	return activityOption(f)
}

// WithResources adds unique non-nil resources into the activityConfig.
func WithResources(ress ...*ResourceRole) activityOption {
	f := func(cfg *activityConfig) error {
		for _, r := range ress {
			if r != nil {
				found := false
				for _, cr := range cfg.resources {
					if cr.Id() == r.Id() {
						found = true
						break
					}
				}

				if !found {
					cfg.resources = append(cfg.resources, r)
				}
			}
		}

		return nil
	}

	return activityOption(f)
}

// WithStartQuantity sets start quantity token number for the acitvity.
func WithStartQuantity(qty int) activityOption {
	f := func(cfg *activityConfig) error {
		if qty > 0 {
			cfg.startQ = qty
		}

		return nil
	}

	return activityOption(f)
}

// WithCompletionQuantity sets Activity completion token number quantity.
func WithCompletionQuantity(qty int) activityOption {
	f := func(cfg *activityConfig) error {
		if qty > 0 {
			cfg.complQ = qty
		}

		return nil
	}

	return activityOption(f)
}
