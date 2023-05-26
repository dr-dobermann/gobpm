package process

import (
	"errors"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/identity"
	"github.com/dr-dobermann/gobpm/pkg/model"
	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/expression"
)

var ErrSnapshotChange = errors.New("couldn't change process snapshot data")

type ProcessDataType uint8

const (
	PdtModel ProcessDataType = iota
	PdtSnapshot
)

const (
	defaultLaneName string = "__default"
)

type Process struct {
	common.FlowElementContainer

	laneSet *common.LaneSet

	nodes map[identity.Id]common.Node

	flows map[identity.Id]*common.SequenceFlow

	properties []data.Property

	// name indexed messages
	messages map[string]common.Message

	// the type of process data.
	// could be a process Model or process model Snapshot.
	// Snapshot is used as a real-time model for process
	// execution.
	// Snapshot could not be changed
	dataType ProcessDataType

	// consist an ID of original process
	// in case of its copying as snapshot
	// it's empty for the real process
	OriginID identity.Id
}

// ===================== Core services =========================================
func New(pid identity.Id, name string) *Process {

	if pid == identity.EmptyID() {
		pid = identity.NewID()
	}

	name = strings.Trim(name, " ")
	if len(name) == 0 {
		name = "Process #" + pid.String()
	}

	return &Process{
		FlowElementContainer: *common.NewContainer(pid, name),
		laneSet:              common.NewLaneSet("LSP#" + pid.String()),
		nodes:                map[identity.Id]common.Node{},
		flows:                map[identity.Id]*common.SequenceFlow{},
		properties:           []data.Property{},
		messages:             map[string]common.Message{},
		dataType:             PdtModel,
		OriginID:             identity.EmptyID(),
	}
}

// Copy creates a copy from a model process.
// copy could not be made from a copy (snapshot) of the process.
func (p Process) Copy() (*Process, error) {
	if p.dataType == PdtSnapshot {
		return nil, model.NewModelError(p.Name(), p.ID(), nil,
			"couldn't make a copy of snapshot")
	}

	pc := Process{
		FlowElementContainer: *common.NewContainer(identity.NewID(), p.Name()),
		laneSet:              nil,
		nodes:                map[identity.Id]common.Node{},
		properties:           []data.Property{},
		flows:                map[identity.Id]*common.SequenceFlow{},
		dataType:             PdtSnapshot,
		OriginID:             p.ID(),
	}

	// copy nodes
	// nodeMap is used as a mapper from source process nodes id to
	// a copied process nodes.
	nodeMap := map[identity.Id]common.Node{}

	for _, n := range p.nodes {
		nn := n.Copy()
		nodeMap[n.ID()] = nn
		pc.nodes[nn.ID()] = nn
	}

	// copy lanes
	if p.laneSet != nil {
		pc.laneSet = p.laneSet.Copy()

		// create map of copied lanes on source lanes' IDs
		lm := map[identity.Id]*common.Lane{}
		for _, l := range pc.laneSet.GetAllLanes(true) {
			lm[l.SourceID()] = l
		}

		for _, l := range p.laneSet.GetAllLanes(true) {
			for _, n := range l.GetAllNodes() {
				lm[l.ID()].AddNode(nodeMap[n.ID()].GetFlowNode())
			}
		}
	}

	// copy messages

	// copy flows

	return &pc, nil
}

// ==================== Node Managing ==========================================

// AddNode adds the Node n into process p on lane with name laneName.
// If n already binded to another process then error returns.
// if there is no lane with name laneName then new Lane created and
// n placed onto it.
// if the laneName is empty, then node is placed on default Lane.
func (p *Process) AddNode(n common.Node, laneName string) error {

	if n.GetProcessID() != identity.EmptyID() {
		return model.NewModelError(p.Name(), p.ID(), nil,
			"node %s[%v] already binded to process %v",
			n.Name(), n.ID(), n.GetProcessID())
	}

	laneName = strings.Trim(laneName, " ")
	if laneName == defaultLaneName {
		return model.NewModelError(p.Name(), p.ID(), nil,
			"invalid lane name. To put node on default lane just set laneName empty")
	}

	if n.Type() != common.EtActivity &&
		n.Type() != common.EtEvent &&
		n.Type() != common.EtGateway {
		return model.NewModelError(p.Name(), p.ID(), nil,
			"invalid node type. Want EtActivity, EtEvent or EtGateway, has %v",
			n.Type())
	}

	var (
		l   *common.Lane
		err error
	)

	if laneName != "" {
		l, err = p.laneSet.GetLaneByName(laneName, true)
		if err != nil {
			l = common.NewLane(identity.EmptyID(), laneName)
			p.laneSet.AddLanes(l)
		}
	} else {
		l, err = p.laneSet.GetLaneByName(defaultLaneName, false)
		if err != nil {
			l = common.NewLane(identity.EmptyID(), defaultLaneName)
			p.laneSet.AddLanes(l)
		}
	}

	l.AddNode(n.GetFlowNode())
	p.nodes[n.ID()] = n

	return n.BindToProcess(p.ID())
}

// ConnectNodes connects two nodes added to the process.
func (p *Process) ConnectNodes(from, to common.Node,
	flowName string, cond expression.Condition) error {

	// check nodes registration on process
	for _, n := range []common.Node{from, to} {
		if _, ok := p.nodes[n.ID()]; !ok {
			return model.NewModelError(p.Name(), p.ID(), nil,
				"node %s[%v] isn't registered on process", n.Name(), n.ID())
		}
	}

	// connect them
	flow, err := from.GetFlowNode().Connect(to.GetFlowNode(),
		flowName, cond)
	if err != nil {
		return model.NewModelError(p.Name(), p.ID(), err,
			"couldn't connect node %s[%v] with %s[%v]",
			from.Name(), from.ID(), to.Name(), to.ID())
	}

	// if everything is ok, add new flow on process
	p.flows[flow.ID()] = flow

	return nil
}

// GetNodes returns a list of Nodes in the Process p.
// Only Task, Gateway or Event could be returned.
// If et is EtUnspecified then all three types of Nodes
// will be returned in the same list.
// If et is differs from EtAcitvity, EtGateway, EtEvent,
// the error will be returned
func (p *Process) GetNodes(et common.FlowElementType) ([]common.Node, error) {

	if et != common.EtUnspecified &&
		et != common.EtActivity &&
		et != common.EtGateway &&
		et != common.EtEvent {

		return nil,
			model.NewModelError(p.Name(), p.ID(), nil,
				"invalid element type. Wants EtActivity, EtGateway, EtEvent, EtUnspecified), has %v", et)
	}

	nn := []common.Node{}

	for _, n := range p.nodes {
		if et == common.EtUnspecified {
			if n.Type() == common.EtActivity ||
				n.Type() == common.EtEvent ||
				n.Type() == common.EtGateway {

				nn = append(nn, n)
			}

			continue
		}

		if et == n.Type() {
			nn = append(nn, n)
		}
	}

	return nn, nil
}

// ==================== Message Services =======================================
func (p *Process) RegisterMessage(m *common.Message) error {

	if m == nil {
		return model.NewModelError(p.Name(), p.ID(), nil,
			"no message to register")
	}

	if _, ok := p.messages[m.Name()]; ok {
		return model.NewModelError(p.Name(), p.ID(), nil,
			"message %q already registered", m.Name())
	}

	nm, err := common.NewMessage(m.Name(), m.GetItem())
	if err != nil {
		return model.NewModelError(p.Name(), p.ID(), err,
			"coudln't register message %q", m.Name())
	}

	p.messages[m.Name()] = *nm

	return nil
}

func (p *Process) GetMessage(name string) (*common.Message, error) {

	m, ok := p.messages[name]
	if !ok {
		return nil,
			model.NewModelError(p.Name(), p.ID(), nil,
				"no message found %q", name)
	}

	return common.NewMessage(m.Name(), m.GetItem())
}

func (p *Process) RemoveMessage(name string) error {

	if _, ok := p.messages[name]; !ok {
		return model.NewModelError(p.Name(), p.ID(), nil,
			"there is no message %q", name)
	}

	delete(p.messages, name)

	return nil
}
