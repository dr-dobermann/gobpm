package activities

import (
	"reflect"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	hi "github.com/dr-dobermann/gobpm/pkg/model/hinteraction"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

type (
	activityConfig struct {
		loop             *LoopCharacteristics
		roles            map[string]*hi.ResourceRole
		props            map[string]*data.Property
		dataAssociations map[data.Direction][]*data.Association
		params           map[data.Direction][]*data.Parameter
		defaultFlow      *flow.SequenceFlow
		name             string
		baseOpts         []options.Option
		startQ           int
		complQ           int
		compensation     bool
		withoutParams    bool
	}

	// ActivityOption represents an activity configuration option.
	ActivityOption func(cfg *activityConfig) error
)

// newActivity creates a new Activity from the activityConfig.
func (ac *activityConfig) newActivity() (*activity, error) {
	if err := ac.Validate(); err != nil {
		return nil, err
	}

	fn, err := flow.NewBaseNode(ac.name, ac.baseOpts...)
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
		BaseNode:            *fn,
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

// createIOSpecs creates a new InputOutputSpecification from the activity's
// declared parameters and returns its pointer on success or error on failure.
// With withoutParams set (or no parameters declared) it returns an IOSpec with
// empty input/output parameter lists — the activity needs and produces no data
// (ADR-011 v.2: the empty input/output set).
func createIOSpecs(ac *activityConfig) (*data.InputOutputSpecification, error) {
	ioSpecs, err := data.NewIOSpec()
	if err != nil {
		return nil, err
	}

	if ac.withoutParams {
		return ioSpecs, nil
	}

	for d, pp := range ac.params {
		for _, p := range pp {
			// AddParameter dedups by identity, so a parameter listed twice is
			// added once.
			if err := ioSpecs.AddParameter(p, d); err != nil {
				return nil, errs.New(
					errs.M("couldn't add parameter"),
					errs.E(err),
					errs.D("param_name", p.Name()),
					errs.D("param_direction", string(d)))
			}
		}
	}

	return ioSpecs, nil
}

// WithCompensation sets isForCompensation Activity flag to true.
func WithCompensation() ActivityOption {
	f := func(cfg *activityConfig) error {
		cfg.compensation = true

		return nil
	}

	return ActivityOption(f)
}

// WithLoop adds loop characteristics to the Activity.
func WithLoop(lc *LoopCharacteristics) ActivityOption {
	f := func(cfg *activityConfig) error {
		if lc == nil {
			return errs.New(
				errs.M("loop definition couldn't be empty"),
				errs.C(errorClass, errs.InvalidParameter, errs.EmptyNotAllowed))
		}

		cfg.loop = lc

		return nil
	}

	return ActivityOption(f)
}

// WithStartQuantity sets start quantity token number for the acitvity.
func WithStartQuantity(qty int) ActivityOption {
	f := func(cfg *activityConfig) error {
		if qty > 0 {
			cfg.startQ = qty
		}

		return nil
	}

	return ActivityOption(f)
}

// WithCompletionQuantity sets Activity completion token number quantity.
func WithCompletionQuantity(qty int) ActivityOption {
	f := func(cfg *activityConfig) error {
		if qty > 0 {
			cfg.complQ = qty
		}

		return nil
	}

	return ActivityOption(f)
}

// WithParameters declares the activity's input or output parameters for the
// given direction (ADR-011 v.2: the single input/output set is the parameter
// list; per-parameter optional/whileExecuting flags carry the role). Parameters
// already present (by id) for the direction are skipped. May be called more than
// once per direction; the lists accumulate.
//
// Parameters:
//   - d -- the parameters' direction
//   - params -- the parameters to add (each pre-flagged via data.Optional() /
//     data.WhileExecuting() at construction)
func WithParameters(
	d data.Direction,
	params ...*data.Parameter,
) ActivityOption {
	f := func(cfg *activityConfig) error {
		if err := d.Validate(); err != nil {
			return errs.New(
				errs.M("WithParameters: invalid direction %q", d),
				errs.C(errorClass, errs.InvalidParameter),
				errs.E(err))
		}

		for _, p := range params {
			if p == nil {
				return errs.New(
					errs.M("WithParameters: a nil parameter isn't allowed"),
					errs.C(errorClass, errs.EmptyNotAllowed,
						errs.InvalidParameter),
					errs.D("direction", string(d)))
			}
		}

		cfg.params[d] = append(cfg.params[d], params...)

		return nil
	}

	return ActivityOption(f)
}

// WithoutParams indicates that the Activity has neither incoming nor outgoing
// parameters and ignores any WithParameters options. It creates an IOSpec with
// empty input and output parameter lists.
func WithoutParams() ActivityOption {
	f := func(cfg *activityConfig) error {
		cfg.withoutParams = true

		return nil
	}

	return ActivityOption(f)
}

// --------------------- options.Option interface ------------------------------

// Apply converts activityOption into options.Option.
func (ao ActivityOption) Apply(cfg options.Configurator) error {
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
