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
	"errors"
	"sync"

	"github.com/dr-dobermann/gobpm/internal/exec"
	"github.com/dr-dobermann/gobpm/internal/instance"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

const errorClass = "THRESHER_ERRORS"

// =============================================================================

// State of the Thresher.
type State uint8

const (
	Invalid State = iota
	NotStarted
	Started
	Paused
)

// Validate checks State to be valid.
func (s State) Validate() error {
	if s > Paused {
		return errs.New(
			errs.M("invalid thresher state %d", uint8(s)))
	}

	return nil
}

// String implement Stringer interface for the State. If s is invalid, then
// error message returned as State name.
func (s State) String() string {
	if err := s.Validate(); err != nil {
		return err.Error()
	}

	return []string{
		"Invalid",
		"NotStarted ",
		"Started",
		"Paused",
	}[s]
}

// =============================================================================

// eventReg holds single link from to event definition to Snapshot or
// EventProcessor.
type eventReg struct {
	// proc is empty for the initial events and new Instance should be created
	// once Instance created, it registered itself as EventProcessor with all
	// awaited events, including initial ones.
	//
	// if proc isn't empty it just processed its copy of eventDefinition.
	proc exec.EventProcessor

	// ProcessId holds the Id of the process. It used when proc is empty
	// and Thresher should find the appropriate Snapshot to start an
	// Instance of the Process.
	ProcessId string
}

type instanceReg struct {
	stop context.CancelFunc
	inst *instance.Instance
}

type Thresher struct {
	m sync.Mutex

	state State

	ctx context.Context

	// events holds all registered events either initial or for running
	// instances.
	// events is indexed by event definition ID.
	events map[string][]eventReg

	// snapshots is indexed by the ProcessID
	snapshots map[string]*exec.Snapshot

	// instances holds process instances in any state.
	// instances is indexed by instance ID.
	instances map[string]instanceReg
}

func New(ctx context.Context) *Thresher {
	if ctx == nil {
		ctx = context.Background()
	}

	return &Thresher{
		state:     Started,
		ctx:       ctx,
		events:    map[string][]eventReg{},
		snapshots: map[string]*exec.Snapshot{},
		instances: map[string]instanceReg{},
	}
}

// State returns current state of the Threasher.
func (t *Thresher) State() State {
	t.m.Lock()
	defer t.m.Unlock()

	return t.state
}

// UpdateState sets new State ns for the Threasher if there is no any error.
func (t *Thresher) UpdateState(ns State) error {
	t.m.Lock()
	defer t.m.Unlock()

	if err := ns.Validate(); err != nil {
		return errs.New(
			errs.M("couldn't set new state %q of the Thresher", ns.String()),
			errs.C(errorClass, errs.InvalidState),
			errs.E(err))
	}

	t.state = ns

	return nil
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

	t.m.Lock()
	defer t.m.Unlock()

	for _, ed := range eDefs {
		pp, ok := t.events[ed.Id()]
		if !ok {
			t.events[ed.Id()] = []eventReg{
				{
					proc: ep,
				}}

			continue
		}

		if indexEventProc(pp, ep) != -1 {
			pp = append(pp, eventReg{
				proc: ep,
			})
		}

		t.events[ed.Id()] = pp
	}

	return nil
}

// UnregisterEvents removes event definition to EventProcessor link from
// EventProducer.
func (t *Thresher) UnregisterEvents(
	ep exec.EventProcessor,
	eDefs ...flow.EventDefinition,
) error {
	if ep == nil {
		return errs.New(
			errs.M("empty event processor"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	t.m.Lock()
	defer t.m.Unlock()

	for _, ed := range eDefs {
		pp, ok := t.events[ed.Id()]
		if !ok {
			return nil
		}

		if idx := indexEventProc(pp, ep); idx != -1 {
			pp = append(pp[:idx], pp[idx+1:]...)
		}

		if len(pp) == 0 {
			delete(t.events, ed.Id())

			continue
		}

		t.events[ed.Id()] = pp
	}

	return nil
}

// --------------- exec.Runner interface ---------------------------------------

// RegisterProcess registers a process snapshot to start Instances on
// initial event firing
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
		events = append(events, e.Definitions()...)
	}

	t.m.Lock()
	defer t.m.Unlock()

	if _, ok := t.snapshots[s.ProcessId]; !ok {
		t.snapshots[s.ProcessId] = s

		t.addInitialEvent(s.ProcessId, events...)
	}

	return nil
}

// StartProcess runs process with processId without any event even if
// process awaits them.
func (t *Thresher) StartProcess(processId string) error {
	t.m.Lock()
	defer t.m.Unlock()

	if t.state != Started {
		return errs.New(
			errs.M("thresher isn't started"),
			errs.C(errorClass, errs.InvalidState),
			errs.D("current_state", t.state.String()))
	}

	s, ok := t.snapshots[processId]
	if !ok {
		return errs.New(
			errs.M("couldn't find snapshot for process ID %q", processId),
			errs.C(errorClass, errs.ObjectNotFound))
	}

	inst, err := instance.New(s, nil, t)
	if err != nil {
		return errs.New(
			errs.M("couldn't create an Instance for process %q",
				processId),
			errs.C(errorClass, errs.BulidingFailed),
			errs.E(err))
	}

	ctx, cancel := context.WithCancel(t.ctx)
	if err := inst.Run(ctx, cancel, t); err != nil {
		return errs.New(
			errs.M("inctance %q running failed", inst.Id()),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err))
	}

	t.instances[inst.Id()] = instanceReg{
		stop: cancel,
		inst: inst,
	}

	return nil
}

// ProcessEvent processes single eventDefinition and if there is any
// registration of event definition with eDef ID, it starts a new Instance
// or send the event to runned Instance.
func (t *Thresher) ProcessEvent(eDef flow.EventDefinition) error {
	if eDef == nil {
		return errs.New(
			errs.M("no event definition"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	t.m.Lock()
	defer t.m.Unlock()

	rr, ok := t.events[eDef.Id()]
	if !ok {
		return errs.New(
			errs.M("event defintion %q isn't registered", eDef.Id()),
			errs.C(errorClass, errs.InvalidObject))
	}

	var (
		ee []error
		wg sync.WaitGroup
		m  sync.Mutex
	)

	for _, r := range rr {
		go func(er eventReg) {
			wg.Add(1)
			defer wg.Done()

			if r.proc == nil {
				err := t.StartProcess(r.ProcessId)
			}
		}(r)
	}

	wg.Wait()

	if len(ee) != 0 {
		return errors.Join(ee...)
	}

	return nil
}

// addInitialEvent links initial event edd with Process processId.
func (t *Thresher) addInitialEvent(
	processId string,
	edd ...flow.EventDefinition,
) {
	for _, ed := range edd {
		pp, ok := t.events[ed.Id()]
		if !ok {
			t.events[ed.Id()] = []eventReg{
				{
					proc:      nil,
					ProcessId: processId,
				}}

			continue
		}

		for _, ep := range pp {
			if ep.ProcessId == processId {
				continue
			}
		}

		pp = append(pp, eventReg{
			proc:      nil,
			ProcessId: processId,
		})

		t.events[ed.Id()] = pp
	}
}

// -----------------------------------------------------------------------------

// indexEventProc looks for the eventProcessor ep in eventProc slice pp and
// return its index. If there is no ep in pp, -1 returned.
func indexEventProc(pp []eventReg, ep exec.EventProcessor) int {
	for i, p := range pp {
		if p.proc == ep {
			return i
		}
	}

	return -1
}

// =============================================================================
// Interface implementation check

var (
	_ exec.EventProducer = (*Thresher)(nil)
	_ exec.ProcessRunner = (*Thresher)(nil)
)
