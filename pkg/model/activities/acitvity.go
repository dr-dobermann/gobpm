package activities

import (
	"errors"
	"reflect"

	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/helpers"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// The Activity class is the abstract super class for all concrete Activity
// types.
type Activity struct {
	foundation.BaseElement

	flow.FlowNode

	flow.FlowElement

	// A flag that identifies whether this Activity is intended for the
	// purposes of compensation.
	// If false, then this Activity executes as a result of normal execution
	// flow.
	// If true, this Activity is only activated when a Compensation Event is
	// detected and initiated under Compensation Event visibility scope
	isForCompensation bool

	// An Activity MAY be performed once or MAY be repeated. If repeated,
	// the Activity MUST have loopCharacteristics that define the repetition
	// criteria (if the isExecutable attribute of the Process is set to true).
	loopCharacteristics LoopCharacteristics

	// Defines the resource that will perform or will be responsible for the
	// Activity. The resource, e.g., a performer, can be specified in the form
	// of a specific individual, a group, an organization role or position, or
	// an organization.
	//
	// DEV_NOTE: roles indexed by role name.
	roles map[string]*ResourceRole

	// The Sequence Flow that will receive a token when none of the
	// conditionExpressions on other outgoing Sequence Flows evaluate to true.
	// The default Sequence Flow should not have a conditionExpression. Any
	// such Expression SHALL be ignored.
	defaultFlow *flow.SequenceFlow

	// Modeler-defined properties MAY be added to an Activity. These
	// properties are contained within the Activity.
	//
	// DEV_NOTE: properties indexed by property name.
	properties map[string]*data.Property

	// The default value is 1. The value MUST NOT be less than 1. This attribute
	// defines the number of tokens that MUST arrive before the Activity can
	// begin.
	// Note that any value for the attribute that is greater than 1 is an
	// advanced type of modeling and should be used with caution.
	startQuantity int

	// The default value is 1. The value MUST NOT be less than 1. This attribute
	// defines the number of tokens that MUST be generated from the Activity.
	// This number of tokens will be sent done any outgoing Sequence Flow
	// (assuming any Sequence Flow conditions are satisfied).
	// Note that any value for the attribute that is greater than 1 is an
	// advanced type of modeling and should be used with caution.
	completionQuantity int

	// The InputOutputSpecification defines the inputs and outputs and the
	// InputSets and OutputSets for the Activity.
	IoSpec *data.InputOutputSpecification

	// This references the Intermediate Events that are attached to the
	// boundary of the Activity.
	boundaryEvents []flow.EventNode

	// An optional reference to the DataInputAssociations.
	// A DataInputAssociation defines how the DataInput of the Activity’s
	// InputOutputSpecification will be populated.
	// dataInputAssociations []*data.Association

	// An optional reference to the DataOutputAssociations.
	// dataOutputAssociations []*data.Association

	// dataAssociations holds input and output DataAssociation of the Activity.
	dataAssociations map[data.Direction][]*data.Association

	// dataPath of the Activitiy.
	dataPath scope.DataPath
}

// NewActivity creates a new Activity with options and returns its pointer on
// success or errors on failure.
func NewActivity(
	name string,
	actOpts ...options.Option,
) (*Activity, error) {
	cfg := activityConfig{
		name:             helpers.Strim(name),
		roles:            map[string]*ResourceRole{},
		props:            map[string]*data.Property{},
		startQ:           1,
		complQ:           1,
		baseOpts:         []options.Option{},
		dataAssociations: map[data.Direction][]*data.Association{},
		parameters:       map[data.Direction][]*data.Parameter{},
		sets:             map[data.Direction][]*Set{},
	}

	ee := []error{}

	for _, opt := range actOpts {
		switch o := opt.(type) {
		case activityOption, RoleOption, data.PropertyOption:
			if err := o.Apply(&cfg); err != nil {
				ee = append(ee, err)
			}

		case foundation.BaseOption:
			cfg.baseOpts = append(cfg.baseOpts, opt)

		default:
			ee = append(ee,
				errs.New(
					errs.M("invalid option type for Activity"),
					errs.C(errorClass, errs.BulidingFailed,
						errs.TypeCastingError),
					errs.D("option_type", reflect.TypeOf(o).String())))
		}
	}

	if len(ee) > 0 {
		return nil, errors.Join(ee...)
	}

	return cfg.newActivity()
}

// Roles returns list of ResourceRoles of the Activity.
func (a *Activity) Roles() []*ResourceRole {
	rr := make([]*ResourceRole, len(a.roles))

	i := 0
	for _, r := range a.roles {
		rr[i] = r

		i++
	}

	return rr
}

// Properties implements an data.PropertyOwner interface and returns
// copy of the Activity properties.
func (a *Activity) Properties() []*data.Property {
	pp := make([]*data.Property, len(a.properties))

	i := 0
	for _, p := range a.properties {
		pp[i] = p

		i++
	}

	return pp
}

// SetDefaultFlow sets default flow from the Activity.
// If the flowId is empty, then default flow cleared for Activity.
func (a *Activity) SetDefaultFlow(flowId string) error {
	flowId = helpers.Strim(flowId)

	if flowId == "" {
		a.defaultFlow = nil

		return nil
	}

	for _, o := range a.Outgoing() {
		if o.Id() == flowId {
			a.defaultFlow = o

			return nil
		}
	}

	return errs.New(
		errs.M("flow %q dosn't existed in acitivity %q", flowId, a.Name()),
		errs.C(errorClass, errs.InvalidParameter))
}

func (a *Activity) BoundaryEvents() []flow.EventNode {
	return append([]flow.EventNode{}, a.boundaryEvents...)
}

// ------------------ flow.Node interface --------------------------------------

func (a *Activity) Node() flow.Node {
	return a
}

// NodeType returns Activity's node type.
func (a *Activity) NodeType() flow.NodeType {
	return flow.ActivityNodeType
}

// ------------------ flow.SequenceTarget interface ----------------------------

// AcceptIncomingFlow checks if it possible to use sf as IncomingFlow for the
// Activity.
func (a *Activity) AcceptIncomingFlow(sf *flow.SequenceFlow) error {
	// Activity has no restrictions on incoming floes
	return nil
}

// ------------------ flow.SequenceSource interface ----------------------------

// SuportOutgoingFlow checks if it possible to source sf SequenceFlow from
// the Activity.
func (a *Activity) SuportOutgoingFlow(sf *flow.SequenceFlow) error {
	// Activity has no restrictions on outgoing flows
	return nil
}

// -----------------------------------------------------------------------------
