package process

import (
	"errors"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/identity"
	"github.com/dr-dobermann/gobpm/pkg/model"
	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
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

	flows map[identity.Id]common.SequenceFlow

	properties []data.Property

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

// New creates a new process
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
		flows:                map[identity.Id]common.SequenceFlow{},
		properties:           []data.Property{},
		dataType:             PdtModel,
		OriginID:             identity.EmptyID(),
	}
}

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
			return err
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
		flows:                map[identity.Id]common.SequenceFlow{},
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

// // copy tasks from p to pc and creates mapping of p.tasks ot a pc.tasks for
// // future linking througt sequenceFlow
// func (p *Process) copyTasks(pc *Process) (map[identity.Id]Node, error) {
// 	tm := make(map[identity.Id]Node)

// 	// copy tasks and place them on lanes
// 	for _, ot := range p.tasks {
// 		t, err := ot.Copy(pc)
// 		if err != nil {
// 			return nil,
// 				fmt.Errorf("couldn't copy task '%s': %v", ot.Name(), err)
// 		}

// 		t.ClearFlows()

// 		tm[ot.ID()] = t

// 		pc.AddTask(t, ot.LaneName())
// 	}

// 	return tm, nil
// }

// // copies gateway to a new process and return gateway mapper of
// // old gateway id to a copied Node
// func (p *Process) copyGateways(pc *Process) (map[identity.Id]Node, error) {
// 	gm := make(map[identity.Id]Node)

// 	// copy gateway and place them on lanes
// 	for _, og := range p.gateways {
// 		g, err := og.Copy(pc)
// 		if err != nil {
// 			return nil,
// 				fmt.Errorf("couldn't copy gateway '%s': %v", og.Name(), err)
// 		}

// 		g.ClearFlows()

// 		gm[og.ID()] = g

// 		pc.AddGateway(g, og.LaneName())
// 	}

// 	return gm, nil
// }

// // copies flows in copied process based on old process flows and node mappers
// func (p *Process) copyFlows(pc *Process, nodeMapper map[identity.Id]Node) error {
// 	for _, of := range p.flows {
// 		var e expr.Expression

// 		if of.GetExpression() != nil {
// 			e = of.GetExpression().Copy()
// 		}

// 		if err := pc.LinkNodes(
// 			nodeMapper[of.GetSource().ID()],
// 			nodeMapper[of.GetTarget().ID()], e); err != nil {

// 			return NewPMErr(p.ID(), err, "couldn't link nodes in snapshot")
// 		}
// 	}

// 	return nil
// }

// // adds new lane to the process
// func (p *Process) NewLane(name string) error {
// 	if p.dataType == PdtSnapshot {
// 		return ErrSnapshotChange
// 	}

// 	name = strings.Trim(name, " ")
// 	if len(name) > 0 {
// 		if _, ok := p.lanes[name]; ok {
// 			return NewPMErr(p.ID(), nil,
// 				"Lane '%s' already exists", name)
// 		}
// 	}

// 	l := new(Lane)
// 	l.SetNewID(identity.NewID())
// 	l.SetName(name)
// 	l.SetType(common.EtLane)
// 	l.process = p
// 	l.nodes = make([]Node, 0)

// 	if len(name) == 0 {
// 		l.SetName("Lane " + l.ID().String())
// 	}

// 	p.lanes[l.Name()] = l

// 	return nil
// }

// // returns a slice of process lanes
// func (p *Process) Lanes() []string {
// 	ln := []string{}

// 	for l := range p.lanes {
// 		ln = append(ln, l)
// 	}

// 	return ln
// }

// // remove lane from the process
// func (p *Process) RemoveLane(ln string) error {
// 	if p.dataType == PdtSnapshot {
// 		return ErrSnapshotChange
// 	}

// 	ln = strings.Trim(ln, " ")

// 	l, ok := p.lanes[ln]
// 	if l == nil || !ok {
// 		return NewPMErr(p.ID(), nil, "lane '%s' isn't found", ln)
// 	}

// 	if len(p.lanes[ln].nodes) > 0 {
// 		return NewPMErr(p.ID(), nil,
// 			"couldn't remove non-empty lane '%s'", ln)
// 	}

// 	delete(p.lanes, ln)

// 	return nil
// }

// // register a single non-empty, non-duplicating message
// // in the process.
// func (p *Process) AddMessage(msgName string,
// 	dir MessageFlowDirection, vars ...MessageVariable) (*Message, error) {

// 	if p.dataType == PdtSnapshot {
// 		return nil, ErrSnapshotChange
// 	}

// 	msgName = strings.Trim(msgName, " ")
// 	if len(msgName) == 0 {
// 		return nil, NewPMErr(p.ID(), nil,
// 			"couldn't register meassage with an empty name")
// 	}

// 	for _, m := range p.messages {
// 		if m.Name() == msgName {
// 			return nil, NewPMErr(p.ID(), nil,
// 				"message '%s' already exists", msgName)
// 		}
// 	}

// 	msg, err := NewMessage(msgName, dir, vars...)
// 	if err != nil {
// 		return nil,
// 			NewPMErr(p.ID(), err,
// 				"couldn't register message '%s' to process", msgName)
// 	}

// 	p.messages = append(p.messages, msg)

// 	return msg, nil
// }

// // AddTask adds a new task into the Process Model into lane named ln.
// // If t is nil, or ln is the wrong lane name the error would be
// // returned.
// func (p *Process) AddTask(t TaskModel, ln string) error {
// 	if p.dataType == PdtSnapshot {
// 		return ErrSnapshotChange
// 	}

// 	if t == nil {
// 		return NewPMErr(p.ID(), nil,
// 			"сouldn't add nil task or task with an empty name")
// 	}

// 	l, ok := p.lanes[ln]
// 	if !ok {
// 		return NewPMErr(p.ID(), nil, "lane '%s' is not found", ln)
// 	}

// 	if n := p.getNamedNode(t.Name()); n != nil {
// 		return NewPMErr(p.ID(), nil, "node named '%s'(%s) "+
// 			"already exists in the process", n.Name(), n.Type().String())
// 	}

// 	if err := t.Check(); err != nil {
// 		return NewPMErr(p.ID(), err,
// 			"task %s doesn't pass self-check", t.Name())
// 	}

// 	p.tasks = append(p.tasks, t)
// 	l.addNode(t)

// 	return nil
// }

// func (p *Process) AddGateway(g GatewayModel, lane string) error {
// 	if p.dataType == PdtSnapshot {
// 		return ErrSnapshotChange
// 	}

// 	if g == nil {
// 		return NewPMErr(p.ID(), nil, "couldn't add nil gateway")
// 	}

// 	l, ok := p.lanes[lane]
// 	if !ok {
// 		return NewPMErr(p.ID(), nil, "lane %s is not found", lane)
// 	}

// 	if n := p.getNamedNode(g.Name()); n != nil {
// 		return NewPMErr(p.ID(), nil, "node named '%s'(%s) "+
// 			"already exists in the process", n.Name(), n.Type().String())
// 	}

// 	p.gateways = append(p.gateways, g)
// 	l.addNode(g)

// 	return nil
// }

// // links two Nodes by one SequenceFlow.
// //
// // expr added as Expression on SequenceFlow
// // TODO: Add SequenceFlow name
// func (p *Process) LinkNodes(
// 	src Node,
// 	trg Node,
// 	expr expr.Expression) error {

// 	if p.dataType == PdtSnapshot {
// 		return ErrSnapshotChange
// 	}

// 	if src == nil || trg == nil {
// 		return NewPMErr(p.ID(), nil,
// 			"trying to link nil-nodes. src: %v, dest: %v", src, trg)
// 	}

// 	if src.ProcessID() != p.ID() ||
// 		trg.ProcessID() != p.ID() {
// 		return NewPMErr(p.ID(), nil,
// 			"nodes isnt't binded to process src.pID[%v], trg.pID[%v]",
// 			src.ProcessID(), trg.ProcessID())
// 	}

// 	sf, err := src.Connect(trg, "")
// 	if err != nil {
// 		return NewPMErr(p.ID(), err,
// 			"couldn't connect sequence flow [%v] to node '%s' as source",
// 			sf.ID(), src.Name())
// 	}

// 	p.flows = append(p.flows, &sf)

// 	return nil
// }

// func (p *Process) LinkNamedNodes(src, dest string, expr expr.Expression) error {
// 	const notFound = "couldn't find node named '%s'"

// 	srcN := p.getNamedNode(src)
// 	if srcN == nil {
// 		return fmt.Errorf(notFound, src)
// 	}

// 	destN := p.getNamedNode(dest)
// 	if destN == nil {
// 		return fmt.Errorf(notFound, dest)
// 	}

// 	return p.LinkNodes(srcN, destN, expr)
// }

// func (p *Process) getNamedNode(name string) Node {
// 	name = strings.Trim(name, " ")

// 	for _, t := range p.tasks {
// 		if name == t.Name() {
// 			return t
// 		}
// 	}

// 	// should be done the same for gateways and events

// 	return nil
// }
