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

// Package thresher provides the main BPMN process execution engine.
package thresher

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/internal/eventproc/eventhub"
	"github.com/dr-dobermann/gobpm/internal/instance"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/runner"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

const (
	errorClass = "THRESHER_ERRORS"

	defaultThresherID = "MegaThresher"
)

// State of the Thresher.
type State uint8

const (
	// Invalid represents an invalid thresher state.
	Invalid State = iota
	// NotStarted represents a thresher that has not been started.
	NotStarted
	// Started represents a thresher that has been started.
	Started
	// Paused represents a thresher that has been paused.
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

// instanceReg holds single Instance registration.
type instanceReg struct {
	stop context.CancelFunc
	inst *instance.Instance
}

// Thresher represents the main BPMN process execution engine.
type Thresher struct {
	ctx       context.Context
	eventHub  eventproc.EventHub
	snapshots map[string]*snapshot.Snapshot
	instances map[string]instanceReg
	id        string
	m         sync.Mutex
	state     State
}

// New creates a new empty Thresher in NotStarted state.
// Function only initializes inner structures. To run Thresher, Run method
// should be called.
func New(id string) (*Thresher, error) {
	eh, err := eventhub.New()
	if err != nil {
		return nil,
			errs.New(
				errs.M("eventHub building failed"),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(err))
	}

	id = strings.TrimSpace(id)
	if id == "" {
		id = defaultThresherID
	}

	return &Thresher{
			id:        id,
			state:     NotStarted,
			snapshots: map[string]*snapshot.Snapshot{},
			instances: map[string]instanceReg{},
			eventHub:  eh,
		},
		nil
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

	// Run eventhub in background
	go func() {
		_ = t.eventHub.Run(ctx)
	}()

	// Give eventhub a moment to initialize
	time.Sleep(1 * time.Millisecond)

	err := t.UpdateState(Started)
	if err != nil {
		return errs.New(
			errs.M("couldn't update Thresher state"),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err))
	}

	return nil
}

// ------------------ EventProducer interface ----------------------------------

// RegisterEvent registered eventDefinition and its processor in the Thresher.
func (t *Thresher) RegisterEvent(
	ep eventproc.EventProcessor,
	eDef flow.EventDefinition,
) error {
	if ep == nil {
		return errs.New(
			errs.M("empty event processor"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if t.State() != Started {
		return errs.New(
			errs.M("thresher is not started"),
			errs.C(errorClass, errs.InvalidState))
	}

	return t.eventHub.RegisterEvent(ep, eDef)
}

// UnregisterEvent removes link EventDefinition to EventProcessor from
// EventProducer.
func (t *Thresher) UnregisterEvent(
	ep eventproc.EventProcessor,
	eDefID string,
) error {
	if ep == nil {
		return errs.New(
			errs.M("empty event processor"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if t.State() != Started {
		return errs.New(
			errs.M("thresher is not started"),
			errs.C(errorClass, errs.InvalidState))
	}

	return t.eventHub.UnregisterEvent(ep, eDefID)
}

// PropagateEvent sends a fired throw event's eventDefinition
// up to chain of EventProducers
func (t *Thresher) PropagateEvent(
	ctx context.Context,
	eDef flow.EventDefinition,
) error {
	st := t.State()
	if st != Started {
		return errs.New(
			errs.M("thresher is not started"),
			errs.C(errorClass, errs.InvalidState))
	}

	if eDef == nil {
		return errs.New(
			errs.M("empty event definition"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if err := t.eventHub.PropagateEvent(ctx, eDef); err != nil {
		return errs.New(
			errs.M("event propagation failed"),
			errs.C(errorClass, errs.OperationFailed),
			errs.D("event_definition_id", eDef.ID()),
			errs.D("event_definition_type", eDef.Type()),
			errs.E(err))
	}

	return nil
}

// --------------- exec.Runner interface ---------------------------------------

// RegisterProcess registers a process directly, creating snapshot internally
func (t *Thresher) RegisterProcess(
	p *process.Process,
) error {
	if p == nil {
		return errs.New(
			errs.M("empty process"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	// Create snapshot from process
	s, err := snapshot.New(p)
	if err != nil {
		return errs.New(
			errs.M("failed to create snapshot from process"),
			errs.C(errorClass, errs.BulidingFailed),
			errs.E(err))
	}

	t.m.Lock()
	defer t.m.Unlock()

	if _, ok := t.snapshots[s.ProcessID]; !ok {
		t.snapshots[s.ProcessID] = s
	}

	return nil
}

// StartProcess runs process with processId without any event even if
// process awaits them.
func (t *Thresher) StartProcess(processID string) error {
	if t.state != Started {
		return errs.New(
			errs.M("thresher isn't started"),
			errs.C(errorClass, errs.InvalidState),
			errs.D("current_state", t.state.String()))
	}

	t.m.Lock()
	defer t.m.Unlock()

	s, ok := t.snapshots[processID]
	if !ok {
		return errs.New(
			errs.M("couldn't find snapshot for process ID %q", processID),
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
				s.ProcessID),
			errs.C(errorClass, errs.BulidingFailed),
			errs.E(err))
	}

	ctx, cancel := context.WithCancel(t.ctx)
	defer cancel()
	if err := inst.Run(ctx); err != nil {
		return errs.New(
			errs.M("inctance %q of process %q failed to run",
				inst.ID(), s.ProcessID),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err))
	}

	t.m.Lock()
	defer t.m.Unlock()

	t.instances[inst.ID()] = instanceReg{
		stop: cancel,
		inst: inst,
	}

	return nil
}

// =============================================================================
// Interface implementation check

var (
	_ eventproc.EventProducer = (*Thresher)(nil)
	_ runner.ProcessRunner    = (*Thresher)(nil)
)
