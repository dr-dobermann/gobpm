package snapshot

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

const errorClass = "SNAPSHOT_ERRORS"

// Snapshot holds process'es snapshot ready to run.
type Snapshot struct {
	foundation.ID

	ProcessId   string
	ProcessName string
	Nodes       map[string]flow.Node
	Flows       map[string]*flow.SequenceFlow
	Properties  []*data.Property
}

// New creates a new snapshot from the Process p and returns its
// pointer on success or error on failure.
func New(
	p *process.Process,
	snapOpts ...options.Option,
) (*Snapshot, error) {
	if p == nil {
		return nil,
			errs.New(
				errs.M("process is empty"),
				errs.C(errorClass, errs.EmptyNotAllowed))
	}

	s := Snapshot{
		ID:          *foundation.NewID(),
		ProcessId:   p.Id(),
		ProcessName: p.Name(),
		Nodes:       map[string]flow.Node{},
		Flows:       map[string]*flow.SequenceFlow{},
		Properties:  p.Properties(),
	}

	seExists := false
	eeExists := false

	for _, n := range p.Nodes() {
		s.Nodes[n.Id()] = n

		// check events
		if n.NodeType() == flow.EventNodeType {
			en, ok := n.(flow.EventNode)
			if !ok {
				return nil,
					errs.New(
						errs.M("failed to convert to EventNode"),
						errs.C(errorClass, errs.TypeCastingError),
						errs.D("node_id", n.Id()),
						errs.D("node_name", n.Name()))
			}

			switch en.EventClass() {
			case flow.StartEventClass:
				seExists = true

			case flow.IntermediateEventClass:
				break

			case flow.EndEventClass:
				eeExists = true
			}
		}

		// by BPMN demands that, if there is an EndEvent in the process, there should
		// be at least one StartEvent
		if eeExists && !seExists {
			return nil,
				errs.New(
					errs.M("no StartEvent in process with an EndEvent"))
		}
	}

	for _, f := range p.Flows() {
		s.Flows[f.Id()] = f
	}

	return &s, nil
}
