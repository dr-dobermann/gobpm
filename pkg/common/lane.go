package common

type Lane struct {
	NamedElement

	nodes []*FlowNode

	childLaneSet *LaneSet
}

type LaneSet struct {
	NamedElement

	lanes []Lane

	parentLane *Lane
}

// func (l *Lane) addNode(n Node) error {
// 	for _, ln := range l.nodes {
// 		if ln.ID() == n.ID() {
// 			return NewPMErr(l.process.ID(), nil,
// 				"Node %s already exists on lane %s",
// 				n.Name(), l.Name())
// 		}
// 	}

// 	l.nodes = append(l.nodes, n)

// 	return n.PutOnLane(l)
// }

// func (l Lane) PerformerRole() string {

// 	return l.performerRole
// }
