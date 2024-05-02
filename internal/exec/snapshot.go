package exec

import (
	"errors"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// Snapshot holds process'es snapshot ready to run.
type Snapshot struct {
	foundation.ID

	ProcessId   string
	ProcessName string
	Nodes       map[string]flow.Node
	Flows       map[string]*flow.SequenceFlow
	Properties  []*data.Property
	InitEvents  []flow.EventNode
}

// NewSnapshot creates a new snapshot from the Process p and returns its
// pointer on success or error on failure.
func NewSnapshot(
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
		InitEvents:  []flow.EventNode{},
	}

	return createSnapshot(&s, p)
}

// createSnapshot creates a snapshot of the process and retruns its pointer.
// If any errors found, then error returned.
func createSnapshot(s *Snapshot, p *process.Process) (*Snapshot, error) {
	ee := []error{}

	for _, n := range p.Nodes() {
		if _, ok := n.(NodeExecutor); !ok {
			ee = append(ee,
				errs.New(
					errs.M(
						"node %q(%s) does not implement NodeExecutor interface",
						n.Name(), n.Id()),
					errs.C(errorClass, errs.TypeCastingError)))

			continue
		}

		s.Nodes[n.Id()] = n

		// find initial events
		if en, ok := n.(flow.EventNode); ok {
			_, bounded := en.(flow.BoudaryEvent)
			if len(en.Incoming()) == 0 && !bounded {
				s.InitEvents = append(s.InitEvents, en)
			}
		}
	}

	for _, f := range p.Flows() {
		s.Flows[f.Id()] = f
	}

	if len(ee) != 0 {
		return nil, errs.New(
			errs.M("process snapshot creation failed"),
			errs.C(errorClass, errs.BulidingFailed),
			errs.E(errors.Join(ee...)))
	}

	return s, nil
}
