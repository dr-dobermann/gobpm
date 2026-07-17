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
	invoker             exec.ProcessInvoker
	sc                  instanceScope
	corr                correlator
	now                 func() time.Time
	tracksSnap          atomic.Pointer[[]*track]
	lastErr             atomic.Pointer[error]
	s                   *snapshot.Snapshot
	tracks              map[string]*track
	loopDone            chan struct{}
	// parentInstanceID/callNodeID are the call linkage (SRD-050): when set,
	// report stamps them on every fact so a child instance's trace stitches
	// back to its caller's Call Activity node. Empty for a top-level instance.
	parentInstanceID string
	callNodeID       string
	foundation.BaseElement
	observers  []obsReg
	trackCount atomic.Int64
	obsMu      sync.RWMutex
	obsID      uint64
	state      atomic.Uint32
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
	// invoker launches child instances for the Call Activities this instance
	// runs (SRD-050 FR-3); nil for a library embedder without a thresher — a
	// call then fails fast with a classified no-invoker error.
	invoker exec.ProcessInvoker
	// rootData is committed into the root scope at construction — the Call
	// Activity's inputs (SRD-050), the same injection point as an event
	// payload (bindEventPayload). Kept last so its len/cap fall outside the
	// GC pointer scan (fieldalignment).
	rootData []data.Data
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

	if s == nil {
		return nil,
			errs.New(
				errs.M("no snapshot is given"),
				errs.C(errorClass, errs.EmptyNotAllowed))
	}

	// Each Instance owns a private clone of the node graph so concurrent
	// instances of the same process never share a node (ADR-009); the snapshot
	// passed in stays the shared immutable template.
	s, err := s.Clone()
	if err != nil {
		return nil, errs.New(
			errs.M("snapshot clone for instance failed"),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err))
	}

	if er == nil {
		return nil,
			errs.New(
				errs.M("empty engine runtime"),
				errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if ep == nil {
		return nil,
			errs.New(
				errs.M("empty parent event producer"),
				errs.C(errorClass, errs.EmptyNotAllowed))
	}

	be, err := foundation.NewBaseElement()
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
		invoker:             cfg.invoker,
		loopDone:            make(chan struct{}),
		parentEventProducer: ep,
		td:                  td,
		parentInstanceID:    cfg.parentInstanceID,
		callNodeID:          cfg.callNodeID,
	}
	inst.state.Store(uint32(Created))
	inst.announceCreated()
	// The correlator back-pointer refers to the same heap object New returns —
	// inst escapes via &inst below (the instanceScope loader takes it the same way).
	inst.corr = correlator{inst: &inst, keys: map[string]string{}}

	if err := inst.sc.load(
		parentRoot, inst.s.ProcessName, inst.s.Properties, &inst); err != nil {
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

	if err := inst.createTracks(bornStart, cfg.bornEvent); err != nil {
		return nil, err
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
		withRootData(rootData),
		withCallLinkage(parentInstanceID, callNodeID),
		WithInvoker(inv))
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
func (inst *Instance) createTracks(
	bornStart flow.Node, bornEvent flow.EventDefinition,
) error {
	for _, n := range inst.s.Nodes {
		// born-from-event: the instantiating start node is already fired, so it
		// is not seeded as a track (it would otherwise park as a waiter); its
		// outgoing targets are seeded below instead (SRD-015 §4.4).
		if bornStart != nil && n.ID() == bornStart.ID() {
			continue
		}

		_, boundaryEvent := n.(flow.BoundaryEvent)
		if len(n.Incoming()) != 0 ||
			n.NodeType() == flow.GatewayNodeType ||
			boundaryEvent {
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
