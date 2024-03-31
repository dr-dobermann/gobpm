package exec

import (
	"errors"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// Snapshot holds process'es snapshot ready to run.
type Snapshot struct {
	foundation.BaseElement

	ProcessId string
	Nodes     map[string]flow.Node
	Flows     map[string]*flow.SequenceFlow
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

	be, err := foundation.NewBaseElement(snapOpts...)
	if err != nil {
		return nil, err
	}

	s := Snapshot{
		BaseElement: *be,
		ProcessId:   p.Id(),
		Nodes:       map[string]flow.Node{},
		Flows:       map[string]*flow.SequenceFlow{},
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
						"node %q(%s) does not implement Nodetor interface",
						n.Name(), n.Id()),
					errs.C(errorClass, errs.TypeCastingError)))

			continue
		}

		s.Nodes[n.Id()] = n
	}

	for _, f := range p.Flows() {
		s.Flows[f.Id()] = f
	}

	if len(ee) != 0 {
		return nil, errors.Join(ee...)
	}

	return s, nil
}
