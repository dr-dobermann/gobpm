package model

import "github.com/dr-dobermann/gobpm/pkg/common"

type Lane struct {
	common.FlowElement
	process *Process
	nodes   []Node
}

func (l *Lane) addNode(n Node) error {
	for _, ln := range l.nodes {
		if ln.ID() == n.ID() {
			return NewPMErr(l.process.ID(), nil,
				"Node %s already exists on lane %s",
				n.Name(), l.Name())
		}
	}

	l.nodes = append(l.nodes, n)

	return n.PutOnLane(l)
}
