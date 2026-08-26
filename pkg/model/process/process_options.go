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

	// ioParams are the declared I/O parameters per direction (ADR-040 §2.1).
	// Nil until the first WithInputs/WithOutputs: a process that declares
	// none stays contract-less (§2.5), distinguishable from an empty contract.
	ioParams map[data.Direction][]*data.Parameter

	baseOpts []options.Option
}

// AddIOParameters implements data.IOSpecAdder. A nil parameter cannot arrive
// here: data.WithInputs/WithOutputs refuse one before calling, and this config
// is unexported so nothing else can. A guard would be unreachable code.
func (pc *processConfig) AddIOParameters(
	dir data.Direction, params ...*data.Parameter,
) error {
	if pc.ioParams == nil {
		pc.ioParams = map[data.Direction][]*data.Parameter{}
	}

	pc.ioParams[dir] = append(pc.ioParams[dir], params...)

	return nil
}

// ioSpec materializes the declared contract, or nil when the process
// declared no parameter at all (ADR-040 §2.5 — the permissive, contract-less
// process).
func (pc *processConfig) ioSpec() (*data.InputOutputSpecification, error) {
	if pc.ioParams == nil {
		return nil, nil
	}

	ios, err := data.NewIOSpec()
	if err != nil {
		return nil, err
	}

	for _, dir := range []data.Direction{data.Input, data.Output} {
		for _, p := range pc.ioParams[dir] {
			// AddParameter rejects only a nil parameter or an invalid direction,
			// and the option refused both before a pair reached ioParams — a
			// failure here is a broken invariant, and reports as one.
			if err := ios.AddParameter(p, dir); err != nil {
				return nil, errs.Invariant("param %q rejected: %w", p.Name(), err)
			}
		}
	}

	return ios, nil
}

// AddLaneSet implements lanes.LaneSetAdder — a Process is one of the two
// FlowElementsContainers BPMN hangs laneSets off.
// A nil set cannot arrive here: lanes.WithLaneSets refuses one before calling,
// and this config is unexported so nothing else can. A guard would be
// unreachable code.
func (pc *processConfig) AddLaneSet(ls *lanes.LaneSet) error {
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

	ios, err := pc.ioSpec()
	if err != nil {
		return nil, err
	}

	p := Process{
		name:                     pc.name,
		BaseElement:              *be,
		properties:               pc.props,
		roles:                    pc.roles,
		laneSets:                 pc.laneSets,
		ioSpec:                   ios,
		CorrelationSubscriptions: []*bpmncommon.CorrelationSubscription{},
		nodes:                    map[string]flow.Node{},
		flows:                    map[string]*flow.SequenceFlow{},
		dataObjects:              map[string]*dataobjects.DataObject{},
		dataStoreRefs:            map[string]*datastores.DataStoreReference{},
	}

	return &p, nil
}
