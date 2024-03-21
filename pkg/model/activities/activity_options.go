package activities

import (
	"reflect"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

type Set struct {
	Set *data.Set
	Dir data.Direction

	// Type allows combination of SetType
	Type   data.SetType
	Params []*data.Parameter
}

type (
	activityConfig struct {
		name             string
		compensation     bool
		loop             *LoopCharacteristics
		resources        []*ResourceRole
		props            []*data.Property
		startQ, complQ   int
		baseOpts         []options.Option
		dataAssociations map[data.Direction][]*data.Association
		parameters       map[data.Direction][]*data.Parameter
		sets             map[data.Direction][]*Set
		withoutParms     bool
	}

	activityOption func(cfg *activityConfig) error
)

// Apply implements options.Option interface for the activityOption.
func (ao activityOption) Apply(cfg options.Configurator) error {
	if ac, ok := cfg.(*activityConfig); ok {
		return ao(ac)
	}

	return errs.New(
		errs.M("cfg isn't an activityConfig"),
		errs.C(errorClass, errs.InvalidParameter, errs.TypeCastingError),
		errs.D("cfg_type", reflect.TypeOf(cfg).String()))
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
		return nil, err
	}

	ioSpecs, err := createIOSpecs(ac)
	if err != nil {
		return nil, err
	}

	if err := ioSpecs.Validate(); err != nil {
		return nil, err
	}

	a := Activity{
		Node:               *n,
		isForCompensation:  ac.compensation,
		resources:          loadPSlice(ac.resources),
		properties:         loadPSlice(ac.props),
		startQuantity:      ac.startQ,
		completionQuantity: ac.complQ,
		boundaryEvents:     []flow.Event{},
		dataAssociations:   ac.dataAssociations,
		IoSpec:             ioSpecs,
	}

	if ac.loop != nil {
		a.loopCharacteristics = *ac.loop
	}

	return &a, nil
}

// createIOSpecs creates a new InputOutputSpecification and returns its
// pointer on success or error on failure.
// if activityConfig has withoutParams flag set, then all Parameters and
// Sets are ignored and IOSpec creates with defalu_input and default_output
// empty sets.
//
//nolint:gocognit
func createIOSpecs(ac *activityConfig) (*data.InputOutputSpecification, error) {
	ioSpecs, err := data.NewIOSpec()
	if err != nil {
		return nil, err
	}

	if ac.withoutParms {
		for _, d := range []data.Direction{data.Input, data.Output} {
			s, err := data.NewSet("default_" + strings.ToLower(string(d)))
			if err != nil {
				return nil, err
			}

			if err := ioSpecs.AddSet(s, d); err != nil {
				return nil, err
			}
		}

		return ioSpecs, nil
	}

	// add parameters
	for d, pp := range ac.parameters {
		for _, p := range pp {
			if err := ioSpecs.AddParameter(p, d); err != nil {
				return nil, err
			}
		}
	}

	// through all sets
	for d, ss := range ac.sets {
		for _, s := range ss {
			for _, p := range s.Params {
				if !ioSpecs.HasParameter(p, d) {
					return nil,
						errs.New(
							errs.M("there is no %s parameter %q",
								d, p.Name()),
							errs.C(errorClass, errs.InvalidParameter))
				}

				if err := s.Set.AddParameter(p, s.Type); err != nil {
					return nil, err
				}
			}

			// add set to IOSpecs
			if err := ioSpecs.AddSet(s.Set, d); err != nil {
				return nil, err
			}
		}
	}

	return ioSpecs, nil
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
		if lc == nil {
			return errs.New(
				errs.M("loop definition couldn't be empty"),
				errs.C(errorClass, errs.InvalidParameter, errs.EmptyNotAllowed))
		}

		cfg.loop = lc

		return nil
	}

	return activityOption(f)
}

// WithProperties adds properties to the activityConfig.
// Duplicat properties (by Id) are ignored.
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
func WithRoles(ress ...*ResourceRole) activityOption {
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

// WithParameters adds non-nil unique parameters to the Activity.
func WithParameter(p *data.Parameter, d data.Direction) activityOption {
	f := func(cfg *activityConfig) error {
		if p == nil {
			return nil
		}

		if err := d.Validate(); err != nil {
			return errs.New(
				errs.E(err),
				errs.M("parameter %q has invalid type (%q)",
					p.Name(), d),
				errs.C(errorClass))
		}

		params, ok := cfg.parameters[d]
		if !ok {
			cfg.parameters[d] = []*data.Parameter{p}

			return nil
		}

		// check for duplication
		found := false
		for _, cp := range params {
			if cp.Id() == p.Id() {
				found = true
				break
			}
		}

		if !found {
			cfg.parameters[d] = append(params, p)
		}

		return nil
	}

	return activityOption(f)
}

// WithSets adds non-empty unique Set into the Activity config.
func WithSet(
	s *data.Set,
	d data.Direction,
	st data.SetType,
	params []*data.Parameter,
) activityOption {
	f := func(cfg *activityConfig) error {
		if s == nil {
			return nil
		}

		if err := d.Validate(); err != nil {
			return errs.New(
				errs.M("invalid direction %q for data.Set %q",
					d, s.Name()),
				errs.C(errorClass, errs.InvalidParameter),
				errs.E(err))
		}

		if err := st.Validate(data.CombinedTypes); err != nil {
			return errs.New(
				errs.M("invalid set type %d for data.Set",
					st, s.Name()),
				errs.C(errorClass, errs.InvalidParameter),
				errs.E(err))
		}

		// check for duplication
		tss, ok := cfg.sets[d]
		if !ok {
			cfg.sets[d] = []*Set{
				{
					Set:    s,
					Dir:    d,
					Type:   st,
					Params: convertNilSlice(params),
				}}

			return nil
		}

		for _, ts := range tss {
			if ts.Set.Id() == s.Id() {
				return nil
			}
		}

		cfg.sets[d] = append(tss,
			&Set{
				Set:    s,
				Dir:    d,
				Type:   st,
				Params: convertNilSlice(params),
			})

		return nil
	}

	return activityOption(f)
}

// WithoutParams indicates that the Activity has no parameters and
// ignores all Parameters and Sets options.
// It creates an empty input and output data.Sets in IOSpec with no
// parameters.
func WithoutParams() activityOption {
	f := func(cfg *activityConfig) error {
		cfg.withoutParms = true

		return nil
	}

	return activityOption(f)
}
