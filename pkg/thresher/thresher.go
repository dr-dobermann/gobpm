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
// Node execution is a single Execute step (the track loads incoming data, runs
// the node's Exec, and uploads outgoing data).
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
	"fmt"
	"strings"
	"sync"

	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/internal/eventproc/eventhub"
	"github.com/dr-dobermann/gobpm/internal/instance"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
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
	cfg       thresherConfig
	eventHub  eventproc.EventHub
	snapshots map[string]*snapshot.Snapshot
	instances map[string]instanceReg
	starters  map[string][]*instanceStarter
	id        string
	m         sync.Mutex
	state     State
}

// New creates a new empty Thresher in NotStarted state. Engine-level extensions
// default to their bundled core implementations; each WithXxx option overrides
// one (a zero-option New produces a fully working engine — no NewDefault).
// Function only initializes inner structures. To run Thresher, Run method
// should be called.
func New(id string, opts ...Option) (*Thresher, error) {
	cfg := defaultConfig()
	for _, o := range opts {
		if err := o(&cfg); err != nil {
			return nil,
				errs.New(
					errs.M("invalid thresher option"),
					errs.C(errorClass, errs.InvalidParameter),
					errs.E(err))
		}
	}

	id = strings.TrimSpace(id)
	if id == "" {
		id = defaultThresherID
	}

	t := &Thresher{
		id:        id,
		cfg:       cfg,
		state:     NotStarted,
		snapshots: map[string]*snapshot.Snapshot{},
		instances: map[string]instanceReg{},
		starters:  map[string][]*instanceStarter{},
	}

	// The EventHub receives the engine's resolved runtime (&t.cfg implements
	// renv.EngineRuntime) so the waiters it builds reach Clock / ExpressionEngine
	// (ADR-002 §4.3, Solution B). Built after t so it shares t's cfg pointer.
	eh, err := eventhub.New(&t.cfg)
	if err != nil {
		return nil,
			errs.New(
				errs.M("eventHub building failed"),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(err))
	}

	t.eventHub = eh

	t.logStartupConfig()

	return t, nil
}

// logStartupConfig prints the engine banner, build metadata and every resolved
// engine-level extension on its own INFO line, so the full wiring is readable at
// startup instead of crammed into one dense record (ADR-002 v.2 §4.4.1). The
// banner and the configuration dump are each suppressible (WithoutBanner /
// WithoutStartupConfig); the closing separator prints only when a block did.
func (t *Thresher) logStartupConfig() {
	log := t.cfg.logger
	printed := false

	if !t.cfg.suppressBanner {
		bi := readBuildInfo()

		for line := range strings.SplitSeq(banner, "\n") {
			log.Info(line)
		}

		log.Info("GoBPM — BPMN v2 process engine")
		log.Info(fmt.Sprintf("version:     %s", bi.version))
		log.Info(fmt.Sprintf("last commit: %s (%s)", bi.shortRevision(), bi.revTime))

		printed = true
	}

	if !t.cfg.suppressStartupConfig {
		log.Info(fmt.Sprintf("thresher id: %s", t.id))

		module := func(name string, impl any) {
			log.Info(fmt.Sprintf("  %-22s %T", name+":", impl))
		}

		log.Info("configuration:")
		module("repository", t.cfg.repository)
		module("logger", t.cfg.logger)
		module("tracer", t.cfg.tracer)
		module("metricsRecorder", t.cfg.metrics)
		module("clock", t.cfg.clock)
		module("messageBroker", t.cfg.msgBroker)
		module("expressionEngine", t.cfg.exprEngine)
		module("authorizationProvider", t.cfg.authz)
		module("workerDispatcher", t.cfg.dispatcher)

		printed = true
	}

	// The separator closes the report only when a block printed; suppressing
	// both blocks yields a fully silent startup (ADR-002 v.2 §4.4.1).
	if printed {
		log.Info(separator)
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

	// Synchronously initialize the EventHub so that any subsequent
	// RegisterEvent / UnregisterEvent / PropagateEvent call sees a fully
	// constructed hub. The return of Start establishes a happens-before
	// edge for all writes the hub performs during initialization. See
	// FIX-001 for the race this replaces (previously the hub was spun up
	// in a goroutine guarded only by a 1ms sleep, which is not a memory
	// barrier under the Go memory model).
	if err := t.eventHub.Start(ctx); err != nil {
		return errs.New(
			errs.M("couldn't start eventHub"),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err))
	}

	go func() {
		_ = t.eventHub.Run(ctx)
	}()

	err := t.UpdateState(Started)
	if err != nil {
		return errs.New(
			errs.M("couldn't update Thresher state"),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err))
	}

	// Register the persistent instance-starters for processes registered before
	// Run (the hub only accepts registrations once Started; SRD-015 FR-2).
	// Processes registered after Run wire their starters in RegisterProcess.
	if err := t.registerAllStarters(); err != nil {
		return errs.New(
			errs.M("couldn't register instance-starters at startup"),
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

// RegisterProcess registers a process directly, creating its snapshot
// internally. By default the process is registered for auto-instantiation: a
// persistent instance-starter is registered for each instantiating start
// trigger (a message StartEvent with no incoming flow), so a matching message
// spawns a new instance. WithManualStart (SRD-015 FR-9) opts out: no starter is
// registered and the process is instantiated only via StartProcess.
//
// Re-registering an already-registered process is idempotent (the first
// registration wins).
func (t *Thresher) RegisterProcess(
	p *process.Process,
	opts ...RegisterOption,
) error {
	if p == nil {
		return errs.New(
			errs.M("empty process"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	var rc registerConfig
	for _, o := range opts {
		if err := o(&rc); err != nil {
			return errs.New(
				errs.M("invalid register option"),
				errs.C(errorClass, errs.InvalidParameter),
				errs.E(err))
		}
	}

	// Create snapshot from process
	s, err := snapshot.New(p)
	if err != nil {
		return errs.New(
			errs.M("failed to create snapshot from process"),
			errs.C(errorClass, errs.BulidingFailed),
			errs.E(err))
	}

	// Auto mode (default) registers a persistent instance-starter per
	// instantiating start trigger; manual-start (FR-9) registers none.
	var starters []*instanceStarter
	if !rc.manualStart {
		starters = scanInstantiatingStarts(s, t)
	}

	t.m.Lock()
	if _, ok := t.snapshots[s.ProcessID]; ok {
		// Already registered — idempotent, keep the first registration.
		t.m.Unlock()

		return nil
	}

	t.snapshots[s.ProcessID] = s
	t.starters[s.ProcessID] = starters
	started := t.state == Started
	t.m.Unlock()

	// Register the starters on the EventHub OUTSIDE t.m: the hub path is
	// independent of t.m, and holding the engine lock across an engine-subsystem
	// call is the deadlock class FIX-002 RC2 warns about. Before Run the hub
	// isn't started yet, so registration is deferred to Run.
	if started {
		return t.registerStarters(starters)
	}

	return nil
}

// UnregisterProcess removes a registered process: it tears down every
// persistent instance-starter subscription registered for it and drops its
// snapshot. It is the teardown counterpart of RegisterProcess (SRD-015 FR-2).
// Running instances of the process are unaffected.
func (t *Thresher) UnregisterProcess(processID string) error {
	processID = strings.TrimSpace(processID)
	if processID == "" {
		return errs.New(
			errs.M("empty process id"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	t.m.Lock()
	_, ok := t.snapshots[processID]
	starters := t.starters[processID]
	started := t.state == Started
	if ok {
		delete(t.snapshots, processID)
		delete(t.starters, processID)
	}
	t.m.Unlock()

	if !ok {
		return errs.New(
			errs.M("process %q isn't registered", processID),
			errs.C(errorClass, errs.ObjectNotFound))
	}

	// Tear down the hub subscriptions OUTSIDE t.m (same reason as
	// RegisterProcess). Starters are only on the hub once the engine is Started;
	// before Run they were never registered, so nothing to unregister.
	if started {
		for _, st := range starters {
			if err := t.eventHub.UnregisterEvent(st, st.eDef.ID()); err != nil {
				return errs.New(
					errs.M("couldn't unregister instance-starter subscription"),
					errs.C(errorClass, errs.OperationFailed),
					errs.D("process_id", processID),
					errs.D("event_definition_id", st.eDef.ID()),
					errs.E(err))
			}
		}
	}

	return nil
}

// registerStarters registers each instance-starter as a persistent subscription
// on the engine EventHub. Called at the later of RegisterProcess (auto mode,
// engine already Started) and Run (for processes registered before Run).
func (t *Thresher) registerStarters(starters []*instanceStarter) error {
	for _, st := range starters {
		if err := t.eventHub.RegisterPersistentEvent(st, st.eDef); err != nil {
			return errs.New(
				errs.M("couldn't register instance-starter subscription"),
				errs.C(errorClass, errs.OperationFailed),
				errs.D("process_id", st.snapshot.ProcessID),
				errs.D("event_definition_id", st.eDef.ID()),
				errs.E(err))
		}
	}

	return nil
}

// registerAllStarters registers the instance-starters of every process
// registered before Run (the hub only accepts registrations once Started).
func (t *Thresher) registerAllStarters() error {
	t.m.Lock()
	all := make([]*instanceStarter, 0, len(t.starters))
	for _, sts := range t.starters {
		all = append(all, sts...)
	}
	t.m.Unlock()

	return t.registerStarters(all)
}

// launchInstanceFromEvent creates a new instance born from an event-triggered
// start (the persistent instance-starter fired): the start node is treated as
// already fired and the message payload carried by eDef is bound into the new
// instance, which then runs from the start node's outgoing flow(s) (ADR-015
// §2.2, SRD-015 §4.4). It mirrors launchInstance but uses instance.NewFromEvent.
func (t *Thresher) launchInstanceFromEvent(
	_ context.Context,
	s *snapshot.Snapshot,
	startNode flow.Node,
	eDef flow.EventDefinition,
) error {
	inst, err := instance.NewFromEvent(
		s, scope.EmptyDataPath, &t.cfg, t, nil, startNode.ID(), eDef)
	if err != nil {
		return errs.New(
			errs.M("couldn't create an event-born Instance for process %q",
				s.ProcessID),
			errs.C(errorClass, errs.BulidingFailed),
			errs.D("event_definition_id", eDef.ID()),
			errs.E(err))
	}

	// The instance owns this context for its whole lifetime; cancel is retained
	// in instanceReg.stop for later teardown (see launchInstance for why it is
	// not deferred).
	ctx, cancel := context.WithCancel(t.ctx)
	if err := inst.Run(ctx); err != nil {
		cancel()

		return errs.New(
			errs.M("event-born instance %q of process %q failed to run",
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

// StartProcess runs process with processId without any event even if
// process awaits them.
func (t *Thresher) StartProcess(processID string) error {
	if st := t.State(); st != Started {
		return errs.New(
			errs.M("thresher isn't started"),
			errs.C(errorClass, errs.InvalidState),
			errs.D("current_state", st.String()))
	}

	// Hold the engine lock only for the snapshot lookup; release it BEFORE
	// launchInstance. launchInstance (and, for event-start nodes, the
	// construction-time RegisterEvent -> Thresher.State()) re-acquire t.m, which
	// would self-deadlock a non-reentrant mutex if held across them (FIX-002
	// RC2).
	t.m.Lock()
	s, ok := t.snapshots[processID]
	t.m.Unlock()

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
	inst, err := instance.New(s, scope.EmptyDataPath, &t.cfg, t, nil)
	if err != nil {
		return errs.New(
			errs.M("couldn't create an Instance for process %q",
				s.ProcessID),
			errs.C(errorClass, errs.BulidingFailed),
			errs.E(err))
	}

	// The instance owns this context for its whole lifetime; cancel is retained
	// in instanceReg.stop for later teardown (engine stop / instance cleanup).
	// It must NOT be deferred here — inst.Run is non-blocking, so a deferred
	// cancel would terminate the instance the moment launchInstance returns.
	ctx, cancel := context.WithCancel(t.ctx)
	if err := inst.Run(ctx); err != nil {
		cancel()

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

var _ eventproc.EventProducer = (*Thresher)(nil)
