package process

import (
	"errors"

	"github.com/dr-dobermann/gobpm/pkg/common"
	"github.com/dr-dobermann/gobpm/pkg/data"
	mid "github.com/dr-dobermann/gobpm/pkg/identity"
)

var ErrSnapshotChange = errors.New("couldn't change process snapshot data")

type ProcessDataType uint8

const (
	PdtModel ProcessDataType = iota
	PdtSnapshot
)

type Process struct {
	common.FlowElementContainer

	// supportedBy []string // processes supported this one
	laneSet *LaneSet

	properties []data.Property

	subscriptions []common.CorrelationSubscription

	// processes tasks
	// tasks []TaskModel

	// processes gateways
	// gateways []GatewayModel

	// processes events
	// events []Events

	// flows []*common.SequenceFlow

	// the type of process data.
	// could be a real Model or Snapshot of the model.
	// Snapshot is used as a real-time model for process
	// execution
	dataType ProcessDataType

	// process messages
	// messages []*Message

	// consist an ID of original process
	// in case of its copying as snapshot
	// it's empty for the real process
	OriginID mid.Id
}

// // creates a new process
// func NewProcess(pid mid.Id, nm string, ver string) *Process {
// 	if pid == mid.Id(uuid.Nil) {
// 		pid = mid.NewID()
// 	}

// 	nm = strings.Trim(nm, " ")
// 	if len(nm) == 0 {
// 		nm = "Process #" + pid.String()
// 	}

// 	if len(ver) == 0 {
// 		ver = "0.1.0"
// 	}

// 	return &Process{
// 		FlowElement: *common.NewElement(pid, nm, common.EtProcess),
// 		version:     ver,
// 		tasks:       []TaskModel{},
// 		gateways:    []GatewayModel{},
// 		flows:       []*common.SequenceFlow{},
// 		messages:    make([]*Message, 0),
// 		lanes:       make(map[string]*Lane)}
// }

// // returns a version of the process.
// func (p *Process) Version() string {
// 	return p.version
// }

// func (p *Process) HasMessages() bool {
// 	return len(p.messages) > 0
// }

// // GetNodes returns a list of Nodes in the Process p.
// // Only Task, Gateway or Event could be returned.
// // If et is EtUnspecified then all three types of Nodes
// // will be returned in the same list.
// // If et is differs from EtAcitvity, EtGateway, EtEvent,
// // the panic will be fired
// func (p *Process) GetNodes(et common.FlowElementType) ([]Node, error) {

// 	nn := []Node{}

// 	switch et {
// 	// return all nodes
// 	case common.EtUnspecified:
// 		nn = append(nn, p.getTasks()...)
// 		nn = append(nn, p.getGateways()...)

// 	case common.EtActivity:
// 		nn = p.getTasks()

// 	case common.EtGateway:
// 		nn = p.getGateways()

// 	//case EtEvent:

// 	default:
// 		return nil,
// 			NewPMErr(p.ID(), nil, "wrong element type for node [%s]",
// 				et.String())
// 	}

// 	return nn, nil

// }

// func (p *Process) getTasks() []Node {
// 	nn := []Node{}

// 	for _, t := range p.tasks {
// 		nn = append(nn, t)
// 	}

// 	return nn
// }

// func (p *Process) getGateways() []Node {
// 	nn := []Node{}

// 	for _, g := range p.gateways {
// 		nn = append(nn, g)
// 	}

// 	return nn
// }

// // returns a task by its ID.
// //
// // if there is no such task, nil will be returned.
// func (p *Process) GetTask(tid mid.Id) TaskModel {
// 	for _, t := range p.tasks {
// 		if t.ID() == tid {
// 			return t
// 		}
// 	}

// 	return nil
// }

// // GetMessage returns a *Message from the Process p.
// // If there is no Message with name mn, the error will be returned
// func (p *Process) GetMessage(mn string) (*Message, error) {
// 	for _, m := range p.messages {
// 		if m.Name() == mn {
// 			return m, nil
// 		}
// 	}

// 	return nil,
// 		NewPMErr(
// 			p.ID(),
// 			nil, "couldn't find message '%s' in process",
// 			mn)
// }

// // creates a copy from a model process.
// // copy could not be made from a copy (snapshot) of the process.
// func (p Process) Copy() (*Process, error) {
// 	if p.dataType == PdtSnapshot {
// 		return nil, NewPMErr(p.ID(), nil, "couldn't make a copy of snapshot")
// 	}

// 	pc := Process{
// 		FlowElement: p.FlowElement,
// 		lanes:       make(map[string]*Lane),
// 		tasks:       make([]TaskModel, 0),
// 		flows:       make([]*common.SequenceFlow, 0),
// 		messages:    make([]*Message, 0)}

// 	// copy lanes
// 	for l := range p.lanes {
// 		pc.NewLane(l)
// 	}

// 	// copy messages
// 	for _, m := range p.messages {
// 		pc.AddMessage(m.Name(), m.direction, m.GetVariables(AllVariables)...)
// 	}

// 	// nm used as a mapper from source process node id to
// 	// a copy process node used in copying of sequenceFlow in the new
// 	// process
// 	nm, err := p.copyTasks(&pc)
// 	if err != nil {
// 		return nil, NewPMErr(p.ID(), err, "couldn't copy process tasks")
// 	}

// 	// get mapper for gateways
// 	gm, err := p.copyGateways(&pc)
// 	if err != nil {
// 		return nil, NewPMErr(p.ID(), err, "couldn't copy process gateways")
// 	}

// 	// combine task's and gateway's mappers
// 	for oldGID, n := range gm {
// 		nm[oldGID] = n
// 	}

// 	err = p.copyFlows(&pc, nm)
// 	if err != nil {
// 		return nil, NewPMErr(p.ID(), err, "snapshot node linking error")
// 	}

// 	pc.dataType = PdtSnapshot
// 	pc.OriginID = p.ID()

// 	return &pc, nil
// }

// // copy tasks from p to pc and creates mapping of p.tasks ot a pc.tasks for
// // future linking througt sequenceFlow
// func (p *Process) copyTasks(pc *Process) (map[mid.Id]Node, error) {
// 	tm := make(map[mid.Id]Node)

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
// func (p *Process) copyGateways(pc *Process) (map[mid.Id]Node, error) {
// 	gm := make(map[mid.Id]Node)

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
// func (p *Process) copyFlows(pc *Process, nodeMapper map[mid.Id]Node) error {
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
// func (p *Process) NewLane(nm string) error {
// 	if p.dataType == PdtSnapshot {
// 		return ErrSnapshotChange
// 	}

// 	nm = strings.Trim(nm, " ")
// 	if len(nm) > 0 {
// 		if _, ok := p.lanes[nm]; ok {
// 			return NewPMErr(p.ID(), nil,
// 				"Lane '%s' already exists", nm)
// 		}
// 	}

// 	l := new(Lane)
// 	l.SetNewID(mid.NewID())
// 	l.SetName(nm)
// 	l.SetType(common.EtLane)
// 	l.process = p
// 	l.nodes = make([]Node, 0)

// 	if len(nm) == 0 {
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
