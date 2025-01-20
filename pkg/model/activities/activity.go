package activities

import (
	"errors"
	"reflect"
	"strings"

	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	hi "github.com/dr-dobermann/gobpm/pkg/model/hinteraction"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"golang.org/x/exp/maps"
)

// The activity class is the abstract super class for all concrete Activity
// types.
type activity struct {
	flow.FlowNode

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
	loopCharacteristics *LoopCharacteristics

	// Defines the resource that will perform or will be responsible for the
	// Activity. The resource, e.g., a performer, can be specified in the form
	// of a specific individual, a group, an organization role or position, or
	// an organization.
	//
	// DEV_NOTE: roles indexed by role name not by the addition order.
	roles map[string]*hi.ResourceRole

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

// newActivity creates a new Activity with options and returns its pointer on
// success or errors on failure.
func newActivity(
	name string,
	actOpts ...options.Option,
) (*activity, error) {
	cfg := activityConfig{
		name:             strings.TrimSpace(name),
		roles:            map[string]*hi.ResourceRole{},
		props:            map[string]*data.Property{},
		startQ:           1,
		complQ:           1,
		baseOpts:         []options.Option{},
		dataAssociations: map[data.Direction][]*data.Association{},
		sets:             map[data.Direction][]*setDef{},
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
					errs.M("invalid option type for activity"),
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

// Roles returns list of ResourceRoles of the activity.
func (a *activity) Roles() []*hi.ResourceRole {
	return maps.Values(a.roles)
}

// Properties implements an data.PropertyOwner interface and returns
// copy of the Activity properties.
func (a *activity) Properties() []*data.Property {
	return maps.Values(a.properties)
}

// SetDefaultFlow sets default flow from the Activity.
// If the flowId is empty, then default flow cleared for Activity.
func (a *activity) SetDefaultFlow(flowId string) error {
	flowId = strings.TrimSpace(flowId)

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

// BoundaryEvents returns list of events bounded to the acitvity.
func (a *activity) BoundaryEvents() []flow.EventNode {
	return append([]flow.EventNode{}, a.boundaryEvents...)
}

// ------------------ flow.Node interface --------------------------------------

// Node returns Node itself.
func (a *activity) Node() flow.Node {
	return a
}

// NodeType returns Activity's node type.
func (a *activity) NodeType() flow.NodeType {
	return flow.ActivityNodeType
}

// ------------------ flow.SequenceTarget interface ----------------------------

// AcceptIncomingFlow checks if it possible to use sf as IncomingFlow for the
// activity.
func (a *activity) AcceptIncomingFlow(_ *flow.SequenceFlow) error {
	// Activity has no restrictions on incoming floes
	return nil
}

// ------------------ flow.SequenceSource interface ----------------------------

// SuportOutgoingFlow checks if it possible to source sf SequenceFlow from
// the activity.
func (a *activity) SupportOutgoingFlow(_ *flow.SequenceFlow) error {
	// activity has no restrictions on outgoing flows
	return nil
}

// -----------------------------------------------------------------------------

// interfaces check
var (
	_ flow.Node = (*activity)(nil)

	_ flow.SequenceSource = (*activity)(nil)
	_ flow.SequenceTarget = (*activity)(nil)
)
