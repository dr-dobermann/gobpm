/*
Package instance provides process instance management for BPMN execution.

This package is part of GoBPM - Business Process Management Engine for Go.
See LICENSE file for license information.

Author: dr-dobermann (rgabitov@gmail.com)
Repository: https://github.com/dr-dobermann/gobpm
*/
package instance

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/exec"
	"github.com/dr-dobermann/gobpm/pkg/interactor"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	engrenv "github.com/dr-dobermann/gobpm/pkg/renv"
)

const errorClass = "INSTANCE_ERROR"

// Instance represents a process instance for execution.
type Instance struct {
	startTime time.Time
	ctx       context.Context
	cancel    context.CancelFunc
	engrenv.EngineRuntime
	td                  interactor.TaskDistributor
	parentEventProducer eventproc.EventProducer
	events              chan trackEvent
	taskReq             chan taskRequest
	jobReq              chan jobRequest
	callReq             chan callRequest
	scopeReq            chan scopeRequest
	invoker             exec.ProcessInvoker
	waitHolders         exec.WaitHolders
	sc                  instanceScope
	corr                correlator
	// performers records who completed each human task, served read-only through
	// the RUNTIME subtree and carried across a hydrate (ADR-020 v.2 §2.4.2).
	performers *performers
	now                 func() time.Time
	tracksSnap          atomic.Pointer[[]*track]
	lastErr             atomic.Pointer[error]
	s                   *snapshot.Snapshot
	tracks              map[string]*track
	loopDone            chan struct{}
	// settled is closed when the instance reaches a TERMINAL state — and only
	// then. loopDone closes on EVERY loop exit, dehydration included, so it
	// cannot answer "has this instance finished?" any more (SRD-071): a
	// released instance has no loop but is very much still in flight. The
	// engine owns this channel per instance ID and hands the SAME one to each
	// rebuild, so a caller waiting on it waits across dehydration cycles.
	// nil for an instance nobody is waiting on.
	settled chan struct{}
	// parentInstanceID/callNodeID are the call linkage (SRD-050): when set,
	// report stamps them on every fact so a child instance's trace stitches
	// back to its caller's Call Activity node. Empty for a top-level instance.
	parentInstanceID string
	callNodeID       string
	// cpOwner arms checkpointing (SRD-070 FR-4); its int-sized siblings
	// (cpTTL/cpRecVersion/cpIncarnation) sit at the struct tail, outside
	// the GC pointer scan (fieldalignment).
	cpOwner string
	// waitHeld reports whether a parked track's wait has an engine-level holder
	// that can wake a released instance (SRD-071 FR-2). nil (the default, and
	// production without an injected WaitHolders) means "nothing held" — the
	// instance never dehydrates a wait it cannot wake. When WithWaitHolders is
	// set, New wires this to read each track's `held` flag (set at arm time when
	// a holder accepted the wait). Tests may inject their own predicate.
	waitHeld func(*track) bool
	// restoredLedgers is the checkpoint-rebuilt compensation ledger the
	// loop adopts at start (SRD-070 FR-6); nil for a fresh instance.
	restoredLedgers map[scope.DataPath][]*ledgerEntry
	// boundaryPlans are the armed-boundary firing plans read back from the
	// checkpoint (SRD-071 FR-9a), consumed as the loop re-arms each boundary.
	// Written once by restoreTracks before the loop starts and read only on
	// the loop goroutine, so it needs no lock. Empty for a fresh instance and
	// for one restored from a Schema-1 document.
	boundaryPlans map[boundaryKey]checkpoint.TimerDescriptor
	foundation.BaseElement
	observers  []obsReg
	trackCount atomic.Int64
	obsMu      sync.RWMutex
	obsID      uint64
	// dehydrationPins suppresses release while a synchronous interaction is in
	// flight (SRD-071 FR-8). A human task is IDLE by definition, so an instance
	// hydrated to service a Take/Complete would release again before the action
	// could land — the action would never win. The engine pins it across the
	// call; the loop's detector honors the pin.
	dehydrationPins atomic.Int32
	state           atomic.Uint32
	// The checkpoint cursors (SRD-070 FR-4): the lease TTL, the CAS
	// record version, the lease fencing incarnation (grows on reclaim,
	// SRD-071+). Non-pointer tail — see cpOwner above.
	cpTTL         time.Duration
	cpRecVersion  int64
	cpIncarnation int64
}

// validatedTemplate checks New's required collaborators and returns the
// instance's PRIVATE clone of the node graph: concurrent instances of one
// process never share a node (ADR-009), the passed snapshot staying the shared
// immutable template.
func validatedTemplate(
	s *snapshot.Snapshot,
	er engrenv.EngineRuntime,
	ep eventproc.EventProducer,
) (*snapshot.Snapshot, error) {
	if s == nil {
		return nil, errs.New(
			errs.M("no snapshot is given"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if er == nil {
		return nil, errs.New(
			errs.M("empty engine runtime"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if ep == nil {
		return nil, errs.New(
			errs.M("empty parent event producer"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	clone, err := s.Clone()
	if err != nil {
		return nil, errs.New(
			errs.M("snapshot clone for instance failed"),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err))
	}

	return clone, nil
}

// seedBoundaryPlans loads the recorded armed-boundary plans so the loop's
// re-arm can pin each deadline instead of recomputing it (SRD-071 FR-9a).
// Called by restoreTracks before the loop starts.
func (inst *Instance) seedBoundaryPlans(rr []checkpoint.BoundaryRecord) {
	if len(rr) == 0 {
		return
	}

	inst.boundaryPlans = make(map[boundaryKey]checkpoint.TimerDescriptor, len(rr))

	for _, r := range rr {
		// A boundary with no timer restores nothing — it re-arms from the
		// model like any fresh arm. Only the resolved deadline is state the
		// model cannot reproduce.
		if r.Timer == nil {
			continue
		}

		inst.boundaryPlans[boundaryKey{
			trackID:    r.HostTrack,
			boundaryID: r.BoundaryID,
			defIndex:   r.DefIndex,
		}] = *r.Timer
	}
}

// takeBoundaryPlan returns the recorded plan for one boundary arm and removes
// it: the plan describes the ONE window the checkpoint captured, so re-entering
// the same activity later is a new window that resolves its own deadline.
// Loop goroutine only.
func (inst *Instance) takeBoundaryPlan(
	k boundaryKey,
) (checkpoint.TimerDescriptor, bool) {
	rec, ok := inst.boundaryPlans[k]
	if ok {
		delete(inst.boundaryPlans, k)
	}

	return rec, ok
}

// PinResident suppresses dehydration until the matching UnpinResident, so a
// synchronous interaction (a human-task Take/Complete, SRD-071 FR-8) is
// guaranteed a live loop to run against. Pins nest.
func (inst *Instance) PinResident() {
	inst.dehydrationPins.Add(1)
}

// UnpinResident releases one PinResident. The instance may then release its
// goroutines again at the next idle moment.
func (inst *Instance) UnpinResident() {
	inst.dehydrationPins.Add(-1)
}

// pinnedResident reports whether an interaction is holding the instance in
// memory.
func (inst *Instance) pinnedResident() bool {
	return inst.dehydrationPins.Load() > 0
}

// WithSettledSignal gives the instance the channel to close when it reaches a
// TERMINAL state (SRD-071). The engine owns it per instance ID and passes the
// same channel to every rebuild, so a host waiting for completion is not woken
// by a mere dehydration — which releases the loop without finishing anything.
func WithSettledSignal(ch chan struct{}) Option {
	return func(cfg *newConfig) {
		cfg.settled = ch
	}
}

// markSettled closes the terminal signal exactly once. Called only from the
// loop's TERMINAL exit — never from the dehydration exit.
func (inst *Instance) markSettled() {
	if inst.settled == nil {
		return
	}

	select {
	case <-inst.settled:
		// already closed by an earlier incarnation — nothing to do.
	default:
		close(inst.settled)
	}
}

// WithResidentPin builds the instance already pinned (SRD-071 FR-8): the engine
// rebuilds an instance to service a task action, and the rebuild must not
// release again before the action arrives. The engine unpins when it is done.
func WithResidentPin() Option {
	return func(cfg *newConfig) {
		cfg.residentPin = true
	}
}

// wireWaitHeld points the idle detector's "is this wait wakeable?" gate at each
// track's arm-time `held` flag (SRD-071 FR-2/FR-3): with a durable holder
// registry a wait releases only if its holder accepted it. Without holders the
// predicate stays nil and no wait ever releases.
func (inst *Instance) wireWaitHeld() {
	if inst.waitHolders == nil {
		return
	}

	inst.waitHeld = func(t *track) bool { return t.held.Load() }
}

// newInstanceIdentity mints the instance's BaseElement — a restored
// instance keeps its recorded identity (SRD-070 FR-6).
func newInstanceIdentity(restoredID string) (*foundation.BaseElement, error) {
	if restoredID == "" {
		return foundation.NewBaseElement()
	}

	return foundation.NewBaseElement(foundation.WithID(restoredID))
}

// newConfig holds the optional parameters of New. Its zero value builds a
// normal instance (entry-node seeding); withBornEvent switches it to a
// born-from-event instance (SRD-015).
type newConfig struct {
	bornEvent    flow.EventDefinition
	bornStartID  string
	convKeyName  string
	convKeyValue string
	// parentInstanceID/callNodeID are the call linkage stamped on every fact a
	// child instance emits (SRD-050 FR-4); empty for a top-level instance.
	parentInstanceID string
	callNodeID       string
	// cpOwner arms consistent-cut checkpointing (SRD-070 FR-4): the
	// lease owner (engine id). Empty = volatile instance (today's
	// default); restoredID keeps a restored instance's identity
	// (SRD-070 FR-6). The int-sized checkpoint cursors sit at the tail.
	cpOwner    string
	restoredID string
	// invoker launches child instances for the Call Activities this instance
	// runs (SRD-050 FR-3); nil for a library embedder without a thresher — a
	// call then fails fast with a classified no-invoker error.
	invoker exec.ProcessInvoker
	// waitHolders is the engine's durable wait-holder registry (SRD-071 FR-3):
	// a dehydratable timer registers its deadline here at arm time. nil for a
	// library embedder or a volatile instance — every wait then stays resident.
	waitHolders exec.WaitHolders
	// settled is the engine's per-instance-ID terminal signal (SRD-071).
	settled chan struct{}
	// rootData is committed into the root scope at construction — the Call
	// Activity's inputs (SRD-050), the same injection point as an event
	// payload (bindEventPayload). Its len/cap and the checkpoint cursors
	// below fall outside the GC pointer scan (fieldalignment): cpTTL is
	// the lease's validity window, cpRecVersion/cpIncarnation seed the
	// restored fencing chain (SRD-070 FR-7).
	rootData      []data.Data
	cpTTL         time.Duration
	cpRecVersion  int64
	cpIncarnation int64
	// residentPin starts the instance pinned against dehydration (SRD-071 FR-8).
	residentPin bool
}

// newOption tunes New. The born-event / conversation-key options are exposed
// publicly via NewFromEvent rather than the bare option; WithInvoker is the one
// option the engine passes directly, via the exported Option alias below.
type newOption func(*newConfig)

// Option is the exported handle for a New option the engine passes across the
// package boundary (WithInvoker). It aliases the internal option type so the
// public constructors keep a single option shape.
type Option = newOption

// withBornEvent makes New build a born-from-event instance: the instantiating
// start node (startNodeID) is treated as already fired (its payload is bound,
// its outgoing flows seeded) instead of parked.
func withBornEvent(startNodeID string, eDef flow.EventDefinition) newOption {
	return func(c *newConfig) {
		c.bornStartID = startNodeID
		c.bornEvent = eDef
	}
}

// withRootData seeds data into the new instance's root scope at construction —
// the Call Activity's inputs (SRD-050 FR-4), committed at the same point as an
// event payload (bindEventPayload). Exposed publicly via NewChild. An empty
// slice is a no-op.
func withRootData(dd []data.Data) newOption {
	return func(c *newConfig) {
		c.rootData = dd
	}
}

// withCallLinkage stamps the call linkage (SRD-050 FR-4) onto every fact the
// instance emits, stitching a child's trace back to its caller. Exposed via
// NewChild. Empty ids leave the instance top-level (unstamped).
func withCallLinkage(parentInstanceID, callNodeID string) newOption {
	return func(c *newConfig) {
		c.parentInstanceID = parentInstanceID
		c.callNodeID = callNodeID
	}
}

// WithInvoker sets the ProcessInvoker the instance uses to launch child
// instances for its Call Activities (SRD-050 FR-3). The engine (thresher) passes
// itself; left unset (nil), a Call Activity fails fast with a classified
// no-invoker error (a library embedder without a thresher). It is the one New
// option the engine passes across the package boundary — the born-event and
// conversation-key options ride their dedicated constructors.
func WithInvoker(inv exec.ProcessInvoker) Option {
	return func(c *newConfig) {
		c.invoker = inv
	}
}

// WithWaitHolders sets the engine's durable wait-holder registry (SRD-071 FR-3):
// a dehydratable timer registers its deadline with it at arm time so the engine
// can wake the instance after it releases its goroutines. The engine (thresher)
// passes itself; left unset (nil), every wait stays resident and no instance
// ever dehydrates (a library embedder without a thresher, or checkpointing off).
func WithWaitHolders(wh exec.WaitHolders) Option {
	return func(c *newConfig) {
		c.waitHolders = wh
	}
}

// withConversationKey seeds the new instance's conversation key (SRD-017 §4.5)
// before createTracks runs, so an in-instance receiver reached directly off the
// born start subscribes keyed to it (createTracks parks receivers during
// construction — the seed must precede it). An empty name/value is ignored.
func withConversationKey(name, value string) newOption {
	return func(c *newConfig) {
		c.convKeyName = name
		c.convKeyValue = value
	}
}

// New creates a new Instance from the Snapshot s and sets state to Created.
// parentRoot is the container-scope path the instance's root scope attaches
// under (sub-process / call-activity nesting, future); scope.EmptyDataPath
// roots the instance at the top. Initial tracks are seeded from the process's
// entry nodes (no-incoming, non-gateway, non-boundary); withBornEvent
// (NewFromEvent) seeds from a fired start node instead.
func New(
	s *snapshot.Snapshot,
	parentRoot scope.DataPath,
	er engrenv.EngineRuntime,
	ep eventproc.EventProducer,
	td interactor.TaskDistributor,
	opts ...newOption,
) (*Instance, error) {
	var cfg newConfig
	for _, o := range opts {
		o(&cfg)
	}

	s, err := validatedTemplate(s, er, ep)
	if err != nil {
		return nil, err
	}

	be, err := newInstanceIdentity(cfg.restoredID)
	if err != nil {
		return nil, fmt.Errorf("failed to create base element: %w", err)
	}

	inst := Instance{
		BaseElement:         *be,
		EngineRuntime:       er,
		s:                   s,
		now:                 er.Clock().Now,
		tracks:              map[string]*track{},
		events:              make(chan trackEvent),
		taskReq:             make(chan taskRequest),
		jobReq:              make(chan jobRequest),
		callReq:             make(chan callRequest),
		scopeReq:            make(chan scopeRequest),
		invoker:             cfg.invoker,
		waitHolders:         cfg.waitHolders,
		settled:             cfg.settled,
		loopDone:            make(chan struct{}),
		parentEventProducer: ep,
		td:                  td,
		parentInstanceID:    cfg.parentInstanceID,
		callNodeID:          cfg.callNodeID,
		cpOwner:             cfg.cpOwner,
		cpTTL:               cfg.cpTTL,
		cpRecVersion:        cfg.cpRecVersion,
		cpIncarnation:       cfg.cpIncarnation,
	}
	inst.state.Store(uint32(Created))

	if cfg.residentPin {
		inst.PinResident()
	}

	inst.wireWaitHeld()
	inst.announceCreated()
	// The correlator back-pointer refers to the same heap object New returns —
	// inst escapes via &inst below (the instanceScope loader takes it the same way).
	inst.corr = correlator{inst: &inst, keys: map[string]string{}}
	inst.performers = newPerformers()

	if err := inst.sc.load(
		parentRoot, inst.s.ProcessName, inst.s.Properties,
		inst.s.DataObjects, inst.DataStores(), &inst); err != nil {
		return nil, errs.New(
			errs.M("couldn't load process'es properties into Instance scope"),
			errs.E(err),
			errs.C(errorClass, errs.BulidingFailed),
			errs.D("process_name", s.ProcessName),
			errs.D("process_id", s.ProcessID))
	}

	// Seed the initial root data: the born-from-event payload (resolving the
	// fired start node) and/or the Call Activity inputs (SRD-050).
	bornStart, serr := inst.seedInitialData(&cfg)
	if serr != nil {
		return nil, serr
	}

	// Seed the conversation key BEFORE createTracks (SRD-017 §4.5): createTracks
	// parks an in-instance receiver reached directly off the born start, and the
	// receiver must subscribe keyed to this conversation, so the key has to be
	// present first.
	inst.corr.associate(cfg.convKeyName, cfg.convKeyValue)

	// A restored instance rebuilds its track table from the checkpoint
	// (SRD-070 FR-6) — start seeding would double-run the entry nodes.
	if cfg.restoredID == "" {
		if err := inst.createTracks(bornStart, cfg.bornEvent); err != nil {
			return nil, err
		}
	}

	// TracksCount reflects all tracks created (initial + forks); seed it with
	// the initial tracks. The loop adds forks; ended tracks are retained.
	inst.trackCount.Store(int64(len(inst.tracks)))

	return &inst, nil
}

// seedInitialData commits the instance's construction-time root data and returns
// the resolved born-from-event start node (nil for a normally-seeded instance).
// Two sources, both at the root scope: a born-from-event payload (SRD-015 — its
// start node resolved so createTracks seeds from its outgoing flows instead of
// parking it) and the Call Activity inputs (SRD-050 — bindRootData no-ops when
// empty). A child is never also born-from-event, so the two never overlap.
func (inst *Instance) seedInitialData(cfg *newConfig) (flow.Node, error) {
	var bornStart flow.Node

	if cfg.bornStartID != "" {
		bs, ok := inst.s.Nodes[cfg.bornStartID]
		if !ok {
			return nil, errs.New(
				errs.M("born-from-event start node %q not found in snapshot",
					cfg.bornStartID),
				errs.C(errorClass, errs.ObjectNotFound),
				errs.D("process_id", inst.s.ProcessID))
		}

		bornStart = bs

		if err := inst.sc.bindEventPayload(cfg.bornEvent); err != nil {
			return nil, err
		}
	}

	if err := inst.sc.bindRootData(cfg.rootData); err != nil {
		return nil, err
	}

	return bornStart, nil
}

// NewFromEvent creates an Instance born from an event-triggered start (SRD-015):
// the instantiating start node (startNodeID) is treated as already fired. The
// message payload carried by eDef is bound into the instance root scope and the
// initial track(s) start on the start node's outgoing flow target(s), rather
// than the start node being parked as a waiter. The auto-instantiation path
// (Thresher.launchInstanceFromEvent) uses this; StartProcess keeps using New.
// keyName/keyValue seed the conversation key the start trigger correlated on
// (SRD-017 §4.5); both empty for an uncorrelated start.
func NewFromEvent(
	s *snapshot.Snapshot,
	parentRoot scope.DataPath,
	er engrenv.EngineRuntime,
	ep eventproc.EventProducer,
	td interactor.TaskDistributor,
	startNodeID string,
	eDef flow.EventDefinition,
	keyName, keyValue string,
	opts ...newOption,
) (*Instance, error) {
	startNodeID = strings.TrimSpace(startNodeID)
	if startNodeID == "" {
		return nil, errs.New(
			errs.M("NewFromEvent: empty start node id isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if eDef == nil {
		return nil, errs.New(
			errs.M("NewFromEvent: a nil event definition isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	// The born-event and conversation-key options are fixed for this path; any
	// extra options (WithInvoker) are appended, so a call-bearing auto-started
	// process can itself launch child instances.
	return New(s, parentRoot, er, ep, td,
		append([]newOption{
			withBornEvent(startNodeID, eDef),
			withConversationKey(keyName, keyValue),
		}, opts...)...)
}

// NewChild creates an instance launched by a Call Activity (SRD-050 FR-4): a
// CHILD instance rooted at the top (scope.EmptyDataPath — the isolation
// contract, no scope walk-up to the caller), seeded with the caller-resolved
// inputs (already cloned across the boundary) and stamped with the call linkage
// so its facts stitch back to the caller. It mirrors NewFromEvent's role as the
// public wrapper over the private options; the thresher's InvokeProcess uses it.
// A nil snapshot, an empty parent instance id, or an empty call node id is
// rejected — the linkage is the contract that makes a child's trace attributable.
func NewChild(
	s *snapshot.Snapshot,
	er engrenv.EngineRuntime,
	ep eventproc.EventProducer,
	td interactor.TaskDistributor,
	inv exec.ProcessInvoker,
	rootData []data.Data,
	parentInstanceID, callNodeID string,
	opts ...Option,
) (*Instance, error) {
	parentInstanceID = strings.TrimSpace(parentInstanceID)
	if parentInstanceID == "" {
		return nil, errs.New(
			errs.M("NewChild: empty parent instance id isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	callNodeID = strings.TrimSpace(callNodeID)
	if callNodeID == "" {
		return nil, errs.New(
			errs.M("NewChild: empty call activity node id isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	return New(s, scope.EmptyDataPath, er, ep, td,
		append([]Option{
			withRootData(rootData),
			withCallLinkage(parentInstanceID, callNodeID),
			WithInvoker(inv),
		}, opts...)...)
}

// emit delivers a track event to the loop. It never blocks forever: once the
// loop has exited (loopDone closed) it drops the event. It must NOT drop on
// ctx cancellation — the loop keeps draining events until every track has
// ended, so a canceled instance still accounts each track's terminal event and
// reaches Terminated instead of hanging.
func (inst *Instance) emit(ev trackEvent) {
	select {
	case inst.events <- ev:
	case <-inst.loopDone:
	}
}

// createTrack creates all initial tracks of the Instance.
// seedableEntry reports whether n should seed an initial track: a no-incoming,
// non-gateway, non-boundary node that is not the already-fired born start
// (SRD-015 §4.4), not a scope-armed Event Sub-Process handler (ADR-023 v.2
// §2.10 / SRD-052 FR-3), not a Link event node — a Link catch (target) has
// no incoming by design but is reached only via the redirect, and a Link throw
// (source) is reached through its own incoming flow (ADR-006 v.4 §2.8) — and
// not a compensation handler, which is flow-less by design and runs only when
// compensation is thrown (ADR-026 §2.3, SRD-059 FR-2).
func seedableEntry(n, bornStart flow.Node) bool {
	if bornStart != nil && n.ID() == bornStart.ID() {
		return false
	}

	if isEventSubHandler(n) {
		return false
	}

	if ln, ok := n.(flow.LinkEventNode); ok && ln.LinkName() != "" {
		return false
	}

	if _, boundary := n.(flow.BoundaryEvent); boundary {
		return false
	}

	if c, ok := n.(interface{ ForCompensation() bool }); ok &&
		c.ForCompensation() {
		return false
	}

	return len(n.Incoming()) == 0 && n.NodeType() != flow.GatewayNodeType
}

func (inst *Instance) createTracks(
	bornStart flow.Node, bornEvent flow.EventDefinition,
) error {
	for _, n := range inst.s.Nodes {
		if !seedableEntry(n, bornStart) {
			continue
		}

		t, err := newTrack(n, inst, nil)
		if err != nil {
			return err
		}

		inst.tracks[t.ID()] = t
	}

	if bornStart == nil {
		return nil
	}

	// A Parallel-start Event-Based gateway seeds differently: the arm whose event
	// instantiated the process runs its continuation while the OTHER arms re-arm as
	// in-instance waiters keyed to the seeded conversation (SRD-025 §4.3). Completion
	// stays automatic — a waiting arm track keeps the instance active until it fires.
	if ps, ok := bornStart.(interface{ ParallelStart() bool }); ok &&
		ps.ParallelStart() {
		return inst.seedParallelStart(bornStart, bornEvent)
	}

	// Single born start (message StartEvent, instantiate ReceiveTask, or an
	// Exclusive-start gate arm): seed the initial track(s) on the already-fired
	// start node's outgoing flow target(s) — the spawnForks pattern: the track's
	// first step is the target node, recording the flow it arrived on.
	for _, f := range bornStart.Outgoing() {
		t, err := newTrack(f.Target().Node(), inst, nil)
		if err != nil {
			return err
		}

		t.steps[0].inFlow = f
		inst.tracks[t.ID()] = t
	}

	return nil
}

// seedParallelStart seeds a Parallel-start Event-Based gateway instance (SRD-025 §4.3):
// the arm whose event instantiated the process (resolved via the gate's ArmFor over the
// born event) is pre-fired — tracks on its outgoing target(s), its payload already bound
// at root — while every OTHER arm is seeded as a track AT the arm node, which
// run()->checkNodeType arms as a waiter keyed to the seeded conversation key. The waiting
// arms keep the instance active until they fire, so BPMN's "completes only once all
// events have occurred" (§13.2) is automatic — no explicit completion counter.
func (inst *Instance) seedParallelStart(
	gate flow.Node, bornEvent flow.EventDefinition,
) error {
	router, ok := gate.(interface {
		ArmFor(flow.EventDefinition) (flow.Node, bool)
	})
	if !ok {
		return errs.New(
			errs.M("parallel-start gate %q does not resolve arms", gate.ID()),
			errs.C(errorClass, errs.InvalidState))
	}

	firing, ok := router.ArmFor(bornEvent)
	if !ok {
		return errs.New(
			errs.M("parallel-start gate %q has no arm for the instantiating event",
				gate.ID()),
			errs.C(errorClass, errs.InvalidState))
	}

	for _, of := range gate.Outgoing() {
		arm := of.Target().Node()

		if arm.ID() == firing.ID() {
			// pre-fire: run the firing arm's continuation (its payload is bound at root).
			for _, af := range arm.Outgoing() {
				t, err := newTrack(af.Target().Node(), inst, nil)
				if err != nil {
					return err
				}

				t.steps[0].inFlow = af
				inst.tracks[t.ID()] = t
			}

			continue
		}

		// re-arm: a waiting track at the other arm node (keyed to the conversation key).
		t, err := newTrack(arm, inst, nil)
		if err != nil {
			return err
		}

		inst.tracks[t.ID()] = t
	}

	return nil
}
