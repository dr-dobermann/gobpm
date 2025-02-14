package activities

import (
	"reflect"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	hi "github.com/dr-dobermann/gobpm/pkg/model/hinteraction"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// setDef defines single set for activity InputOutputSpecifictation.
type setDef struct {
	set *data.Set
	dir data.Direction

	// setType allows combination of SetType
	setType data.SetType
	params  []*data.Parameter
}

// newSetDef tries to create sn IoSpec set definition and return its pointer or
// error on failure.
func newSetDef(name, id string,
	d data.Direction,
	st data.SetType,
	params []*data.Parameter,
) (*setDef, error) {
	name = strings.TrimSpace(name)
	id = strings.TrimSpace(id)
	var (
		s   *data.Set
		err error
	)

	if id == "" {
		s, err = data.NewSet(name)
	} else {
		s, err = data.NewSet(name, foundation.WithId(id))
	}
	if err != nil {
		return nil, errs.New(
			errs.M("couldn't create new set"),
			errs.E(err),
			errs.D("set_name", name),
			errs.D("set_id", id))
	}

	return &setDef{
		set:     s,
		dir:     d,
		setType: st,
		params:  append([]*data.Parameter{}, params...),
	}, nil
}

type (
	activityConfig struct {
		name             string
		compensation     bool
		loop             *LoopCharacteristics
		roles            map[string]*hi.ResourceRole
		props            map[string]*data.Property
		startQ, complQ   int
		baseOpts         []options.Option
		dataAssociations map[data.Direction][]*data.Association
		sets             map[data.Direction][]*setDef
		withoutParams    bool
		defaultFlow      *flow.SequenceFlow
	}

	activityOption func(cfg *activityConfig) error
)

// newActivity creates a new Activity from the activityConfig.
func (ac *activityConfig) newActivity() (*activity, error) {
	if err := ac.Validate(); err != nil {
		return nil, err
	}

	fn, err := flow.NewFlowNode(ac.name, ac.baseOpts...)
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

	a := activity{
		FlowNode:            *fn,
		isForCompensation:   ac.compensation,
		roles:               ac.roles,
		properties:          ac.props,
		startQuantity:       ac.startQ,
		completionQuantity:  ac.complQ,
		IoSpec:              ioSpecs,
		dataAssociations:    ac.dataAssociations,
		defaultFlow:         ac.defaultFlow,
		loopCharacteristics: ac.loop,
	}

	return &a, nil
}

// createIOSpecs creates a new InputOutputSpecification and returns its
// pointer on success or error on failure.
// if activityConfig has withoutParams flag set, then all Parameters and
// Sets are ignored and IOSpec creates with default_input and default_output
// empty sets.
func createIOSpecs(ac *activityConfig) (*data.InputOutputSpecification, error) {
	ioSpecs, err := data.NewIOSpec()
	if err != nil {
		return nil, err
	}

	if ac.withoutParams {
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

	for d, ss := range ac.sets {
		for _, s := range ss {
			if err := addSetParams(ioSpecs, d, s); err != nil {
				return nil, err
			}

			// add set to IOSpecs
			if err := ioSpecs.AddSet(s.set, d); err != nil {
				return nil, err
			}
		}
	}

	return ioSpecs, nil
}

// addSetParams adds params to the data.Set if parameters aren't existed in
// ioSpecs, it add them to the ioSpecs too.
func addSetParams(
	ioSpecs *data.InputOutputSpecification,
	d data.Direction,
	s *setDef,
) error {
	for _, p := range s.params {
		if !ioSpecs.HasParameter(p, d) {
			if err := ioSpecs.AddParameter(p, d); err != nil {
				return errs.New(
					errs.M("couldn't add set's parameter"),
					errs.E(err),
					errs.D("set_name", s.set.Name()),
					errs.D("param_name", p.Name()),
					errs.D("param_direction", string(d)))
			}
		}

		if err := s.set.AddParameter(p, s.setType); err != nil {
			return errs.New(
				errs.M("couldn't add parameter to set"),
				errs.E(err),
				errs.D("set_name", s.set.Name()),
				errs.D("param_name", p.Name()),
				errs.D("param_direction", string(d)))
		}
	}

	return nil
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

// WithEmptySets adds empty unique Set into the Activity config.
// WithEmptySet called since BPMN standard demands non-empty input and
// output set for the activity.
//
// It creates an default set on given direction. If there is already exists
// any set for the direction, error returned.
//
// Parameters:
//   - name -- data set name
//   - id -- data set id. If empty, will be generated
//   - d -- data set direction.
func WithEmptySet(
	name, id string,
	d data.Direction,
) activityOption {
	f := func(cfg *activityConfig) error {
		if err := d.Validate(); err != nil {
			return errs.New(
				errs.M("invalid direction %q for data.Set %q",
					d, name),
				errs.C(errorClass, errs.InvalidParameter),
				errs.E(err))
		}

		sd, err := newSetDef(name, id, d, data.DefaultSet, []*data.Parameter{})
		if err != nil {
			return err
		}

		// check for duplication
		tss, ok := cfg.sets[d]
		if ok && len(tss) != 0 {
			return errs.New(
				errs.M("couldn't add empty set to non-empty ones"),
				errs.C(errorClass, errs.InvalidParameter),
				errs.D("set_name", name),
				errs.D("set_direction", d),
				errs.E(err))
		}

		cfg.sets[d] = []*setDef{sd}

		return nil
	}

	return activityOption(f)
}

// WithSets adds non-empty unique Set into the Activity config.
//
// Parameters:
//   - name -- data set name
//   - id -- data set id. If empty, will be generated
//   - st -- data set type from data.SetType
//   - params -- list of data set parameters
func WithSet(
	name, id string,
	d data.Direction,
	st data.SetType,
	params []*data.Parameter,
) activityOption {
	f := func(cfg *activityConfig) error {
		if err := d.Validate(); err != nil {
			return errs.New(
				errs.M("invalid direction for data.Set"),
				errs.C(errorClass, errs.InvalidParameter),
				errs.D("set_name", name),
				errs.D("set_direction", d),
				errs.E(err))
		}

		if err := st.Validate(data.CombinedTypes); err != nil {
			return errs.New(
				errs.M("invalid set type for data.Set"),
				errs.C(errorClass, errs.InvalidParameter),
				errs.D("set_name", name),
				errs.D("set_type", st),
				errs.E(err))
		}

		sd, err := newSetDef(name, id, d, st, params)
		if err != nil {
			return err
		}

		// check for duplication
		tss, ok := cfg.sets[d]
		if !ok {
			cfg.sets[d] = []*setDef{sd}

			return nil
		}

		for _, ts := range tss {
			if ts.set.Id() == sd.set.Id() {
				return nil
			}
		}

		cfg.sets[d] = append(tss, sd)

		return nil
	}

	return activityOption(f)
}

// WithoutParams indicates that the Activity has neither incoming
// nor outgoing parameters and ignores all Parameters and Sets options.
// It creates an empty input and output data.Sets in IOSpec with no
// parameters.
func WithoutParams() activityOption {
	f := func(cfg *activityConfig) error {
		cfg.withoutParams = true

		return nil
	}

	return activityOption(f)
}

// --------------------- options.Option interface ------------------------------

// Apply converts activityOption into options.Option.
func (ao activityOption) Apply(cfg options.Configurator) error {
	if ac, ok := cfg.(*activityConfig); ok {
		return ao(ac)
	}

	return errs.New(
		errs.M("cfg isn't an activityConfig"),
		errs.C(errorClass, errs.InvalidParameter, errs.TypeCastingError),
		errs.D("cfg_type", reflect.TypeOf(cfg).String()))
}

// ------------------ options.Configurator interface ---------------------------

// Validate validates activityConfig fields.
func (ac *activityConfig) Validate() error {
	if err := errs.CheckStr(
		ac.name,
		"Activity name couldn't be empty",
		errorClass,
	); err != nil {
		return err
	}

	return nil
}

// ------------------- RoleConfigurator interface ------------------------------

// AddRole adds single non-empty unique ResourceRole into activityConfig.
// if activityConfig already has the ResourceRole with the same name,
// it will be overwritten.
func (ac *activityConfig) AddRole(r *hi.ResourceRole) error {
	if r == nil {
		return errs.New(
			errs.M("role couldn't be empty"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	ac.roles[r.Name()] = r

	return nil
}

// --------------- data.PropertyConfigurator interface -------------------------

// AddProperty adds non-empyt property into the activityConfig.
// if the activityConfig already has the property with the same name it
// will be overwritten.
func (ac *activityConfig) AddProperty(p *data.Property) error {
	if p == nil {
		return errs.New(
			errs.M("property couldn't be empty"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	ac.props[p.Name()] = p

	return nil
}
