package activities

import (
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"

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
	loopCharacteristics LoopCharacteristics
	roles               map[string]*hi.ResourceRole
	defaultFlow         *flow.SequenceFlow
	properties          map[string]*data.Property
	IoSpec              *data.InputOutputSpecification
	dataAssociations    map[data.Direction][]*data.Association
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
		params:           map[data.Direction][]*data.Parameter{},
	}

	ee := []error{}

	addErr := func(err error) {
		if err != nil {
			ee = append(ee, err)
		}
	}

	for _, opt := range actOpts {
		switch o := opt.(type) {
		case ActivityOption:
			addErr(o(&cfg))

		case RoleOption: // *activityConfig implements RoleConfigurator
			addErr(o(&cfg))

		case data.PropertyOption: // *activityConfig implements PropertyAdder
			addErr(o(&cfg))

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

// clone returns a per-instance copy of the activity: the properties are
// deep-copied so the clone owns private Property objects — a later edit to the
// source process (removing or re-valuing a property) can't leak into a
// registered snapshot or a running instance, and a value-less property is
// rejected here (FIX-017). The other configuration fields (loop characteristics,
// roles, default flow, IoSpec, data associations, quantities, flags) are shared
// by reference; the BaseNode shell is fresh (empty flows, no container).
// Execution data lives in the per-execution frame, never on the node
// (ADR-010 §2.4).
//
// boundaryEvents is deliberately left empty: a boundary's cross-references (host
// ↔ boundary) must point at the instance's own cloned nodes, not the shared
// model, so the per-instance graph build (snapshot.Clone) rebinds the cloned
// boundaries onto this cloned host (SRD-029 M3a). Copying the model boundaries
// here would leak shared nodes into the instance.
func (a *activity) clone() (activity, error) {
	props, err := data.CloneProperties(slices.Collect(maps.Values(a.properties)))
	if err != nil {
		return activity{}, err
	}

	properties := make(map[string]*data.Property, len(props))
	for _, p := range props {
		properties[p.Name()] = p
	}

	return activity{
		BaseNode:            a.CloneShell(),
		loopCharacteristics: a.loopCharacteristics,
		roles:               a.roles,
		defaultFlow:         a.defaultFlow,
		properties:          properties,
		IoSpec:              a.IoSpec,
		dataAssociations:    a.dataAssociations,
		startQuantity:       a.startQuantity,
		completionQuantity:  a.completionQuantity,
		isForCompensation:   a.isForCompensation,
	}, nil
}

// Roles returns list of ResourceRoles of the activity.
func (a *activity) Roles() []*hi.ResourceRole {
	return slices.Collect(maps.Values(a.roles))
}

// Properties implements an data.PropertyOwner interface and returns
// copy of the Activity properties.
func (a *activity) Properties() []*data.Property {
	return slices.Collect(maps.Values(a.properties))
}

// LoopCharacteristics returns the activity's loop/multi-instance marker, or nil
// when the activity runs exactly once (ADR-025). The runtime reads it to decide
// whether — and how — to iterate the activity.
func (a *activity) LoopCharacteristics() LoopCharacteristics {
	return a.loopCharacteristics
}

// SetDefaultFlow sets default flow from the Activity — the flow taken when no
// conditional outgoing flow fires (SRD-046). The flow must be one of the
// activity's outgoing flows and must NOT carry a condition (the BPMN rule the
// gateway's UpdateDefaultFlow enforces too). If the flowId is empty, then
// default flow cleared for Activity.
func (a *activity) SetDefaultFlow(flowID string) error {
	flowID = strings.TrimSpace(flowID)

	if flowID == "" {
		a.defaultFlow = nil

		return nil
	}

	for _, o := range a.Outgoing() {
		if o.ID() != flowID {
			continue
		}

		if o.Condition() != nil {
			return errs.New(
				errs.M("default flow %q must not carry a condition", flowID),
				errs.C(errorClass, errs.InvalidParameter),
				errs.D("activity_name", a.Name()))
		}

		a.defaultFlow = o

		return nil
	}

	return errs.New(
		errs.M("flow %q doesn't exist in activity %q", flowID, a.Name()),
		errs.C(errorClass, errs.InvalidParameter))
}

// DefaultFlow returns the activity's default outgoing flow, or nil when none
// is set (the gateway-getter symmetry, SRD-046).
func (a *activity) DefaultFlow() *flow.SequenceFlow {
	return a.defaultFlow
}

// BoundaryEvents returns list of events bounded to the acitvity.
func (a *activity) BoundaryEvents() []flow.EventNode {
	return append([]flow.EventNode{}, a.boundaryEvents...)
}

// AddBoundaryEvent attaches a boundary event to the activity. Multiplicity (at
// most one interrupting handler per Event Declaration) is enforced by
// BoundaryEvent.BoundTo before this is called; this stores the attachment.
func (a *activity) AddBoundaryEvent(be flow.BoundaryEvent) error {
	if be == nil {
		return errs.New(
			errs.M("a nil boundary event isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	a.boundaryEvents = append(a.boundaryEvents, be)

	return nil
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
