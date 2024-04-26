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

// eDefReg holds single link from to event definition to Snapshot or
// EventProcessor.
type eDefReg struct {
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

// instanceReg holds single Instance registration.
type instanceReg struct {
	stop context.CancelFunc
	inst *instance.Instance
}

// eventReg holds single active event information.
// DEV_NOTE: I'm not sure is it necessary to send back the results of
//
//	event processing. If so, there should be some callback or
//	channel to accept the results.
//	For now I leave the struct with one field.
type eventReg struct {
	eDef flow.EventDefinition
}

type Thresher struct {
	m sync.Mutex

	state State

	ctx context.Context

	// eDefs holds all registered eDefs either initial or for running
	// instances.
	// eDefs is indexed by event definition ID.
	eDefs map[string][]eDefReg

	// events holds an event queue.
	events []eventReg

	// snapshots is indexed by the ProcessID
	snapshots map[string]*exec.Snapshot

	// instances holds process instances in any state.
	// instances is indexed by instance ID.
	instances map[string]instanceReg
}

func New() *Thresher {
	return &Thresher{
		state:     NotStarted,
		eDefs:     map[string][]eDefReg{},
		events:    []eventReg{},
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

func (t *Thresher) Run(ctx context.Context) error {
	st := t.State()
	if st != NotStarted {
		return errs.New(
			errs.M("couldn't start thresher from state %q (should be in NotStarted)", st),
			errs.C(errorClass, errs.InvalidState))
	}

	if ctx == nil {
		ctx = context.Background()
	}

	t.ctx = ctx

	err := t.runEventQueue()
	if err == nil {
		t.UpdateState(Started)
	}

	return err
}

func (t *Thresher) runEventQueue() error {
	go func(ctx context.Context) {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				if st := t.State(); st != Started && st != Paused {
					return
				}
			}

			t.m.Lock()
			if len(t.events) == 0 {
				t.m.Unlock()
				continue
			}

			// take first event in the queue and get all registrations for
			// its event definition.
			eRegs, ok := t.eDefs[t.events[0].eDef.Id()]
			if ok {
				for _, er := range eRegs {
					if er.proc != nil {
						go func(eDef flow.EventDefinition) {
							_ = er.proc.ProcessEvent(t.ctx, eDef)
						}(t.events[0].eDef)
					}
				}
			}

			t.events = t.events[1:]

			t.m.Unlock()
		}

	}(t.ctx)

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
		pp, ok := t.eDefs[ed.Id()]
		if !ok {
			t.eDefs[ed.Id()] = []eDefReg{
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

		t.eDefs[ed.Id()] = pp
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
		pp, ok := t.eDefs[ed.Id()]
		if !ok {
			return nil
		}

		if idx := indexEventProc(pp, ep); idx != -1 {
			pp = append(pp[:idx], pp[idx+1:]...)
		}

		if len(pp) == 0 {
			delete(t.eDefs, ed.Id())

			continue
		}

		t.eDefs[ed.Id()] = pp
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

	if t.state != Started {
		return errs.New(
			errs.M("thresher isn't started"),
			errs.C(errorClass, errs.InvalidState),
			errs.D("current_state", t.state.String()))
	}

	t.m.Lock()
	defer t.m.Unlock()

	s, ok := t.snapshots[processId]
	if !ok {
		return errs.New(
			errs.M("couldn't find snapshot for process ID %q", processId),
			errs.C(errorClass, errs.ObjectNotFound))
	}

	return t.launchInstance(s)
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
	rr, ok := t.eDefs[eDef.Id()]
	t.m.Unlock()

	if !ok {
		return errs.New(
			errs.M("event defintion %q isn't registered", eDef.Id()),
			errs.C(errorClass, errs.InvalidObject))
	}

	ee := []error{}

	for _, r := range rr {
		if r.proc == nil {
			if err := t.StartProcess(r.ProcessId); err != nil {
				ee = append(ee, err)
			}
		}
	}

	if len(ee) != 0 {
		return errors.Join(ee...)
	}

	return nil
}

// launchInstance creates a new Instance from the Snapshot s, runs it and
// append it to runned insances of the Thresher.
func (t *Thresher) launchInstance(s *exec.Snapshot) error {
	inst, err := instance.New(s, nil, t)
	if err != nil {
		return errs.New(
			errs.M("couldn't create an Instance for process %q",
				s.ProcessId),
			errs.C(errorClass, errs.BulidingFailed),
			errs.E(err))
	}

	ctx, cancel := context.WithCancel(t.ctx)
	if err := inst.Run(ctx, cancel, t); err != nil {
		return errs.New(
			errs.M("inctance %q of process %q failed to run",
				inst.Id(), s.ProcessId),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err))
	}

	t.m.Lock()
	defer t.m.Unlock()

	t.instances[inst.Id()] = instanceReg{
		stop: cancel,
		inst: inst,
	}

	return nil
}

// addInitialEvent links initial event edd with Process processId.
func (t *Thresher) addInitialEvent(
	processId string,
	edd ...flow.EventDefinition,
) {
	for _, ed := range edd {
		pp, ok := t.eDefs[ed.Id()]
		if !ok {
			t.eDefs[ed.Id()] = []eventReg{
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

		t.eDefs[ed.Id()] = pp
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
