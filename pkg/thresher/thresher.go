// Process Initiator is built from Process object. If there are any building errors
// then no Initiator is created.
// While creation a list of process initiation events is built. After creation
// Process Initiator takes Ready state and awaits for initial events to run an
// Process Instance.
//
// After recieving initial event, Process Initiator creates a new Process
// Instance with data from initial event.
//
// Instance consists of nodes and flows and runtime environment.

// For every entry node creates a separate token track which is runs in single
// go-routine.
// Entry node is the node which has no incoming sequence flow.
// Every node has an Executor which configures by node model data.
// Node could implement Prologue and Epilogue interfaces for right node execution
// setup and finish.
//
// Every node execution parameters and results are stored to Instance History.
// Saved History could be used as an Input for new Instance run.
//
// Instance runtime environment (IRE) holds Data Scope object which is holds actual
// accessible data objects: Properties, DataObjects, ...
// Scope could dinamically expand and shring according to executing nodes.
// Scope tracks data objects updates and generates appropriate notification events.
//
// IRE also have instance's Event Processor.
// Event Processor accept all external and internal events and process them
// according to their types.
// Event Processor supports Message Correlation for incoming and outgoing
// insance Messages.

package thresher

import (
	"context"
	"sync"

	"github.com/dr-dobermann/gobpm/internal/exec"
	"github.com/dr-dobermann/gobpm/internal/instance"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

const errorClass = "THRESHER_ERRORS"

type eventReg struct {
	proc  exec.EventProcessor
	eDefs map[string]flow.EventDefinition
}

type instanceReg struct {
	stop context.CancelFunc
	inst *instance.Instance
}

type Thresher struct {
	m sync.Mutex
	// registrations is indexed by processId.
	registrations map[string]eventReg

	instances map[string][]instanceReg
}

// ------------------ EventProducer interface ----------------------------------

// RegisterEvents registered eventDefinition and its processor in the Thresher.
func (t *Thresher) RegisterEvents(
	ep exec.EventProcessor,
	eDefs ...flow.EventDefinition,
) error {
	if ep == nil {
		return errs.New(
			errs.M("empty event processor"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if len(eDefs) == 0 {
		return errs.New(
			errs.M("empty event definitions list"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	_, ok := t.registrations[ep.Id()]
	if !ok {
		t.registrations[ep.Id()] = eventReg{
			proc:  ep,
			eDefs: map[string]flow.EventDefinition{},
		}
	}

	for _, d := range eDefs {
		t.registrations[ep.Id()].eDefs[d.Id()] = d
	}

	return nil
}

// --------------- exec.Runner interface ---------------------------------------

func (t *Thresher) RunProcess(
	s *exec.Snapshot,
	event ...flow.EventDefinition,
) error {
	if s == nil {
		return errs.New(
			errs.M("empty snapshot"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	inst, err := instance.New(s, event...)
	if err != nil {
		return err
	}

	t.m.Lock()
	defer t.m.Unlock()

	ii, ok := t.instances[s.ProcessId]
	if !ok {
		ii = []instanceReg{}
	}

	ctx, cancel := context.WithCancel(context.Background())
	err = inst.Run(ctx, cancel, t)
	if err != nil {
		return err
	}

	t.instances[s.ProcessId] = append(ii,
		instanceReg{
			stop: cancel,
			inst: inst,
		})

	return nil
}

// -----------------------------------------------------------------------------
