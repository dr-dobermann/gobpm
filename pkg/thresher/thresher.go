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
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/internal/eventproc/eventhub"
	"github.com/dr-dobermann/gobpm/internal/instance"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/interactor"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/tasks"
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
	// Starting represents a thresher whose Run has claimed the start transition
	// but whose EventHub is not yet accepting: a transient state between
	// NotStarted and Started. Run publishes Started only after the hub is up.
	Starting
	// Started represents a thresher that has been started and is accepting
	// launches.
	Started
	// Paused represents a thresher that has been paused.
	Paused
	// Stopping represents a thresher whose Shutdown has claimed the stop
	// transition while teardown is in progress: a transient state between Started
	// and Stopped. Stopped is published only once teardown completes.
	Stopping
	// Stopped represents a thresher that has been gracefully shut down. It is
	// terminal: a Stopped thresher rejects RegisterProcess/StartProcess/Run.
	Stopped
)

// Validate checks State to be valid.
func (s State) Validate() error {
	if s > Stopped {
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
		"NotStarted",
		"Starting",
		"Started",
		"Paused",
		"Stopping",
		"Stopped",
	}[s]
}

// instanceReg holds single Instance registration.
type instanceReg struct {
	stop   context.CancelFunc
	inst   *instance.Instance
	handle *InstanceHandle
}

// Thresher represents the main BPMN process execution engine.
//
// Concurrency contract:
//   - m guards ONLY the four registry maps (registrations, nextVersion,
//     instances, seenKeys). Methods that mix registry access with EventHub or
//     launchInstance work confine the registry access to a "...Locked" helper
//     (locked.go) that returns plain data, so the lock is never held across an
//     engine-subsystem call (FIX-002 RC2). The simple lock-and-return discovery
//     accessors (Instance, Instances, Registrations) are themselves such
//     confined sections.
//   - state is atomic and lock-free — never read or written under m. This is
//     what makes State() safe to call while m is held and removes the FIX-002
//     RC2 self-deadlock vector by construction. Run/Shutdown drive it with
//     compare-and-swap through the transitional Starting/Stopping states.
//   - eventHub is an independent subsystem: it MUST NOT be touched while m is
//     held. cfg is immutable after New.
//   - keyLocks is a per-key serialization lock, DISTINCT from m, held across a
//     whole RegisterProcess / UnregisterVersion / UnregisterProcess method —
//     registry mutation plus the paired hub work — so register and unregister of
//     the same key cannot orphan a starter (FIX-013 §1.4). Lock order is per-key
//     (outer) -> m (inner, inside the "...Locked" helpers) -> hub work (under the
//     per-key lock, never under m). State() never takes a per-key lock, so it
//     adds no RC2 vector.
type Thresher struct {
	ctx           context.Context
	engineCancel  context.CancelFunc
	cfg           thresherConfig
	eventHub      eventproc.EventHub
	registrations map[string][]*ProcessRegistration
	nextVersion   map[string]int
	instances     map[string]instanceReg
	seenKeys      map[string]struct{}
	// tasks maps a parked UserTask id → its owning instance id, so Take/Complete
	// route to the right instance loop (SRD-034). Guarded by m. Populated/cleared
	// by taskDist as tasks are announced/withdrawn.
	tasks    map[string]string
	taskDist interactor.TaskDistributor
	keyLocks *keyLockManager
	// producer is the engine's single observable-event sink (SRD-041): it backs
	// t.cfg.reporter (so every instance/hub/dispatcher emits through it) and the
	// engine-scope Observe registry.
	producer *producer
	id       string
	m        sync.Mutex
	state    atomic.Uint32 // a State; lock-free, NEVER accessed under m
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
		id:            id,
		cfg:           cfg,
		registrations: map[string][]*ProcessRegistration{},
		nextVersion:   map[string]int{},
		instances:     map[string]instanceReg{},
		seenKeys:      map[string]struct{}{},
		tasks:         map[string]string{},
		keyLocks:      newKeyLockManager(),
	}
	t.state.Store(uint32(NotStarted))

	// The routing distributor records taskID → instanceID on Distribute (so
	// Take/Complete find the owning instance) and forwards to the embedder's
	// TaskDistributor. Built after t so it can reference the registry (SRD-034).
	t.taskDist = &routingDistributor{thr: t, next: cfg.TaskDistributor()}

	// The single observable-event producer (SRD-041 FR-4): bound to the
	// configured logger, with the visibility capabilities asserted once against
	// the authorizer. Installed as the engine's Reporter so every
	// instance/hub/dispatcher emits through it and Thresher.Observe registers on
	// it. Replaces the echo-only default the config carries pre-assembly.
	t.producer = newProducer(cfg.Logger(), cfg.AuthorizationProvider())
	t.cfg.reporter = t.producer

	// Bind the producer to the dispatcher (when it accepts one) so the
	// dispatcher's job-lifecycle events land on the same seam (SRD-041 §3.2). A
	// dispatcher without the binder simply does not emit.
	if ob, ok := cfg.WorkerDispatcher().(tasks.ReporterBinder); ok {
		ob.BindReporter(t.producer)
	}

	// Bind the engine as the worker dispatcher's completion sink (when it accepts
	// one) so a worker's Complete/Fail routes back to the owning instance by the id
	// embedded in the job id (SRD-036 §4.5). A dispatcher that reaches the engine
	// another way (a remote adapter) need not implement SinkBinder.
	if binder, ok := cfg.WorkerDispatcher().(tasks.SinkBinder); ok {
		binder.BindSink(t)
	}

	// Bind the engine's configured logger so the dispatcher's own lifecycle logging
	// uses the embedder's logger rather than its private default (SRD-037). Done
	// after all options are applied, so a WithLogger override is honored.
	if lb, ok := cfg.WorkerDispatcher().(tasks.LoggerBinder); ok {
		lb.BindLogger(cfg.Logger())
	}

	// Bind the engine's expression engine so the dispatcher can run a Job's
	// ErrorMapper when it classifies a raw fault engine-side (EngineAuthoritative,
	// SRD-038). A dispatcher that never classifies engine-side need not implement
	// ExpressionEngineBinder.
	if eb, ok := cfg.WorkerDispatcher().(tasks.ExpressionEngineBinder); ok {
		eb.BindExpressionEngine(cfg.ExpressionEngine())
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
		module("ruleEngine", t.cfg.ruleEngine)

		printed = true
	}

	// The separator closes the report only when a block printed; suppressing
	// both blocks yields a fully silent startup (ADR-002 v.2 §4.4.1).
	if printed {
		log.Info(separator)
	}
}

// State returns current state of the Threasher. It is lock-free (atomic load),
// so it is safe to call while t.m is held — the property that removes the
// FIX-002 RC2 re-entrant self-deadlock.
func (t *Thresher) State() State {
	//nolint:gosec // bounded State enum, no overflow
	return State(t.state.Load())
}

// UpdateState sets new State ns for the Threasher if there is no any error. It
// is lock-free (atomic store): state is never guarded by t.m.
func (t *Thresher) UpdateState(ns State) error {
	if err := ns.Validate(); err != nil {
		return errs.New(
			errs.M("couldn't set new state %q of the Thresher", ns.String()),
			errs.C(errorClass, errs.InvalidState),
			errs.E(err))
	}

	t.state.Store(uint32(ns))
	t.reportEngineState(ns)

	return nil
}

// enginePhase maps a Thresher state to its observable phase (SRD-041 §3.4). A
// transient or absent state (Invalid, NotStarted — including the Run rollback)
// has no phase and does not report.
var enginePhase = map[State]observability.Phase{
	Starting: observability.PhaseStarting,
	Started:  observability.PhaseStarted,
	Paused:   observability.PhasePaused,
	Stopping: observability.PhaseStopping,
	Stopped:  observability.PhaseStopped,
}

// reportEngineState announces a Thresher lifecycle transition through the engine
// Reporter (SRD-041 §3.4): EngineState carries only the phase, no node/instance.
func (t *Thresher) reportEngineState(s State) {
	phase, ok := enginePhase[s]
	if !ok {
		return
	}

	t.producer.Report(observability.Fact{
		Kind:  observability.KindEngineState,
		Phase: phase,
	})
}

// reportProcessLifecycle announces a process-registration transition through the
// engine Reporter (SRD-041 §3.4): the process id and version travel in details.
func (t *Thresher) reportProcessLifecycle(
	phase observability.Phase,
	details map[string]string,
) {
	t.producer.Report(observability.Fact{
		Kind:    observability.KindProcessLifecycle,
		Phase:   phase,
		Details: details,
	})
}

// Run starts Thresher event queue processing.
func (t *Thresher) Run(ctx context.Context) error {
	if ctx == nil {
		return errs.New(
			errs.M("empty context"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	// Claim the start transition atomically: NotStarted -> Starting. A concurrent
	// or repeat Run loses this CAS and is rejected. Starting is transient and not
	// yet accepting — Started is published only after the hub is up, so no
	// observer of Started ever sees a not-yet-ready hub.
	if !t.state.CompareAndSwap(uint32(NotStarted), uint32(Starting)) {
		return errs.New(
			errs.M("couldn't start thresher from state %q (should be in NotStarted)",
				t.State()),
			errs.C(errorClass, errs.InvalidState))
	}

	t.reportEngineState(Starting)

	// Derive the engine's own cancellable context: Shutdown cancels it to
	// cascade-terminate every instance (launched under t.ctx) and unblock the
	// hub Run, without touching the caller's ctx (SRD-019). The caller canceling
	// their ctx still propagates here.
	// engineCancel is stored on the Thresher and called by Shutdown (and both Run
	// rollbacks) — the engine context lives for the engine's lifetime (SRD-019),
	// so gosec's "cancel not called in this function" heuristic is a false
	// positive here.
	//nolint:gosec // G118: engineCancel escapes to a field, called cross-method
	t.ctx, t.engineCancel = context.WithCancel(ctx)
	runCtx := t.ctx

	// Synchronously initialize the EventHub so that any subsequent
	// RegisterEvent / UnregisterEvent / PropagateEvent call sees a fully
	// constructed hub. The return of Start establishes a happens-before
	// edge for all writes the hub performs during initialization. See
	// FIX-001 for the race this replaces (previously the hub was spun up
	// in a goroutine guarded only by a 1ms sleep, which is not a memory
	// barrier under the Go memory model).
	if err := t.eventHub.Start(t.ctx); err != nil {
		// The start failed: cancel the engine context we just derived (it is
		// otherwise abandoned — a retry Run reassigns t.ctx/engineCancel) and roll
		// the claim back to NotStarted so a retry stays possible.
		t.engineCancel()
		t.state.Store(uint32(NotStarted))

		return errs.New(
			errs.M("couldn't start eventHub"),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err))
	}

	go func() {
		// Use the context captured at spawn (runCtx), not t.ctx: a rollback
		// (hub-start or starter-registration failure) followed by a retry Run
		// reassigns t.ctx, and this goroutine must keep running against the
		// context it was started with rather than racing that write.
		//
		// A context.Canceled return is the expected shutdown path (engineCancel
		// or the caller's ctx); any other error is a genuine hub-loop failure and
		// must not be swallowed (FIX-013 §1.5).
		if err := t.eventHub.Run(runCtx); err != nil &&
			!errors.Is(err, context.Canceled) {
			t.cfg.logger.Error("event hub run loop failed", "error", err.Error())
		}
	}()

	// Publish readiness: the hub is up and accepting.
	t.state.Store(uint32(Started))
	t.reportEngineState(Started)

	// Register the persistent instance-starters for processes registered before
	// Run (the hub only accepts registrations once Started; SRD-015 FR-2).
	// Processes registered after Run wire their starters in RegisterProcess.
	if err := t.registerAllStarters(); err != nil {
		// Reached Started but cannot auto-start the registered processes — roll
		// the lifecycle back so a half-started engine is never observable and a
		// retry stays possible (the hub-Start path above does the same). The
		// engine context is canceled to stop the live hub goroutine and the
		// state returns to NotStarted. registerStarters is all-or-nothing
		// (FIX-013 §1.3), so no partial subscriptions linger to unwind here.
		t.engineCancel()
		t.state.Store(uint32(NotStarted))

		return errs.New(
			errs.M("couldn't register instance-starters at startup"),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err))
	}

	return nil
}

// Shutdown gracefully stops the engine (ADR-013 §2.5): it flips to the terminal
// Stopped state (rejecting further RegisterProcess/StartProcess/Run), cancels
// the engine context — which cascade-terminates every running instance and
// unblocks the hub Run — waits (bounded by ctx) for each instance to reach a
// terminal state, then drains the EventHub's waiters (EventHub.Shutdown,
// realizing ADR-006 §2.5). Idempotent; returns ctx.Err() if the deadline hits
// before all instances settle.
func (t *Thresher) Shutdown(ctx context.Context) error {
	// Claim the stop transition atomically. Idempotent: a second Shutdown that
	// finds Stopping/Stopped returns nil; one that finds NotStarted marks Stopped
	// (nothing to tear down); Starting is rejected (cannot shut down mid-start —
	// unreachable under the single-caller lifecycle). Started and Paused both own
	// live engine resources, so both claim teardown.
	switch st := t.State(); st {
	case Stopped, Stopping:
		return nil

	case NotStarted:
		t.state.Store(uint32(Stopped))
		t.reportEngineState(Stopped)

		return nil

	case Starting:
		return errs.New(
			errs.M("couldn't shut down thresher while it is starting"),
			errs.C(errorClass, errs.InvalidState))

	case Started, Paused:
		if !t.state.CompareAndSwap(uint32(st), uint32(Stopping)) {
			// Lost the race to a concurrent Shutdown that already claimed
			// teardown; that caller owns publishing Stopped.
			return nil
		}

		t.reportEngineState(Stopping)

	default: // Invalid — unreachable in the current lifecycle.
		return errs.New(
			errs.M("couldn't shut down thresher from state %q", st),
			errs.C(errorClass, errs.InvalidState))
	}

	// Teardown is claimed (Stopping). Publish the terminal Stopped once complete,
	// on every exit path below (including the ctx-deadline path).
	defer func() {
		t.state.Store(uint32(Stopped))
		t.reportEngineState(Stopped)
	}()

	t.m.Lock()
	regs := make([]instanceReg, 0, len(t.instances))
	for _, r := range t.instances {
		regs = append(regs, r)
	}

	cancel := t.engineCancel
	t.m.Unlock()

	// Cancel the engine context: every instance (launched under t.ctx) observes
	// it and walks to Terminated; the hub Run unblocks.
	if cancel != nil {
		cancel()
	}

	// Settle each running instance, bounded by ctx.
	for _, r := range regs {
		select {
		case <-r.inst.Done():
		case <-ctx.Done():
			return errs.New(
				errs.M("thresher shutdown timed out before instances settled"),
				errs.C(errorClass, errs.OperationFailed),
				errs.E(ctx.Err()))
		}
	}

	// Drain the event machinery: stop waiters and wait for their goroutines.
	return t.eventHub.Shutdown(ctx)
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
			errs.D("event_definition_type", string(eDef.Type())),
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
// Re-registering an already-registered key mints a NEW version (ADR-019), it is
// not an idempotent no-op: the latest version supersedes for auto-instantiation,
// and a superseded version only finishes its already-running instances.
func (t *Thresher) RegisterProcess(
	p *process.Process,
	opts ...RegisterOption,
) (*ProcessRegistration, error) {
	if p == nil {
		return nil, errs.New(
			errs.M("empty process"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if st := t.State(); st == Stopped {
		return nil, errs.New(
			errs.M("thresher is shut down; process registration rejected"),
			errs.C(errorClass, errs.InvalidState),
			errs.D("current_state", st.String()))
	}

	var rc registerConfig
	for _, o := range opts {
		if err := o(&rc); err != nil {
			return nil, errs.New(
				errs.M("invalid register option"),
				errs.C(errorClass, errs.InvalidParameter),
				errs.E(err))
		}
	}

	// Snapshot the process: an isolated, immutable version of the definition
	// (ADR-019 §2.3). Re-registering the same key mints a NEW version rather than
	// a silent no-op, so editing the process and registering again is meaningful.
	s, err := snapshot.New(p)
	if err != nil {
		return nil, errs.New(
			errs.M("failed to create snapshot from process"),
			errs.C(errorClass, errs.BulidingFailed),
			errs.E(err))
	}

	// Serialize this whole key operation against a concurrent unregister of the
	// same key: the per-key lock spans the registry mutation AND the hub work
	// below, so an UnregisterVersion/UnregisterProcess cannot drop the new
	// version from the registry in the window before its starters reach the hub
	// and leave them orphaned (FIX-013 §1.4). Acquired here (the key is known
	// from the pure snapshot) and held to return.
	defer t.lockKey(s.ProcessID)()

	// Auto mode (default) registers a persistent instance-starter per
	// instantiating start trigger; manual-start (FR-9) registers none.
	var starters []*instanceStarter
	if !rc.manualStart {
		starters = scanInstantiatingStarts(s, t)
	}

	reg, prevLatest := t.appendVersionLocked(s, starters, rc.manualStart)

	// The registry now holds a new latest version; if it displaced one, that
	// prior latest is superseded (its auto-start stops — ADR-019 §2.5). A
	// registry fact, independent of whether the hub is wired yet (SRD-041 §3.4).
	if prevLatest != nil {
		t.reportProcessLifecycle(observability.PhaseVersionSuperseded,
			map[string]string{
				observability.AttrProcessID: s.ProcessID,
				observability.AttrVersion:   strconv.Itoa(prevLatest.version),
			})
	}

	// Touch the EventHub OUTSIDE t.m: the hub path is independent of t.m, and
	// holding the engine lock across an engine-subsystem call is the deadlock
	// class FIX-002 RC2 warns about. Before Run the hub isn't started yet, so
	// registration is deferred to Run (registerAllStarters wires latest-only).
	if t.State() == Started {
		// Latest-supersedes (ADR-019 §2.5): the new version takes over auto-start,
		// so the previous latest's starters stop firing. A superseded version only
		// finishes its already-running instances.
		if prevLatest != nil {
			if err := t.unregisterStarters(prevLatest.starters); err != nil {
				return nil, err
			}
		}

		if err := t.registerStarters(starters); err != nil {
			return nil, err
		}
	}

	t.reportProcessLifecycle(observability.PhaseRegistered,
		map[string]string{
			observability.AttrProcessID: reg.key,
			observability.AttrVersion:   strconv.Itoa(reg.version),
		})

	return reg, nil
}

// UnregisterVersion removes ONE registered version — the one named by reg — by
// tearing down its live instance-starter subscriptions (if any) and dropping it
// from the registry. To remove the whole process (every version of a key) use
// UnregisterProcess. Running instances of that version are unaffected — they keep
// executing against their own frozen snapshot; stop everything via Shutdown
// (SRD-019 FR-8).
//
// Only the latest version of a key has live starters (latest-supersedes), so a
// superseded version has nothing on the hub to tear down. Removing the latest
// version PROMOTES the now-newest remaining version to live auto-start, so the
// invariant "latest registration == live starter set" keeps holding
// (ADR-019 §2.5; promote-on-removal).
func (t *Thresher) UnregisterVersion(reg *ProcessRegistration) error {
	if reg == nil {
		return errs.New(
			errs.M("UnregisterVersion: a nil ProcessRegistration isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	// Serialize against a concurrent register/unregister of the same key: the
	// per-key lock spans the registry removal AND the hub teardown, closing the
	// TOCTOU window of FIX-013 §1.4.
	defer t.lockKey(reg.key)()

	// promote holds the now-newest remaining version's starters when the latest
	// is removed — it is promoted to the live auto-start version so the invariant
	// "latest registration == live starter set" keeps holding (FR-8).
	found, wasLatest, promote := t.removeVersionLocked(reg)
	if !found {
		return errs.New(
			errs.M("registration %q (process %q v%d) isn't registered in this engine",
				reg.id, reg.key, reg.version),
			errs.C(errorClass, errs.ObjectNotFound))
	}

	// The version is out of the registry (the unregister fact); the hub teardown
	// below is its consequence (SRD-041 §3.4).
	t.reportProcessLifecycle(observability.PhaseUnregistered,
		map[string]string{
			observability.AttrProcessID: reg.key,
			observability.AttrVersion:   strconv.Itoa(reg.version),
		})

	// Maintain the hub subscriptions OUTSIDE t.m. Only the latest version's
	// starters are live (latest-supersedes), so removing a superseded version
	// touches nothing; before Run nothing is on the hub at all.
	if t.State() == Started && wasLatest {
		if err := t.unregisterStarters(reg.starters); err != nil {
			return err
		}

		// promote the now-newest remaining version to live auto-start (empty for
		// a manual-start version or when the key had a single version).
		if len(promote) > 0 {
			return t.registerStarters(promote)
		}
	}

	return nil
}

// UnregisterProcess removes the WHOLE process — every registered version of key
// — dropping them all from the registry and resetting the version counter (a
// later registration of this key is v1 again). It is the bulk counterpart of
// RegisterProcess; to remove a single version use UnregisterVersion. Running
// instances of any version survive on their own frozen snapshots (stop them via
// Shutdown). Only the latest version holds live starters, so only those are torn
// down from the hub. Errors ObjectNotFound if key is unknown, EmptyNotAllowed if
// key is empty.
func (t *Thresher) UnregisterProcess(key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return errs.New(
			errs.M("UnregisterProcess: an empty process key isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	// Serialize against a concurrent register/unregister of the same key: the
	// per-key lock spans the registry removal AND the hub teardown, closing the
	// TOCTOU window of FIX-013 §1.4.
	defer t.lockKey(key)()

	// removeKeyLocked takes the key's versions out and forgets its counter. The
	// latest version's starters are the only live ones (latest-supersedes); tear
	// them down OUTSIDE the lock (FIX-002 RC2).
	liveStarters, existed := t.removeKeyLocked(key)
	if !existed {
		return errs.New(
			errs.M("no registered version for process key %q", key),
			errs.C(errorClass, errs.ObjectNotFound))
	}

	// Every version of the process is out of the registry (SRD-041 §3.4).
	t.reportProcessLifecycle(observability.PhaseUnregistered,
		map[string]string{observability.AttrProcessID: key})

	if t.State() == Started {
		return t.unregisterStarters(liveStarters)
	}

	return nil
}

// registerStarters registers each instance-starter as a persistent subscription
// on the engine EventHub. Called at the later of RegisterProcess (auto mode,
// engine already Started) and Run (for processes registered before Run).
//
// It is all-or-nothing (FIX-013 §1.3): if any subscription fails, the ones this
// call already subscribed are best-effort unsubscribed before the error returns,
// so a partial subscription set never persists.
func (t *Thresher) registerStarters(starters []*instanceStarter) error {
	applied := make([]*instanceStarter, 0, len(starters))

	for _, st := range starters {
		if err := t.eventHub.RegisterPersistentEvent(st, st.eDef); err != nil {
			// roll back the ones already subscribed in this call; a rollback
			// failure joins the cause rather than being swallowed (ADR-022 v.1
			// §2.2), so a partial-rollback leak is not silent.
			var rollback error
			for _, done := range applied {
				rollback = errors.Join(rollback,
					t.eventHub.UnregisterEvent(done, done.eDef.ID()))
			}

			return errs.New(
				errs.M("couldn't register instance-starter subscription"),
				errs.C(errorClass, errs.OperationFailed),
				errs.D("process_id", st.snapshot.ProcessID),
				errs.D("event_definition_id", st.eDef.ID()),
				errs.E(errors.Join(err, rollback)))
		}

		applied = append(applied, st)
	}

	return nil
}

// unregisterStarters tears down each instance-starter's persistent subscription
// on the engine EventHub. Shared by UnregisterProcess and the latest-supersedes
// teardown in RegisterProcess.
//
// It is all-or-nothing (FIX-013 §1.3): if any teardown fails, the ones this call
// already unsubscribed are best-effort re-subscribed before the error returns,
// so a partial teardown never persists.
func (t *Thresher) unregisterStarters(starters []*instanceStarter) error {
	applied := make([]*instanceStarter, 0, len(starters))

	for _, st := range starters {
		if err := t.eventHub.UnregisterEvent(st, st.eDef.ID()); err != nil {
			// roll back the ones already unsubscribed in this call; a rollback
			// failure joins the cause rather than being swallowed (ADR-022 v.1
			// §2.2), so a partial-teardown leak is not silent.
			var rollback error
			for _, done := range applied {
				rollback = errors.Join(rollback,
					t.eventHub.RegisterPersistentEvent(done, done.eDef))
			}

			return errs.New(
				errs.M("couldn't unregister instance-starter subscription"),
				errs.C(errorClass, errs.OperationFailed),
				errs.D("process_id", st.snapshot.ProcessID),
				errs.D("event_definition_id", st.eDef.ID()),
				errs.E(errors.Join(err, rollback)))
		}

		applied = append(applied, st)
	}

	return nil
}

// registerAllStarters registers, at Run, the instance-starters of the LATEST
// version of every process registered before Run (only the latest auto-starts —
// latest-supersedes; the hub accepts registrations once Started).
func (t *Thresher) registerAllStarters() error {
	return t.registerStarters(t.latestStartersLocked())
}

// resolveAndLaunch performs the create-or-route-or-join decision per correlation
// key (ADR-016 v.1 §2.3). An empty key always instantiates (name-match, no
// dedup — the M3 behavior). A non-empty key instantiates a new instance only if
// it is unseen for this process, recording it so a subsequent same-key start
// **joins** the existing instance (no duplicate) rather than spawning a second.
// The check-and-record is atomic under t.m; the launch runs after the lock is
// released (launchInstance re-acquires t.m — FIX-002 RC2).
func (t *Thresher) resolveAndLaunch(
	ctx context.Context,
	s *snapshot.Snapshot,
	startNode flow.Node,
	eDef flow.EventDefinition,
	keyName, key string,
) error {
	if key == "" {
		t.cfg.logger.Debug(
			"instance-starter: creating instance (no correlation key)",
			"process_id", s.ProcessID, "start_node_id", startNode.ID())

		return t.launchInstanceFromEvent(ctx, s, startNode, eDef, keyName, key)
	}

	// Namespace the key by process so two processes correlating on the same
	// value remain distinct conversations.
	nsKey := s.ProcessID + "\x1f" + key

	if !t.reserveKeyLocked(nsKey) {
		t.cfg.logger.Debug("instance-starter: joined existing instance (key seen)",
			"process_id", s.ProcessID, "correlation_value", key)

		return nil // an instance already exists for this key: join, no duplicate
	}

	if err := t.launchInstanceFromEvent(
		ctx, s, startNode, eDef, keyName, key); err != nil {
		// the launch failed — drop the reservation so a later message can retry.
		t.releaseKeyLocked(nsKey)

		return err
	}

	t.cfg.logger.Debug("instance-starter: created new instance",
		"process_id", s.ProcessID, "correlation_value", key)

	return nil
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
	keyName, keyVal string,
) error {
	// The conversation key (keyName/keyVal) is seeded inside NewFromEvent BEFORE
	// createTracks parks any receiver, so an in-instance receiver reached
	// directly off the born start subscribes keyed to it (SRD-017 §4.5).
	inst, err := instance.NewFromEvent(
		s, scope.EmptyDataPath, &t.cfg, t, t.taskDist, startNode.ID(), eDef,
		keyName, keyVal, instance.WithInvoker(t))
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

	// An event-born instance is tracked with its read-only handle just like a
	// StartProcess one, so the SRD-019 discovery API (Instances -> Instance(id))
	// returns a usable handle for it instead of a nil that panics on observation.
	t.trackInstanceLocked(inst, cancel)

	return nil
}

// StartProcess launches a new instance of the exact registered version named by
// reg — the receipt RegisterProcess returned — and returns its read-only
// observation handle. A nil reg is rejected. To start by key instead, use
// StartLatest (the newest version) or StartVersion (a specific one).
func (t *Thresher) StartProcess(reg *ProcessRegistration) (*InstanceHandle, error) {
	if reg == nil {
		return nil, errs.New(
			errs.M("StartProcess: a nil ProcessRegistration isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if err := t.ensureStarted(); err != nil {
		return nil, err
	}

	// launchInstance re-acquires t.m, so reg.snapshot is read lock-free here: a
	// registration handle is immutable, and its snapshot is frozen (ADR-019).
	return t.launchInstance(reg.snapshot)
}

// StartLatest launches a new instance of the LATEST registered version of the
// process key, returning its observation handle. It errors if the key is empty
// or no version is registered for it. This is the "just run the current one"
// path; hold a ProcessRegistration and use StartProcess to pin an exact version.
func (t *Thresher) StartLatest(key string) (*InstanceHandle, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, errs.New(
			errs.M("StartLatest: empty process key isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if err := t.ensureStarted(); err != nil {
		return nil, err
	}

	// The lookup is lock-confined in latestSnapshotLocked and returns plain data,
	// so the lock is released BEFORE launchInstance (which re-acquires t.m and
	// would self-deadlock a non-reentrant mutex if held across it — FIX-002 RC2).
	s := t.latestSnapshotLocked(key)
	if s == nil {
		return nil, errs.New(
			errs.M("no registered version for process key %q", key),
			errs.C(errorClass, errs.ObjectNotFound))
	}

	return t.launchInstance(s)
}

// StartVersion launches a new instance of a SPECIFIC registered version (1-based)
// of the process key, returning its observation handle. It errors if the key is
// empty, the version is below 1, or no such key/version is registered. Use it to
// re-run an older version by its (key, version) without holding its handle.
func (t *Thresher) StartVersion(key string, version int) (*InstanceHandle, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, errs.New(
			errs.M("StartVersion: empty process key isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if version < 1 {
		return nil, errs.New(
			errs.M("StartVersion: version must be >= 1, got %d", version),
			errs.C(errorClass, errs.InvalidParameter))
	}

	if err := t.ensureStarted(); err != nil {
		return nil, err
	}

	// The lookup is lock-confined in snapshotForVersionLocked (FIX-002 RC2, as in
	// StartLatest). It addresses by version NUMBER, not slice position: removals
	// can leave gaps (v1, v3, …), so it scans rather than indexing regs[version-1].
	s := t.snapshotForVersionLocked(key, version)
	if s == nil {
		return nil, errs.New(
			errs.M("no version %d registered for process key %q", version, key),
			errs.C(errorClass, errs.ObjectNotFound))
	}

	return t.launchInstance(s)
}

// ensureStarted returns an InvalidState error unless the engine is Started — the
// precondition every Start* entry point shares.
func (t *Thresher) ensureStarted() error {
	if st := t.State(); st != Started {
		return errs.New(
			errs.M("thresher isn't started"),
			errs.C(errorClass, errs.InvalidState),
			errs.D("current_state", st.String()))
	}

	return nil
}

// Instance returns the observation handle of a running instance by its id, or
// false if no such instance is tracked (SRD-018). The handle is read-only.
func (t *Thresher) Instance(instanceID string) (*InstanceHandle, bool) {
	t.m.Lock()
	defer t.m.Unlock()

	reg, ok := t.instances[instanceID]
	if !ok {
		return nil, false
	}

	return reg.handle, true
}

// launchInstance creates a new Instance from the Snapshot s, runs it, appends it
// to the running instances of the Thresher, and returns its read-only handle.
func (t *Thresher) launchInstance(s *snapshot.Snapshot) (*InstanceHandle, error) {
	inst, err := instance.New(s, scope.EmptyDataPath, &t.cfg, t, t.taskDist,
		instance.WithInvoker(t))
	if err != nil {
		return nil, errs.New(
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
	if err = inst.Run(ctx); err != nil {
		cancel()

		return nil, errs.New(
			errs.M("inctance %q of process %q failed to run",
				inst.ID(), s.ProcessID),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err))
	}

	return t.trackInstanceLocked(inst, cancel), nil
}

// =============================================================================
// Interface implementation check

var _ eventproc.EventProducer = (*Thresher)(nil)
