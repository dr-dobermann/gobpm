package process

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	dataobjects "github.com/dr-dobermann/gobpm/pkg/model/data_objects"
	datastores "github.com/dr-dobermann/gobpm/pkg/model/data_stores"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	hi "github.com/dr-dobermann/gobpm/pkg/model/hinteraction"
	"github.com/dr-dobermann/gobpm/pkg/model/lanes"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

type processConfig struct {
	name  string
	props map[string]*data.Property
	roles map[string]*hi.ResourceRole

	// laneSets is ordered, not keyed: a lane set's name is optional and carries
	// no uniqueness rule, and lane order is visible in every diagram (SRD-076).
	laneSets []*lanes.LaneSet

	baseOpts []options.Option
}

// AddLaneSet implements lanes.LaneSetAdder — a Process is one of the two
// FlowElementsContainers BPMN hangs laneSets off.
func (pc *processConfig) AddLaneSet(ls *lanes.LaneSet) error {
	if ls == nil {
		return errs.New(
			errs.M("a nil LaneSet isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	pc.laneSets = append(pc.laneSets, ls)

	return nil
}

// ------------------ options.Configurator interface ---------------------------
//
// Validate validates processConfig fields.
func (pc *processConfig) Validate() error {
	if pc.name == "" {
		return errs.New(
			errs.M("process couldn't have an empty name"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	return nil
}

// ------------------- RoleConfigurator interface ------------------------------
//
// AddRole adds single non-empty unique ResourceRole into processConfig.
// if activityConfig already has the ResourceRole with the same name,
// it will be overwritten.
func (pc *processConfig) AddRole(r *hi.ResourceRole) error {
	if r == nil {
		return errs.New(
			errs.M("role couldn't be empty"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	pc.roles[r.Name()] = r

	return nil
}

// --------------- data.PropertyConfigurator interface -------------------------
//
// AddProperty adds non-empty property into the processConfig.
// if the activityConfig already has the property with the same name it
// will be overwritten.
func (pc *processConfig) AddProperty(p *data.Property) error {
	if p == nil {
		return errs.New(
			errs.M("property couldn't be empty"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	pc.props[p.Name()] = p

	return nil
}

func (pc *processConfig) newProcess() (*Process, error) {
	if err := pc.Validate(); err != nil {
		return nil, err
	}

	be, err := foundation.NewBaseElement(pc.baseOpts...)
	if err != nil {
		return nil, err
	}

	p := Process{
		name:                     pc.name,
		BaseElement:              *be,
		properties:               pc.props,
		roles:                    pc.roles,
		laneSets:                 pc.laneSets,
		CorrelationSubscriptions: []*bpmncommon.CorrelationSubscription{},
		nodes:                    map[string]flow.Node{},
		flows:                    map[string]*flow.SequenceFlow{},
		dataObjects:              map[string]*dataobjects.DataObject{},
		dataStoreRefs:            map[string]*datastores.DataStoreReference{},
	}

	return &p, nil
}
