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
// Instance runtime environment (IRE) holds Data Scope objects which holds actual
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

	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/internal/instance"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/runner"
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

// eDefReg holds single link from to event definition to ProcessID and
// Instance (EventProcessor).
type eDefReg struct {
	// proc is empty for the initial events.
	//
	// pros isn't emepty for Intermediate events. When Instance reach the
	// EventNode, it let node to register the event defintions it awaits and
	// put this Instance track in waiting state.
	//
	// Intermediate event is also registered for boundary events or in-process
	// subprocesses.
	proc eventproc.EventProcessor

	// ProcessId holds the Id of the process. It's used when proc is empty
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
	snapshots map[string]*snapshot.Snapshot

	// instances holds process instances in any state.
	// instances is indexed by instance ID.
	instances map[string]instanceReg
}

// New creates a new empty Thresher in NotStarted state.
// Function only initializes inner structures. To run Thresher, Run method
// should be called.
func New() *Thresher {
	return &Thresher{
		state:     NotStarted,
		eDefs:     map[string][]eDefReg{},
		events:    []eventReg{},
		snapshots: map[string]*snapshot.Snapshot{},
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

// Run starts Thresher event queue processing.
func (t *Thresher) Run(ctx context.Context) error {
	st := t.State()
	if st != NotStarted {
		return errs.New(
			errs.M("couldn't start thresher from state %q (should be in NotStarted)", st),
			errs.C(errorClass, errs.InvalidState))
	}

	if ctx == nil {
		return errs.New(
			errs.M("empty context"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	t.ctx = ctx

	err := t.runEventQueue()
	if err == nil {
		if err := t.UpdateState(Started); err != nil {
			return errs.New(
				errs.M("couldnt update Thresher state"),
				errs.C(errorClass, errs.OperationFailed),
				errs.E(err))
		}
	}

	return err
}

// runEventQueue starts the Thresher events queue processing.
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

			// take first event in the queue and process all registrations for
			// its event definition.
			eRegs, ok := t.eDefs[t.events[0].eDef.Id()]
			if ok {
				for _, er := range eRegs {
					// if it's intermediate event, send eDef to its processor.
					if er.proc != nil {
						go func(
							proc eventproc.EventProcessor,
							eDef flow.EventDefinition,
						) {
							_ = proc.ProcessEvent(t.ctx, eDef)
						}(er.proc, t.events[0].eDef)
					}
				}
			}

			t.events = t.events[1:]

			t.m.Unlock()
		}
	}(t.ctx)

	return nil
}

// addEvent adds singe event definition into the Thresher event queue.
// if eDef is registered as initial event for the process,
// new process instance would be started to add its as a instance avaits
// of this event definition.
func (t *Thresher) addEvent(eDef flow.EventDefinition) error {
	t.m.Lock()
	ree := t.eDefs[eDef.Id()]
	t.m.Unlock()

	ee := []error{}

	for _, re := range ree {
		if re.proc != nil {
			continue
		}

		t.m.Lock()
		s, ok := t.snapshots[re.ProcessId]
		t.m.Unlock()
		if !ok {
			ee = append(ee, errs.New(
				errs.M("no registration for process #%s", re.ProcessId),
				errs.C(errorClass, errs.ObjectNotFound)))
		}

		if err := t.launchInstance(s); err != nil {
			ee = append(ee, err)
		}
	}

	if len(ee) != 0 {
		return errors.Join(ee...)
	}

	t.m.Lock()
	t.events = append(t.events, eventReg{
		eDef: eDef,
	})
	t.m.Unlock()

	return nil
}

// ------------------ EventProducer interface ----------------------------------

// RegisterEvents registered eventDefinition and its processor in the Thresher.
func (t *Thresher) RegisterEvents(
	ep eventproc.EventProcessor,
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
				},
			}

			continue
		}

		if indexEventProc(pp, ep) != -1 {
			pp = append(pp, eDefReg{
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
	ep eventproc.EventProcessor,
	eDefIds ...string,
) error {
	if ep == nil {
		return errs.New(
			errs.M("empty event processor"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	t.m.Lock()
	defer t.m.Unlock()

	for _, ed := range eDefIds {
		pp, ok := t.eDefs[ed]
		if !ok {
			return nil
		}

		for i := 0; i < len(pp); {
			if pp[i].proc.Id() == ep.Id() {
				pp = append(pp[:i], pp[i+1:]...)

				continue
			}

			i++
		}

		if len(pp) == 0 {
			delete(t.eDefs, ed)

			continue
		}

		t.eDefs[ed] = pp
	}

	return nil
}

// UnregisterProcessor unregister all event definitions registered by
// the EventProcessor.
func (t *Thresher) UnregisterProcessor(ep eventproc.EventProcessor) error {
	if ep == nil {
		return errs.New(
			errs.M("empty EventProcessor isn't allowed"),
			errs.C(errorClass, errs.InvalidParameter))
	}

	t.m.Lock()
	defer t.m.Unlock()

	for edId, pp := range t.eDefs {
		for i := 0; i < len(pp); {
			if pp[i].proc.Id() == ep.Id() {
				pp = append(pp[:i], pp[i+1:]...)

				continue
			}

			i++
		}

		t.eDefs[edId] = pp
	}

	return nil
}

// PropagateEvents gets a list of eventDefinitions and sends them to all
// EventProcessors registered for this type of EventDefinition.
func (t *Thresher) PropagateEvents(events ...flow.EventDefinition) error {
	if st := t.State(); st != Started {
		return errs.New(
			errs.M("thresher isn't started"),
			errs.C(errorClass, errs.InvalidState))
	}

	ee := []error{}

	for _, ed := range events {
		if ed == nil {
			ee = append(ee,
				errs.New(
					errs.M("empty event definition"),
					errs.C(errorClass, errs.EmptyNotAllowed)))
			continue
		}

		if err := t.addEvent(ed); err != nil {
			ee = append(ee,
				errs.New(
					errs.M("failed to add event %q[%s]", ed.Id(), ed.Type()),
					errs.C(errorClass, errs.OperationFailed),
					errs.E(err)))
		}
	}

	if len(ee) != 0 {
		return errs.New(
			errs.M("event emitting failed"),
			errs.C(errorClass),
			errs.E(errors.Join(ee...)))
	}

	return nil
}

// --------------- exec.Runner interface ---------------------------------------

// RegisterProcess registers a process snapshot to start Instances on
// initial event firing
func (t *Thresher) RegisterProcess(
	s *snapshot.Snapshot,
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

// launchInstance creates a new Instance from the Snapshot s, runs it and
// append it to runned insances of the Thresher.
func (t *Thresher) launchInstance(s *snapshot.Snapshot) error {
	inst, err := instance.New(s, nil, t, nil, nil)
	if err != nil {
		return errs.New(
			errs.M("couldn't create an Instance for process %q",
				s.ProcessId),
			errs.C(errorClass, errs.BulidingFailed),
			errs.E(err))
	}

	ctx, cancel := context.WithCancel(t.ctx)
	defer cancel()
	if err := inst.Run(ctx); err != nil {
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
			t.eDefs[ed.Id()] = []eDefReg{
				{
					ProcessId: processId,
				},
			}

			continue
		}

		for _, ep := range pp {
			if ep.ProcessId == processId {
				continue
			}
		}

		pp = append(pp, eDefReg{
			ProcessId: processId,
		})

		t.eDefs[ed.Id()] = pp
	}
}

// -----------------------------------------------------------------------------

// indexEventProc looks for the eventProcessor ep in eventProc slice pp and
// return its index. If there is no ep in pp, -1 returned.
func indexEventProc(pp []eDefReg, ep eventproc.EventProcessor) int {
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
	_ eventproc.EventProducer = (*Thresher)(nil)
	_ runner.ProcessRunner    = (*Thresher)(nil)
)
