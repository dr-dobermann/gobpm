package instance

import (
	"context"

	"github.com/dr-dobermann/gobpm/internal/exec"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

const errorClass = "INSTANCE_ERROR"

type Instance struct {
	s *exec.Snapshot

	startEventNode flow.EventNode
	eventDef       flow.EventDefinition

	// Scopes holds accessible in the moment Data.
	// first map indexed by data path, the second map indexed by Data name.
	scopes map[exec.DataPath]map[string]data.Data

	// Event registrator and emitter
	eProd exec.EventProducer
}

func New(
	s *exec.Snapshot,
	start flow.EventNode,
	eDef flow.EventDefinition,
) (*Instance, error) {
	inst := Instance{
		s:              s,
		startEventNode: start,
		eventDef:       eDef,
		scopes:         map[exec.DataPath]map[string]data.Data{},
	}

	return &inst, nil
}

// -------------------- Scope interface ----------------------------------------

// GetData returns data value name from scope path.
func (inst *Instance) GetData(
	path exec.DataPath,
	name string,
) (data.Value, error) {
	s, ok := inst.scopes[path]
	if !ok {
		return nil,
			errs.New(
				errs.M("couldn't find scope %q", path),
				errs.C(errorClass, errs.ObjectNotFound))
	}

	d, ok := s[name]
	if !ok {
		return nil,
			errs.New(
				errs.M("data %q isn't found on scope %q", name, path),
				errs.C(errorClass, errs.ObjectNotFound))
	}

	return d.Value(), nil
}

// -----------------------------------------------------------------------------

func (inst *Instance) Run(
	ctx context.Context,
	cancel context.CancelFunc,
	ep exec.EventProducer,
) error {
	inst.eProd = ep

	defer cancel()

	return nil
}
