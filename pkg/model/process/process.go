package process

import (
	"errors"
	"reflect"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	hi "github.com/dr-dobermann/gobpm/pkg/model/hinteraction"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"golang.org/x/exp/maps"
)

const errorClass = "PROCESS_ERRORS"

// A Process describes a sequence or flow of Activities in an organization with
// the objective of carrying out work. In BPMN a Process is depicted as a graph
// of Flow Elements, which are a set of Activities, Events, Gateways, and
// Sequence Flows that define finite execution semantics. Processes can be
// defined at any level from enterprise-wide Processes to Processes performed
// by a single person. Low-level Processes can be grouped together to achieve a
// common business goal.
type Process struct {
	foundation.BaseElement

	name string

	// DEV_NOTE: CallableElement should be implemented as interface so it
	// it shouldn't be used as a field or embedded struct.
	// common.CallableElement

	// DEV_NOTE: Container replaced by interface flow.Container
	// flow.ElementsContainer

	// An optional Boolean value specifying whether the Process is executable.
	// An executable Process is a private Process that has been modeled for the
	// purpose of being executed according to the semantics of Clause 14. Of
	// course, during the development cycle of the Process, there will be stages
	// where the Process does not have enough detail to be “executable.”
	// A non-executable Process is a private Process that has been modeled for
	// the purpose of documenting Process behavior at a modeler-defined level of
	// detail. Thus, information needed for execution, such as formal condition
	// expressions are typically not included in a non-executable Process.
	// For public Processes, no value has the same semantics as if the value
	// were false. The value MAY not be true for public Processes.
	//
	// DEV_NOTE: goBpm should run only executable processes, so this attribute
	// SHOULD be always true
	// isExecutable bool

	// Modeler-defined properties MAY be added to a Process. These properties
	// are contained within the Process. All Tasks and Sub-Processes SHALL have
	// access to these properties.
	//
	// properties are indexed by the property names.
	properties map[string]*data.Property

	// Defines the resource roles that will perform or will be responsible for
	// the Process. The resource, e.g., a performer, can be specified in the
	// form of a specific individual, a group, an organization role or position,
	// or an organization.
	// Note that the assigned resources of the Process does not determine the
	// assigned resources of the Activities that are contained by the Process.
	//
	// roles indexed by role.Name
	roles map[string]*hi.ResourceRole

	// correlationSubscriptions are a feature of context-based correlation.
	// CorrelationSubscriptions are used to correlate incoming Messages against
	// data in the Process context. A Process MAY contain several
	// correlationSubscriptions.
	CorrelationSubscriptions []*common.CorrelationSubscription

	// nodes keeps all flow.FlowNodes of the Process.
	// it indexed by FlowNode id.
	nodes map[string]flow.Node

	flows map[string]*flow.SequenceFlow
}

// New creates a new Process and returns its pointer.
// Available options:
//
//	activities.WithRoles
//	data.WithProperties
//	foundation.WithId
//	foundation.WithDoc
func New(
	name string,
	procOpts ...options.Option,
) (*Process, error) {
	pc := processConfig{
		name:     strings.Trim(name, " "),
		props:    map[string]*data.Property{},
		roles:    map[string]*hi.ResourceRole{},
		baseOpts: []options.Option{},
	}

	ee := []error{}

	for _, po := range procOpts {
		switch opt := po.(type) {
		case activities.RoleOption, data.PropertyOption:
			if err := opt.Apply(&pc); err != nil {
				ee = append(ee, err)
			}

		case foundation.BaseOption:
			pc.baseOpts = append(pc.baseOpts, opt)

		default:
			ee = append(ee,
				errs.New(
					errs.M("invalid option type for Process"),
					errs.C(errorClass, errs.BulidingFailed,
						errs.TypeCastingError),
					errs.D("option_type", reflect.TypeOf(opt).String())))
		}
	}

	if len(ee) > 0 {
		return nil, errors.Join(ee...)
	}

	return pc.newProcess()
}

func (p *Process) Name() string {
	return p.name
}

// Properties returns the Process properties.
func (p *Process) Properties() []*data.Property {
	return maps.Values(p.properties)
}

// addNode adds non-empty unique FlowNode n to the process p.
func (p *Process) addNode(n flow.Node) error {
	if n == nil {
		return errs.New(
			errs.M("node couldn't be empty"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if _, ok := p.nodes[n.Id()]; ok {
		return errs.New(
			errs.M("node %q(%s) already registered in process.",
				n.Name(), n.Id()),
			errs.C(errorClass, errs.DuplicateObject))
	}

	p.nodes[n.Id()] = n

	return n.BindTo(p)
}

// Nodes returns a slice of Process flow.FlowNodes of one of types.
// if types aren't specified then all nodes returned.
func (p *Process) Nodes(types ...flow.NodeType) []flow.Node {
	if err := flow.ValidateNodeTypes(types...); err != nil {
		return []flow.Node{}
	}

	fnn := make([]flow.Node, 0, len(p.nodes))
	for _, n := range p.nodes {
		if len(types) == 0 || has(types, n.NodeType()) {
			fnn = append(fnn, n)
		}
	}

	return fnn
}

// has checks if slice has item.
func has[T comparable](slice []T, item T) bool {
	for _, i := range slice {
		if i == item {
			return true
		}
	}

	return false
}

// addFlow add non-empty unique SequenceFlow into the Process.
func (p *Process) addFlow(f *flow.SequenceFlow) error {
	if f == nil {
		return errs.New(
			errs.M("flow couldn't be empty"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if _, ok := p.flows[f.Id()]; ok {
		return errs.New(
			errs.M("flow %q already exists in the process %q",
				f.Id(), p.Id()),
			errs.C(errorClass, errs.DuplicateObject))
	}

	p.flows[f.Id()] = f

	return f.BindTo(p)
}

// Flows returns all processes flows.
func (p *Process) Flows() []*flow.SequenceFlow {
	ff := make([]*flow.SequenceFlow, 0, len(p.flows))

	for _, f := range p.flows {
		ff = append(ff, f)
	}

	return ff
}

// ---------------------- flow.Container interface -----------------------------

// Add adds new Element into the Process p.
// On failure it returns an error.
func (p *Process) Add(e flow.Element) error {
	if e == nil {
		return errs.New(
			errs.M("flow element couldn't be empty"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	switch e.EType() {
	case flow.NodeElement:
		return p.addNode(e.(flow.Node))

	case flow.SequenceFlowElement:
		return p.addFlow(e.(*flow.SequenceFlow))
	}

	return errs.New(
		errs.M("invalid flow element type: %s", reflect.TypeOf(e).String()),
		errs.C(errorClass, errs.InvalidParameter))
}

// Elements returns all processes elements.
func (p *Process) Elements() []flow.Element {
	fee := make([]flow.Element, 0, len(p.nodes)+len(p.flows))

	for _, n := range p.nodes {
		fee = append(fee, n.(flow.Element))
	}

	for _, f := range p.flows {
		fee = append(fee, f)
	}

	return fee
}

// Remove deletes single flow or node for the Process p.
func (p *Process) Remove(e flow.Element) error {
	if e == nil {
		return errs.New(
			errs.M("element couldn't be empty"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if _, ok := p.nodes[e.Id()]; ok {
		delete(p.nodes, e.Id())

		return e.Unbind()
	}

	if _, ok := p.flows[e.Id()]; ok {
		delete(p.flows, e.Id())

		return e.Unbind()
	}

	return errs.New(
		errs.M("element %q(%s) not found in process", e.Name(), e.Id()),
		errs.C(errorClass, errs.ObjectNotFound))
}

// ------------------------------------------------------------------------------
var _ flow.Container = (*Process)(nil)
