package activities

import (
	"errors"
	"reflect"
	"strings"

	"golang.org/x/exp/maps"

	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	hi "github.com/dr-dobermann/gobpm/pkg/model/hinteraction"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// The activity class is the abstract super class for all concrete Activity
// types.
type activity struct {
	flow.BaseNode
	loopCharacteristics *LoopCharacteristics
	roles               map[string]*hi.ResourceRole
	defaultFlow         *flow.SequenceFlow
	properties          map[string]*data.Property
	IoSpec              *data.InputOutputSpecification
	dataAssociations    map[data.Direction][]*data.Association
	dataPath            scope.DataPath
	boundaryEvents      []flow.EventNode
	startQuantity       int
	completionQuantity  int
	isForCompensation   bool
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
		case ActivityOption, RoleOption, data.PropertyOption:
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
func (a *activity) SetDefaultFlow(flowID string) error {
	flowID = strings.TrimSpace(flowID)

	if flowID == "" {
		a.defaultFlow = nil

		return nil
	}

	for _, o := range a.Outgoing() {
		if o.ID() == flowID {
			a.defaultFlow = o

			return nil
		}
	}

	return errs.New(
		errs.M("flow %q dosn't existed in acitivity %q", flowID, a.Name()),
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
