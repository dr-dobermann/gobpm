package process

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

type processConfig struct {
	name  string
	props map[string]*data.Property
	roles map[string]*activities.ResourceRole

	baseOpts []options.Option
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
func (pc *processConfig) AddRole(r *activities.ResourceRole) error {
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
// AddProperty adds non-empyt property into the processConfig.
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
		CorrelationSubscriptions: []*common.CorrelationSubscription{},
		nodes:                    map[string]flow.Node{},
		flows:                    map[string]*flow.SequenceFlow{},
	}

	return &p, nil
}
