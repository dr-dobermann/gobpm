package model

type Lane struct {
	FlowElement
	process *Process
	nodes   []Node
}

func (l *Lane) addNode(n Node) error {
	for _, ln := range l.nodes {
		if ln.ID() == n.ID() {
			return NewPMErr(l.process.id, nil,
				"Node %s already exists on lane %s",
				n.Name(), l.name)
		}
	}

	l.nodes = append(l.nodes, n)

	return nil
}
