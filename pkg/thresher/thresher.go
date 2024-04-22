// Process Initiator is built from Process object. If there are any building errors
// then no Initiator is created.
// While creation a list of process initiation events is built. After creation
// Process Initiator takes Ready state and awaits for initial events to run an
// Process Instance.
//
// After receiving initial event, Process Initiator creates a new Process
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

// eventProc holds single link from to event
type eventProc struct {
	// proc is empty for the initial events and new Instance should be created
	// once Instance created, it registered itself as EventProcessor with all
	// awaited events, including initial ones.
	//
	// if proc isn't empty it just processed its copy of eventDefinition.
	proc exec.EventProcessor

	// processId holds the Id of the process. It used when proc is empty
	// and Thresher should find the appropriate Snapshot to start an
	// Instance of the Process.
	processId string
}

type instanceReg struct {
	stop context.CancelFunc
	inst *instance.Instance
}

type Thresher struct {
	m sync.Mutex

	// events holds all registered events either initial or for running
	// instances.
	// events is indexed by event definition ID.
	events map[string][]eventProc

	// snapshots is indexed by the ProcessID
	snapshots map[string]*exec.Snapshot

	instances map[string][]instanceReg
}

func New() *Thresher {
	return &Thresher{
		// registration holds all event registrations for
		// registered processeses.
		events: map[string][]eventProc{},

		// instances is processes' instancess running or finished.
		instances: map[string][]instanceReg{},
	}
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

	t.m.Lock()
	defer t.m.Unlock()

	for _, ed := range eDefs {
		pp, ok := t.events[ed.Id()]
		if !ok {
			t.events[ed.Id()] = []eventProc{
				{
					proc: ep,
				}}

			continue
		}

		if !hasEventProc(pp, ep) {
			pp = append(pp, eventProc{
				proc: ep,
			})
		}

		t.events[ed.Id()] = pp
	}

	return nil
}

// --------------- exec.Runner interface ---------------------------------------

func (t *Thresher) RegisterProcess(
	s *exec.Snapshot,
) error {
	if s == nil {
		return errs.New(
			errs.M("empty snapshot"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	events := make([]flow.EventDefinition, 0, len(s.InitEvents))
	for _, e := range s.InitEvents {
		for _, ed := range e.Definitions() {
			events = append(events, ed)
		}
	}

	inst, err := instance.New(s, nil, events...)
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

// addInitialEvent links initial event edd with Process processId.
func (t *Thresher) addInitialEvent(
	processId string,
	edd ...flow.EventDefinition,
) {
	for _, ed := range edd {
		pp, ok := t.events[ed.Id()]
		if !ok {
			t.events[ed.Id()] = []eventProc{
				{
					proc:      nil,
					processId: processId,
				}}

			continue
		}

		for _, ep := range pp {
			if ep.processId == processId {
				continue
			}
		}

		pp = append(pp, eventProc{
			proc:      nil,
			processId: processId,
		})

		t.events[ed.Id()] = pp
	}
}

// hasEventProc checks if eventProcessor exists in eventProc slince pp.
func hasEventProc(pp []eventProc, ep exec.EventProcessor) bool {
	for _, p := range pp {
		if p.proc == ep {
			return true
		}
	}

	return false
}
