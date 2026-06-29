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
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/exp/maps"

	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/exec"
	"github.com/dr-dobermann/gobpm/pkg/interactor"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/msgflow"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	engrenv "github.com/dr-dobermann/gobpm/pkg/renv"
)

const (
	errorClass = "INSTANCE_ERROR"

	// StartedAt represents the started time variable name.
	StartedAt = "STARTED_AT"
	// CurrState represents the current state variable name.
	CurrState = "STATE"
	// TracksCount represents the tracks count variable name.
	TracksCount = "TRACKS_CNT"
)

// State represents the process instance state.
// uint32-backed so it can live in an atomic.Uint32 without a narrowing
// conversion (the instance's run state is read lock-free via State()).
type State uint32

// Instance lifecycle states — the in-memory runtime lifecycle the instance
// actually exercises (mirrors ADR-001 §4.2). The error branch and suspend are
// owned by their future ADRs, not this runtime, and are absent here.
const (
	// Created is a created instance, not yet running.
	Created State = iota
	// Active is a running instance executing its tracks.
	Active
	// Completed is an instance that finished when all tracks ended normally.
	Completed
	// Terminating is an instance canceling its tracks after ctx cancel.
	Terminating
	// Terminated is an instance that finished via cancellation.
	Terminated
)

// String returns the human-readable name of the instance state.
func (s State) String() string {
	return []string{
		"Created",
		"Active",
		"Completed",
		"Terminating",
		"Terminated",
	}[s]
}

// Instance represents a process instance for execution.
type Instance struct {
	startTime time.Time
	ctx       context.Context
	cancel    context.CancelFunc
	engrenv.EngineRuntime
	rr                  interactor.Registrator
	parentEventProducer eventproc.EventProducer
	events              chan trackEvent
	sc                  instanceScope
	convKeys            map[string]string
	now                 func() time.Time
	tracksSnap          atomic.Pointer[[]*track]
	lastErr             atomic.Pointer[error]
	s                   *snapshot.Snapshot
	tracks              map[string]*track
	loopDone            chan struct{}
	foundation.BaseElement
	observers  []obsReg
	trackCount atomic.Int64
	obsMu      sync.RWMutex
	convMu     sync.Mutex
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
}

// newOption tunes New. Born-from-event is the only option and is exposed
// publicly via NewFromEvent rather than the bare option.
type newOption func(*newConfig)

// withBornEvent makes New build a born-from-event instance: the instantiating
// start node (startNodeID) is treated as already fired (its payload is bound,
// its outgoing flows seeded) instead of parked.
func withBornEvent(startNodeID string, eDef flow.EventDefinition) newOption {
	return func(c *newConfig) {
		c.bornStartID = startNodeID
		c.bornEvent = eDef
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
	rr interactor.Registrator,
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
		convKeys:            map[string]string{},
		events:              make(chan trackEvent),
		loopDone:            make(chan struct{}),
		parentEventProducer: ep,
		rr:                  rr,
	}
	inst.state.Store(uint32(Created))

	if err := inst.sc.load(
		parentRoot, inst.s.ProcessName, inst.s.Properties, &inst); err != nil {
		return nil, errs.New(
			errs.M("couldn't load process'es properties into Instance scope"),
			errs.E(err),
			errs.C(errorClass, errs.BulidingFailed),
			errs.D("process_name", s.ProcessName),
			errs.D("process_id", s.ProcessID))
	}

	// Born-from-event: bind the payload and resolve the fired start node so
	// createTracks seeds from its outgoing flows instead of parking it.
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

	// Seed the conversation key BEFORE createTracks (SRD-017 §4.5): createTracks
	// parks an in-instance receiver reached directly off the born start, and the
	// receiver must subscribe keyed to this conversation, so the key has to be
	// present first.
	inst.associateConversationKey(cfg.convKeyName, cfg.convKeyValue)

	if err := inst.createTracks(bornStart, cfg.bornEvent); err != nil {
		return nil, err
	}

	// TracksCount reflects all tracks created (initial + forks); seed it with
	// the initial tracks. The loop adds forks; ended tracks are retained.
	inst.trackCount.Store(int64(len(inst.tracks)))

	return &inst, nil
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
	rr interactor.Registrator,
	startNodeID string,
	eDef flow.EventDefinition,
	keyName, keyValue string,
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

	return New(s, parentRoot, er, ep, rr,
		withBornEvent(startNodeID, eDef),
		withConversationKey(keyName, keyValue))
}

// AssociateConversationKey records value under the conversation key named name
// set-if-absent (SRD-017 FR-1). It is the no-result form the optional msgflow
// recorder capability uses (first keyed send); the delivery path uses the
// bool-returning associateConversationKey to learn whether to extend receivers.
func (inst *Instance) AssociateConversationKey(name, value string) {
	inst.associateConversationKey(name, value)
}

// associateConversationKey records value under name if name is not already held,
// returning whether it was added (a new conversation key). Empty inputs are a
// no-op returning false. Guarded by convMu — forked tracks run concurrently.
func (inst *Instance) associateConversationKey(name, value string) bool {
	if name == "" || value == "" {
		return false
	}

	inst.convMu.Lock()
	defer inst.convMu.Unlock()

	if _, ok := inst.convKeys[name]; ok {
		return false
	}

	inst.convKeys[name] = value

	return true
}

// conversationKeyValues returns a snapshot of the instance's conversation key
// values (SRD-017 §4.3): the keys its in-instance message receivers subscribe
// on so a follow-up message routes to this instance. An instance with no
// established key returns nil (a wildcard subscription). Taken under convMu —
// forked tracks run on concurrent goroutines.
func (inst *Instance) conversationKeyValues() []string {
	inst.convMu.Lock()
	defer inst.convMu.Unlock()

	if len(inst.convKeys) == 0 {
		return nil
	}

	vals := make([]string, 0, len(inst.convKeys))
	for _, v := range inst.convKeys {
		vals = append(vals, v)
	}

	return vals
}

// The Instance is the hub-facing event processor for Message catches (SRD-027 FR-8).
var _ eventproc.EventProcessor = (*Instance)(nil)

// ProcessEvent (eventproc.EventProcessor) is the hub-facing entry for a Message catch: the
// Instance is the registered processor (SRD-027 FR-8), because message correlation state is
// instance-owned. It does NOT touch track state — it emits the fired event to its own loop
// carrying NO track; the loop resolves the parked track via its msgEDef→track index and runs
// validateAndAssociate before dispatch. Returns once enqueued, not once applied.
func (inst *Instance) ProcessEvent(
	_ context.Context,
	eDef flow.EventDefinition,
) error {
	if eDef == nil {
		return errs.New(
			errs.M("Instance.ProcessEvent: a nil EventDefinition isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed, errs.InvalidParameter))
	}

	// track == nil marks the Message branch — the loop resolves the target via msgIdx (§3.4).
	inst.emit(trackEvent{kind: evDeliver, eDef: eDef})

	return nil
}

// CorrelationKeys returns the conversation key values this instance has established
// (SRD-017 §4.3, SRD-027 FR-8). The message waiter reads it structurally (the "declared
// filter") to subscribe this receiver keyed to its conversation; an instance with no keys
// yields none, leaving a wildcard subscription. Ownership moved here from track when the
// Instance became the message processor — only the message path was ever keyed.
func (inst *Instance) CorrelationKeys() []string {
	return inst.conversationKeyValues()
}

// validateAndAssociate applies the conversation-token rules on a received
// message (SRD-017 §4.5, BPMN §8.4.2). It derives every declared correlation key
// from the message payload in two passes: first it checks for a mismatch — a key
// this instance already holds whose derived value differs — and, if any, reports
// mismatch=true and associates nothing (the message isn't for this conversation,
// so the caller rejects it); otherwise it associates each not-yet-held value
// (lazy secondary-key initialization), extending currently-parked receivers so
// the conversation becomes reachable by the new key, and reports mismatch=false.
func (inst *Instance) validateAndAssociate(
	ctx context.Context,
	eDef flow.EventDefinition,
) (mismatch bool) {
	keys := inst.s.CorrelationKeys
	if len(keys) == 0 {
		return false
	}

	mr, ok := eDef.(interface {
		Message() *bpmncommon.Message
	})
	if !ok {
		return false
	}

	msg := mr.Message()

	var payload any
	if items := eDef.GetItemsList(); len(items) != 0 {
		payload = items[0].Structure().Get(ctx)
	}

	derived := make(map[string]string, len(keys))

	for _, key := range keys {
		v, ok, err := msgflow.DeriveKey(
			ctx, inst.ExpressionEngine(), key, msg, payload)
		if err != nil {
			inst.Logger().Warn("conversation key derivation failed",
				"instance_id", inst.ID(), "correlation_key", key.Name)

			continue
		}

		if !ok {
			continue
		}

		if held, isHeld := inst.heldConversationKey(key.Name); isHeld &&
			held != v {
			inst.Logger().Debug("correlation key mismatch — message dropped",
				"instance_id", inst.ID(), "correlation_key", key.Name)

			return true
		}

		derived[key.Name] = v
	}

	for name, v := range derived {
		if inst.associateConversationKey(name, v) {
			inst.extendReceivers(v)
		}
	}

	return false
}

// heldConversationKey returns the value held for the named conversation key and
// whether it is held. Read under convMu — forked tracks run concurrently.
func (inst *Instance) heldConversationKey(name string) (string, bool) {
	inst.convMu.Lock()
	defer inst.convMu.Unlock()

	v, ok := inst.convKeys[name]

	return v, ok
}

// extendReceivers adds a newly-learned correlation value to every in-instance
// message receiver's broker subscription (SRD-017 §4.5), so a follow-up carrying
// it routes here. It reaches the EventHub's optional AddEventKey capability
// structurally (no interface change). A receiver that isn't parked yet has no
// waiter — a benign no-op; it picks the value up from the grown key-set when it
// registers.
func (inst *Instance) extendReceivers(value string) {
	adder, ok := inst.parentEventProducer.(interface {
		AddEventKey(eDefID, key string) error
	})
	if !ok {
		return
	}

	for _, n := range inst.s.Nodes {
		en, ok := n.(flow.EventNode)
		if !ok {
			continue
		}

		for _, d := range en.Definitions() {
			if d.Type() != flow.TriggerMessage {
				continue
			}

			if err := adder.AddEventKey(d.ID(), value); err != nil {
				inst.Logger().Debug("extend receiver subscription failed",
					"instance_id", inst.ID(),
					"event_definition_id", d.ID())
			}
		}
	}
}

// State returns current state of the Instance.
func (inst *Instance) State() State {
	return State(inst.state.Load())
}

// setState sets a new instance state. Written only from loop() (the single
// owner of lifecycle state) and from Run(); State() readers see it via the
// atomic, so no lock is needed.
func (inst *Instance) setState(newState State) {
	inst.state.Store(uint32(newState))
	inst.notify(ObsInstanceState, "", "", newState.String())
}

// LastErr returns the fatal error that stopped the instance (e.g. a fork
// whose target node could not be constructed), or nil. Set only by loop().
func (inst *Instance) LastErr() error {
	if e := inst.lastErr.Load(); e != nil {
		return *e
	}

	return nil
}

// Done returns a channel closed when the instance reaches a terminal state
// (Completed or Terminated). It backs host WaitCompletion (SRD-018): a closed
// channel is a non-blocking, broadcast completion signal — never dropped, unlike
// the lossy observation stream. The loop closes it on exit.
func (inst *Instance) Done() <-chan struct{} {
	return inst.loopDone
}

// DataReader returns the instance's read-only root data reader — process
// properties plus the runtime variables (StartedAt/CurrState/TracksCount). For
// host observation (SRD-018): the returned value exposes only the read-only
// service.DataReader surface, never a mutating method. Built once in New (an
// empty frame at the process-root scope), so this getter cannot fail.
func (inst *Instance) DataReader() service.DataReader {
	return inst.sc.reader
}

// Run starts the process instance execution. Execution could be stopped by
// cancel function of the context.
func (inst *Instance) Run(
	ctx context.Context,
) error {
	if ctx == nil {
		return errs.New(
			errs.M("empty context for instance"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if inst.State() != Created {
		return errs.New(
			errs.M("invalid instance state to run"),
			errs.C(errorClass, errs.InvalidState),
			errs.D("current_state", inst.State()))
	}

	// Derive the instance's own cancellable context so Cancel() can terminate it
	// (SRD-019). The parent ctx (the engine's, via the Thresher) still cascades —
	// canceling either drives the loop's ctx.Done() termination path.
	inst.ctx, inst.cancel = context.WithCancel(ctx)
	inst.startTime = inst.now()
	inst.setState(Active)

	// initial tracks were built by createTracks() during New; hand them to the
	// loop, which becomes the sole owner of lifecycle state from here on.
	initial := maps.Values(inst.tracks)

	go inst.loop(inst.ctx, initial)

	return nil
}

// Cancel requests termination of the instance: it cancels the instance context,
// which the loop observes (ctx.Done()) and walks Active → Terminating →
// Terminated, withdrawing its tracks. Idempotent and non-blocking — a host that
// wants to await the terminal state uses the InstanceHandle's Cancel/
// WaitCompletion. Safe before Run (no-op until the context exists).
func (inst *Instance) Cancel() {
	if inst.cancel != nil {
		inst.cancel()
	}
}

// Terminate abnormally ends the instance on behalf of a Terminate End Event
// (renv.RuntimeEnvironment, SRD-030 FR-2): it emits an evTerminate trackEvent
// onto the loop's own channel — the single-writer lane every signal uses — and
// the loop tears the instance down (stopAll). Reached only during an active run
// (a Terminate End Event is executed by the loop); idempotent — repeat events
// hit stopAll's stopping guard.
func (inst *Instance) Terminate() {
	inst.emit(trackEvent{kind: evTerminate})
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

// loop is the single owner of the Instance's lifecycle state (the tracks
// registry and the run state). Tracks never mutate that state directly — they
// emit events here, applied in order in this one goroutine, so no lock guards
// lifecycle state. The instance finishes when all tracks have ended.
func (inst *Instance) loop(ctx context.Context, initial []*track) {
	defer close(inst.loopDone)

	active := 0
	stopping := false
	// waiting holds tracks parked on their evtCh and not yet delivered (SRD-027 FR-4):
	// presence ⟺ parked-and-undelivered. Loop-goroutine-only, so no lock — like active /
	// stopping. evWaiting adds a track; the first evDeliver for it removes it and sends the
	// event (the winner); a later evDeliver for it finds it absent and drops (a losing arm of
	// an Event-Based gateway, or a duplicate fire).
	waiting := map[string]struct{}{}
	// msgIdx maps a waited Message catch-definition id → the parked track (SRD-027 FR-5/FR-8).
	// A track-less Message evDeliver (from Instance.ProcessEvent) is resolved through it; it is
	// seeded alongside waiting (by evWaiting / spawn) and a track is cleared from it the moment
	// it flips out of waiting or ends, so an index entry never outlives its track.
	msgIdx := map[string]*track{}
	// position is the loop-owned token-position view (SRD-028 FR-1): live trackID → its current
	// node. Seeded at spawn, advanced by evMoved, and cleared when a track dies (evEnded/evFailed/
	// evMerged-absorbed). Loop-goroutine-only — no lock, like waiting/msgIdx. The reachability and
	// join machinery read THIS map, never another track's currentStep cross-goroutine (Rule 2).
	position := map[string]flow.Node{}
	// parked is the loop-owned parked-at-join view (SRD-028 FR-3): trackID → join node, for tracks
	// suspended at a reachability/Complex join (TrackAwaitSync). Seeded on evParked from position,
	// cleared when the track resumes-and-moves, is merged away, or ends. recheckAwaitingJoins
	// iterates this map instead of scanning inst.tracks for the AwaitSync state.
	parked := map[string]flow.Node{}
	// watchers is the loop-owned boundary-subscription view (SRD-029 FR-5): trackID → the
	// boundaryWatch list armed while that track occupies a guarded activity. Armed when a track
	// arrives on an activity with interrupting boundaries (spawn / evMoved), torn down when it
	// leaves, ends, or fails. Loop-goroutine-only, like the maps above.
	watchers := map[string][]*boundaryWatch{}

	// stopAll is forward-declared so spawn (which arms boundaries and faults the
	// instance on an arm failure) can reference it; it is assigned below.
	var stopAll func()

	// spawn registers a track, adds it to the read snapshot, counts it
	// active, and runs it in its own goroutine.
	spawn := func(t *track) {
		inst.tracks[t.ID()] = t
		inst.addToSnap(t)
		active++

		// Seed the track's initial position on the loop goroutine, BEFORE its run goroutine
		// starts (the `go` below). The track has no other goroutine yet, so this read is
		// sequential — not a Rule-2 cross-read; every later move arrives as evMoved (SRD-028 FR-2).
		position[t.ID()] = t.currentStep().node

		// A track built already parked (an event-start source waiter, registered
		// during New) is recorded here — on the loop goroutine, before its run
		// goroutine starts — so it is in `waiting` (and its message defs are in msgIdx)
		// before any evDeliver can target it (SRD-027 FR-5). Mid-run waits emit evWaiting
		// from checkNodeType instead.
		if t.inState(TrackWaitForEvent) {
			waiting[t.ID()] = struct{}{}

			for _, id := range t.msgDefIDs {
				msgIdx[id] = t
			}
		}

		// Arm any interrupting boundary guarding the track's initial node — on the
		// loop goroutine, before its run goroutine starts — so the watcher exists
		// before the activity can complete or fire (SRD-029 FR-5). Subsequent moves
		// arm via evMoved. A non-activity initial node (a StartEvent) is a no-op.
		inst.armBoundaries(t, t.currentStep().node, watchers, stopAll)

		// Per-track cancellable context, derived here on the loop goroutine so
		// t.cancel is loop-owned — the loop is the sole caller that interrupts a
		// single track for an interrupting boundary (SRD-029 FR-4). inst.ctx stays
		// the parent, so instance terminate (inst.cancel) still cascades to every
		// track (NFR-4).
		tctx, cancel := context.WithCancel(ctx)
		t.cancel = cancel

		// run the track and report back to the loop. A track that reached a
		// synchronizing join without completing it ends its goroutine in
		// AwaitingMerge — reported as evAwaiting, not evEnded, so the loop keeps
		// it as awaiting (its run() will not resume).
		go func(t *track) {
			defer cancel()

			t.run(tctx)

			inst.emit(trackEvent{kind: trackEndKind(t), track: t})
		}(t)
	}

	// stopAll moves the instance to Terminating (once) and signals every
	// live track to stop.
	stopAll = func() {
		if stopping {
			return
		}

		stopping = true
		inst.setState(Terminating)

		for _, t := range inst.tracks {
			t.stop()
			// Cancel the track context so a running ctx-honoring activity (a ServiceTask
			// blocked in Exec) is interrupted — stopIt is only checked between nodes, not
			// mid-Exec (SRD-030 §3.3, the cooperative-cancellation contract of SRD-029).
			// On the abort/fault paths the instance ctx is already canceled, so this is a
			// no-op there (CancelFunc is idempotent).
			t.cancel()
			// Wake a track parked on evtCh: the loop is its sole sender, so closing
			// here is safe and doubles as teardown (SRD-027 FR-7). stopIt covers the
			// between-node path, t.cancel() the running one, the closed channel the parked one.
			close(t.evtCh)
		}

		// After stop no track is dispatched to — a parked track is woken by its closed
		// channel, not an evDeliver — so drop both maps; this also prevents a send on a
		// closed evtCh.
		clear(waiting)
		clear(msgIdx)

		// The position/join view is no longer consulted once terminating (joins do not fire
		// during teardown); drop it too so it does not outlive the run (SRD-028 FR-1/FR-3).
		clear(position)
		clear(parked)
	}

	for _, t := range initial {
		spawn(t)
	}

	if active == 0 {
		inst.setState(Completed)
		return
	}

	done := ctx.Done()
	for active > 0 {
		select {
		case <-done:
			done = nil
			stopAll()

		case ev := <-inst.events:
			// Lock-free attrs only (ID is immutable): this runs per event, and the
			// observability.Logger has no Enabled() gate, so the args are built even
			// at INFO. Node-level detail lives in the fire/abort logs below. A Message
			// evDeliver carries no track (FR-8), so the id is resolved defensively.
			inst.Logger().Debug("track event",
				"instance", inst.ID(),
				"kind", ev.kind.String(),
				"track", eventTrackID(ev))

			inst.applyEvent(ctx, ev, &active, &stopping,
				waiting, msgIdx, position, parked, watchers, spawn, stopAll)
		}
	}

	if stopping {
		inst.setState(Terminated)
	} else {
		inst.setState(Completed)
	}
}

// applyEvent applies one track→loop event to the loop-owned state on the loop goroutine.
// active and stopping are the loop's own counters, passed by pointer so this method and the
// spawn/stopAll closures mutate the same values; waiting is the loop-owned parked set and
// msgIdx its message-def→track index (SRD-027 FR-8); watchers is the loop-owned boundary
// subscription view (SRD-029 FR-5). Called only by loop().
func (inst *Instance) applyEvent(
	ctx context.Context,
	ev trackEvent,
	active *int,
	stopping *bool,
	waiting map[string]struct{},
	msgIdx map[string]*track,
	position, parked map[string]flow.Node,
	watchers map[string][]*boundaryWatch,
	spawn func(*track),
	stopAll func(),
) {
	switch ev.kind {
	case evFork:
		inst.spawnForks(ev, spawn, stopAll, *stopping)

	case evMoved:
		// the track advanced onto a new node — update the loop-owned position view and
		// clear any parked-at-join record (moving ⟹ not parked anymore). SRD-028 FR-2.
		// The track left its previous node, so tear down any boundaries that guarded it
		// there and arm those guarding the new node (SRD-029 FR-5/FR-6).
		inst.disarmBoundaries(ev.track.ID(), watchers)
		position[ev.track.ID()] = ev.node
		delete(parked, ev.track.ID())
		inst.armBoundaries(ev.track, ev.node, watchers, stopAll)

	case evEnded:
		*active--
		flipNotParked(ev.track, waiting, msgIdx)
		clearPosition(position, parked, ev.track)
		// the track's run() returned — its activity window (if any) is over, so
		// tear down the boundaries that guarded it (SRD-029 FR-6).
		inst.disarmBoundaries(ev.track.ID(), watchers)
		inst.recheckAwaitingJoins(position, parked, stopAll)

	case evAwaiting:
		// the track reached a synchronizing join, did not complete it, and its
		// goroutine returned — no longer active, but retained as awaiting until the
		// join fires (ADR-005 §2.4). Clear any index entry so it never outlives the track.
		// Its token is still Alive at the join, so it STAYS in position (not in parked —
		// AwaitingMerge is a Parallel join, not the AwaitSync reachability park). SRD-028 FR-6.
		*active--
		clearMsgIdx(msgIdx, ev.track)

	case evMerged:
		inst.applyMerged(ev, position, parked)
		inst.recheckAwaitingJoins(position, parked, stopAll)

	case evParked:
		inst.applyParked(ev, position, parked, stopAll, *stopping)

	case evFailed:
		inst.applyFailed(ev, active, waiting, msgIdx,
			position, parked, watchers, spawn, stopAll, *stopping)

	case evWaiting:
		// the track parked on its evtCh and is ready to receive — record it as
		// parked-and-undelivered and index its Message defs → track (SRD-027 FR-4/FR-5/
		// FR-8). Not during shutdown: a parked track is woken by its closed evtCh, not an
		// evDeliver, and recording it would risk a send on the closed channel.
		if !*stopping {
			waiting[ev.track.ID()] = struct{}{}

			for _, id := range ev.msgDefIDs {
				msgIdx[id] = ev.track
			}
		}

	case evDeliver:
		inst.dispatchToParked(ctx, ev, waiting, msgIdx)

	case evBoundary:
		// an interrupting boundary fired over its guarded activity — cancel the host
		// track and continue on the boundary's exception flow, the loop arbitrating the
		// completion-vs-fire race (SRD-029 FR-5/FR-8).
		inst.fireBoundary(ev, watchers, spawn, stopAll, *stopping)

	case evTerminate:
		// a Terminate End Event was reached — abnormally terminate the instance (SRD-030
		// FR-1). stopAll sets stopping, tears down parked/between-node tracks, and cancels
		// each track's context to interrupt a running activity. It does NOT touch active:
		// the terminate track's own evEnded (FIFO-after this event) accounts for it.
		stopAll()
	}
}

// dispatchToParked sends a fired event to its parked-and-undelivered track. The target is
// ev.track for a Signal/Timer evDeliver, or — for a track-less Message evDeliver (FR-8) —
// resolved from the fired definition's id via msgIdx (a miss is a benign drop). A message
// whose correlation does not match this conversation is gated here, on the loop goroutine —
// the sole owner of instance conversation state — and the track stays parked for the next
// message (SRD-027 §3.4 / NFR-2); Signal/Timer carry their track and are not correlated.
// On a match the flip (flipNotParked on first delivery) makes deferred choice atomic: a later
// event for the same track finds it absent and is dropped (a losing Event-Based-gateway arm
// or a duplicate fire). The loop is the sole sender to evtCh, and the single buffered slot
// keeps this send from blocking it (SRD-027 FR-3/FR-4). Called only by loop(), so it touches
// the loop-owned maps without a lock.
func (inst *Instance) dispatchToParked(
	ctx context.Context,
	ev trackEvent,
	waiting map[string]struct{},
	msgIdx map[string]*track,
) {
	tr := ev.track
	// Message (FR-8): a track-less evDeliver resolves the parked track from the fired def's id.
	if tr == nil {
		tr = msgIdx[ev.eDef.ID()]
		if tr == nil {
			return // no parked track for this message → drop
		}
	}

	if _, parked := waiting[tr.ID()]; !parked {
		return // losing arm / already delivered → drop (FR-4)
	}

	// Gate correlation only on the Message path (track == nil): a mismatch drops the event
	// and keeps the track parked for the next message (SRD-027 FR-8/NFR-2).
	if ev.track == nil && inst.validateAndAssociate(ctx, ev.eDef) {
		return // correlation mismatch — drop, keep the track parked
	}

	flipNotParked(tr, waiting, msgIdx)
	tr.evtCh <- ev.eDef
}

// flipNotParked removes tr from the parked set and clears its message-index entries — the
// atomic flip that makes deferred choice single-winner (SRD-027 FR-4/§3.4): a later event for
// tr finds it absent and is dropped. Also used on track end so no entry outlives its track.
func flipNotParked(
	tr *track,
	waiting map[string]struct{},
	msgIdx map[string]*track,
) {
	delete(waiting, tr.ID())
	clearMsgIdx(msgIdx, tr)
}

// clearMsgIdx removes every msgEDef→track entry pointing at tr, so a fired message can no
// longer resolve to a track that has flipped out of waiting or ended (SRD-027 §3.4).
func clearMsgIdx(msgIdx map[string]*track, tr *track) {
	for id, t := range msgIdx {
		if t == tr {
			delete(msgIdx, id)
		}
	}
}

// clearPosition drops tr from the loop-owned position and parked views — a dead track
// (Ended / Failed / Merged-absorbed) no longer holds a token, so it must not count as an
// occupied node or a parked-at-join arrival (SRD-028 FR-6).
func clearPosition(position, parked map[string]flow.Node, tr *track) {
	delete(position, tr.ID())
	delete(parked, tr.ID())
}

// eventTrackID returns the subject track's id for logging, or "<none>" for a track-less
// Message evDeliver (its target is resolved later via msgIdx — SRD-027 FR-8).
func eventTrackID(ev trackEvent) string {
	if ev.track == nil {
		return "<none>"
	}

	return ev.track.ID()
}

// nodeIDOf returns n.ID(), or "<none>" for a nil node — a defensive guard for log lines that
// read the loop-owned position map, where a miss yields a nil flow.Node (SRD-028 FR-5).
func nodeIDOf(n flow.Node) string {
	if n == nil {
		return "<none>"
	}

	return n.ID()
}

// trackEndKind classifies a track that returned from run() into the loop event
// kind: evAwaiting if it parked at a synchronizing join, evFailed if a node error
// left it TrackFailed (so the loop faults the instance — FIX-008), else the normal
// evEnded.
func trackEndKind(t *track) trackEventKind {
	switch {
	case t.inState(TrackAwaitingMerge):
		return evAwaiting

	case t.inState(TrackFailed):
		return evFailed

	default:
		return evEnded
	}
}

// failFromTrack surfaces a TrackFailed track's error as an instance failure: it
// records lastErr via Instance.fail (which also cancels the ctx so sibling tracks
// stop) and calls stopAll so the Terminating flag is set synchronously. When this
// is the last active track the ctx cancel alone would race the active--→loop-exit
// and the instance would settle on Completed instead of Terminated; stopAll makes
// the terminal state deterministic. Runs on the loop goroutine, the single writer
// of lastErr (FIX-008).
func (inst *Instance) failFromTrack(t *track, stopAll func()) {
	err := t.lastErr
	if err == nil {
		err = errs.New(
			errs.M("track %s failed", t.ID()),
			errs.C(errorClass, errs.OperationFailed))
	}

	inst.fail(err)
	stopAll()
}

// spawnForks builds and spawns one track per extra forked outgoing flow, runs
// each via spawn, and stops it immediately if the instance is already stopping.
// A build error is recorded and triggers stopAll. Called only from loop().
func (inst *Instance) spawnForks(
	ev trackEvent,
	spawn func(*track),
	stopAll func(),
	stopping bool,
) {
	for _, f := range ev.flows {
		nt, err := newTrack(f.Target().Node(), inst, ev.track)
		if err != nil {
			inst.lastErr.Store(&err)
			stopAll()

			return
		}

		// the new track reached its node via flow f; record it so a
		// synchronizing-join target knows the arriving incoming flow.
		nt.steps[0].inFlow = f

		inst.trackCount.Add(1)
		spawn(nt)

		if stopping {
			nt.stop()
		}
	}
}

// applyParked records a track that blocked at a reachability join (OR-join) in the
// loop-owned parked view and rechecks it (SRD-028 FR-3). Its goroutine is alive, so
// active is unchanged. Two guards:
//   - during shutdown stopAll has cleared the view and joins do not fire while
//     terminating; a late park is woken by ctx.Done(), not recorded here (mirrors
//     evWaiting);
//   - the track must still be live in the position view. If it is absent, the
//     completing arrival's evMerged was applied FIRST and already merged this
//     co-arriving track at the join (clearing its position) — its fate is settled, so
//     recording it now would re-insert a stale parked entry.
//
// Extracted from applyEvent to keep that switch under the complexity limit. Called
// only from applyEvent.
func (inst *Instance) applyParked(
	ev trackEvent,
	position, parked map[string]flow.Node,
	stopAll func(),
	stopping bool,
) {
	if stopping {
		return
	}

	if _, live := position[ev.track.ID()]; !live {
		return
	}

	parked[ev.track.ID()] = ev.node
	inst.recheckParked(ev.track, position, parked, stopAll)
}

// applyFailed handles a track failure on the loop goroutine (SRD-029 FR-9). It
// first tries to catch a typed BpmnError at an Error boundary on the failing
// activity (matchErrorBoundary, run before clearPosition so position still holds
// the failing node): a match routes to the boundary's exception flow and the
// instance runs on. Only an uncaught failure faults the instance (FIX-008). Then
// the track is cleared from the loop-owned views and its boundaries disarmed.
// Called only from applyEvent.
func (inst *Instance) applyFailed(
	ev trackEvent,
	active *int,
	waiting map[string]struct{},
	msgIdx map[string]*track,
	position, parked map[string]flow.Node,
	watchers map[string][]*boundaryWatch,
	spawn func(*track),
	stopAll func(),
	stopping bool,
) {
	if !inst.matchErrorBoundary(ev.track, position, spawn, stopAll, stopping) {
		inst.failFromTrack(ev.track, stopAll)
	}

	*active--
	flipNotParked(ev.track, waiting, msgIdx)
	clearPosition(position, parked, ev.track)
	inst.disarmBoundaries(ev.track.ID(), watchers)
}

// applyMerged flips the tracks the surviving track absorbed at a synchronizing
// join to Merged (their token projects Consumed). It resolves the absorbed ids
// against the loop-owned tracks map; the awaiting goroutines have already
// returned, so the loop is the sole writer of their state. Called only from
// loop().
func (inst *Instance) applyMerged(
	ev trackEvent, position, parked map[string]flow.Node,
) {
	survivor := ev.track.ID()

	for _, id := range ev.mergedIDs {
		m := inst.tracks[id]
		if m == nil {
			continue
		}

		m.mergedInto.Store(&survivor)
		m.updateState(TrackMerged)

		// the absorbed track is now dead (Merged) — drop it from the loop-owned position/
		// parked views so it stops counting as occupied or parked (SRD-028 FR-6).
		clearPosition(position, parked, m)

		// Wake the merged track unconditionally (FIX-006). If it is parked at a
		// reachability/Complex join (AwaitSync) it resumes and returns; if it has
		// not yet reached the park select, the buffered(1) signal waits for it; if
		// it already returned (Parallel AwaitingMerge) the signal is simply never
		// read. Gating this on "is it AwaitSync now" races the track's own
		// transition into AwaitSync and could miss it, hanging the instance.
		m.parkCh <- struct{}{}
	}
}

// recheckAwaitingJoins re-evaluates every reachability join currently holding a
// parked (AwaitSync) track — the death-trigger: a token death can make an
// un-marked incoming flow unreachable and fire a join that has no further arrival
// to ride (SRD-022 §2.10, fixing Camunda 7's arrival-only hang). Called only from
// loop() on a track end / merge.
func (inst *Instance) recheckAwaitingJoins(
	position, parked map[string]flow.Node, stopAll func(),
) {
	seen := map[string]bool{}

	// parked holds exactly the AwaitSync tracks (id → join node), so the loop reads its own
	// view instead of scanning inst.tracks for the state cross-goroutine (SRD-028 FR-3).
	for _, node := range parked {
		if seen[node.ID()] {
			continue
		}

		seen[node.ID()] = true
		inst.recheckJoin(node, position, parked, stopAll)
	}
}

// recheckParked handles a track that just parked at a reachability join. If the join
// already fired without recording this track (a late arrival deemed unreachable by an
// earlier fire — FIX-006), the track is a trailing token: consume it (flip to Merged
// and wake it so its goroutine returns). Otherwise recheck the join — a never-taken
// branch may already be unreachable, firing it now with no token death.
func (inst *Instance) recheckParked(
	t *track, position, parked map[string]flow.Node, stopAll func(),
) {
	// the join node is the one the track parked on, recorded in the loop-owned parked view
	// by the evParked case — no currentStep cross-read (SRD-028 FR-5).
	node := parked[t.ID()]

	if rj, ok := node.(exec.ReachabilityJoin); ok && rj.IsTrailing(t.ID()) {
		t.updateState(TrackMerged)
		// a trailing token is consumed (Merged) — drop it from the position/parked views so
		// it stops counting, matching today's "Merged ⇒ excluded" (SRD-028 FR-6).
		clearPosition(position, parked, t)
		t.parkCh <- struct{}{}

		return
	}

	inst.recheckJoin(node, position, parked, stopAll)
}

// recheckJoin re-evaluates a parked reachability join (OR-join) against the live
// token positions and fires it when no un-marked incoming flow can still receive
// a token (SRD-022 §2.10), or — for a complex (activation) join — aborts the instance
// when the rule is unsatisfiable (SRD-023). stopAll is the loop's terminate action,
// invoked on an abort so termination does not race the resulting track-end events
// (inst.fail alone only cancels ctx, leaving `stopping` unset). Called only from loop().
func (inst *Instance) recheckJoin(
	node flow.Node, position, parked map[string]flow.Node, stopAll func(),
) {
	// The loop-owned position/parked maps drive BOTH the in-transit guard and the reachability
	// the decision below uses (joinPositions), so they can't disagree — and neither reads a
	// track cross-goroutine (SRD-028 FR-4). An imminent arrival — a live token already on the
	// join node but not yet parked (between its evMoved onto the join and its evParked) — is
	// invisible to the backward reachability (it sits at the excluded join) and is not yet
	// marked. Defer: it re-triggers this recheck via its own evParked once it parks.
	occupied, inTransit := joinPositions(node, position, parked)
	if inTransit {
		return
	}

	fc := fixedFlowChecker{occupied: occupied}

	switch j := node.(type) {
	case exec.ActivationJoin:
		// Complex gateway (ADR-005 v.3 §2.11 / SRD-023): the loop owns the
		// fire/abort decision (with guard evaluation). A death can only make the
		// activation unsatisfiable — never newly fire it — so the abort path lives
		// here; firing resumes the parked survivor via fireOrJoin.
		dec, err := j.Recheck(inst.guardEval(inst.ctx), fc)

		switch {
		case err != nil:
			inst.fail(err)
			stopAll()

		case dec.Aborted:
			inst.fail(
				errs.New(
					errs.M("complex gateway activation rule is unsatisfiable"),
					errs.C(errorClass, errs.InvalidState),
					errs.D("node_id", node.ID())))
			stopAll()

		case dec.Fired:
			inst.fireOrJoin(dec.Survivor, dec.Merged, position, parked)
		}

	case exec.ReachabilityJoin:
		if complete, survivor, merged := j.Recheck(fc); complete {
			inst.fireOrJoin(survivor, merged, position, parked)
		}
	}
}

// fireOrJoin completes a reachability join: applyMerged flips the absorbed tracks to
// Merged and wakes any that are parked (FR-8 / FIX-006); here we only resume the
// survivor's blocked goroutine into the node. parkCh is buffered(1), so the signal
// never blocks the loop. Called only from loop().
func (inst *Instance) fireOrJoin(
	survivorID string, merged []string, position, parked map[string]flow.Node,
) {
	survivor := inst.tracks[survivorID]
	if survivor == nil {
		return
	}

	inst.Logger().Debug("synchronizing join fired",
		"instance", inst.ID(),
		"node", nodeIDOf(position[survivorID]),
		"survivor", survivorID,
		"merged", len(merged))

	inst.applyMerged(trackEvent{track: survivor, mergedIDs: merged}, position, parked)

	survivor.parkCh <- struct{}{}
}

// addToSnap appends a track to the lock-free tracks snapshot (copy-on-write).
// Called only from loop() (the single writer); readers Load the snapshot.
func (inst *Instance) addToSnap(t *track) {
	old := inst.tracksSnap.Load()

	var base []*track
	if old != nil {
		base = *old
	}

	next := make([]*track, len(base), len(base)+1)
	copy(next, base)
	next = append(next, t)

	inst.tracksSnap.Store(&next)
}

// GetTokens returns the projected tokens of the instance's ACTIVE tracks
// (those whose token is Alive or WaitForEvent), derived lock-free from the
// tracks snapshot.
func (inst *Instance) GetTokens() []Token {
	snap := inst.tracksSnap.Load()
	if snap == nil {
		return nil
	}

	out := make([]Token, 0, len(*snap))
	for _, t := range *snap {
		tok := t.Token()
		if tok.State == TokenAlive || tok.State == TokenWaitForEvent {
			out = append(out, tok)
		}
	}

	return out
}

// TokenHistory returns the token-flow path history of the instance — one path
// per track (live and ended), stitched by track lineage — derived lock-free
// from the tracks snapshot and each track's recorded transitions.
func (inst *Instance) TokenHistory() []TokenPath {
	snap := inst.tracksSnap.Load()
	if snap == nil {
		return nil
	}

	out := make([]TokenPath, 0, len(*snap))
	for _, t := range *snap {
		out = append(out, t.path())
	}

	return out
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

// RuntimeVar implements scope.RuntimeVarsSupplier: the data plane delegates
// reads under the reserved RUNTIME subtree here, so every read observes the
// live engine state (SRD-007 FR-9).
func (inst *Instance) RuntimeVar(name string) (data.Data, error) {
	var d data.Value

	switch name {
	case StartedAt:
		d = values.NewVariable(inst.startTime)

	case CurrState:
		d = values.NewVariable(inst.State())

	case TracksCount:
		tc := int(inst.trackCount.Load())
		d = values.NewVariable(tc)

	default:
		return nil,
			fmt.Errorf("invalid runtime variable name %q", name)
	}

	id, err := data.NewItemDefinition(d)
	if err != nil {
		return nil,
			fmt.Errorf(
				"couldn't create an ItemDefinition for runtime variable %q: %w",
				name, err)
	}

	iae, err := data.NewItemAwareElement(id, data.ReadyDataState)
	if err != nil {
		return nil,
			fmt.Errorf(
				"couldn't create an ItemAwareElement for runtime variable %q: %w",
				name, err)
	}

	p, err := data.NewParameter(name, iae)
	if err != nil {
		return nil,
			fmt.Errorf(
				"couldn't create an ItemDefinition for runtime variable %q: %w",
				name, err)
	}

	return p, nil
}

// RuntimeVarNames implements scope.RuntimeVarsSupplier: it lists the runtime
// variables the instance exposes under the RUNTIME source.
func (inst *Instance) RuntimeVarNames() []string {
	return []string{StartedAt, CurrState, TracksCount}
}

// -------------------- exec.EventProducer interface ---------------------------

// RegisterEvent register tracks awaited for the event.
// Once event is fired, then track's EventProcessor called.
func (inst *Instance) RegisterEvent(
	proc eventproc.EventProcessor,
	eDef flow.EventDefinition,
) error {
	// Validate the arguments BEFORE the state-specific branch below builds a
	// diagnostic from proc.ID() — a nil processor must not panic the terminal
	// path (FIX-010).
	if proc == nil {
		return errs.New(
			errs.M("empty EventProcessor"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if eDef == nil {
		return errs.New(
			errs.M("empty EventDefinition"),
			errs.C(errorClass, errs.EmptyNotAllowed, errs.InvalidParameter))
	}

	// Event registration is legitimate while the instance is being built
	// (Created — start-event nodes register here) or running (Active — boundary
	// / intermediate catch events); it is refused only on a terminal instance
	// that can no longer act on a fired event (FIX-002 RC1).
	is := inst.State()
	if is != Created && is != Active {
		return errs.New(
			errs.M("instance is terminal, can't register events (state: %s)",
				is),
			errs.C(errorClass, errs.InvalidState),
			errs.D("requester_id", proc.ID()))
	}

	if inst.parentEventProducer == nil {
		return errs.New(
			errs.M("no registered EventProducer"),
			errs.C(errorClass, errs.InvalidObject))
	}

	return inst.parentEventProducer.RegisterEvent(
		proc, eDef)
}

// UnregisterEvent removes the eDefID-to-proc subscription, mirroring
// RegisterEvent: it validates its arguments and delegates to the parent
// EventProducer.
//
// It is idempotent: if the parent reports the waiter or the processor is
// already gone (ObjectNotFound), the desired end state — proc no longer
// subscribed to eDefID — is already reached, so it returns nil. This keeps
// the fired-event flow working, where the waiter self-removes before the
// track unregisters (track.go unregisterEvent). Resolving who OWNS the
// waiter's lifecycle (the hub vs the waiter) is ADR-006's concern; this is
// the interim seam.
func (inst *Instance) UnregisterEvent(
	proc eventproc.EventProcessor,
	eDefID string,
) error {
	if proc == nil {
		return errs.New(
			errs.M("empty EventProcessor"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if strings.TrimSpace(eDefID) == "" {
		return errs.New(
			errs.M("empty event definition id"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if inst.parentEventProducer == nil {
		return errs.New(
			errs.M("no registered EventProducer"),
			errs.C(errorClass, errs.InvalidObject))
	}

	err := inst.parentEventProducer.UnregisterEvent(proc, eDefID)

	var ae *errs.ApplicationError
	if errors.As(err, &ae) && ae.HasClass(errs.ObjectNotFound) {
		return nil
	}

	return err
}

// PropagateEvent sends a fired throw event's eventDefinition
// up to chain of EventProducers
func (inst *Instance) PropagateEvent(
	ctx context.Context,
	eDef flow.EventDefinition,
) error {
	st := inst.State()
	if st != Active {
		return errs.New(
			errs.M("instance isn't active"),
			errs.C(errorClass, errs.InvalidState),
			errs.D("current_state", st),
			errs.D("instance_id", inst.ID()))
	}

	if err := inst.parentEventProducer.PropagateEvent(ctx, eDef); err != nil {
		return errs.New(
			errs.M("event propagation failed"),
			errs.C(errorClass, errs.OperationFailed),
			errs.D("event_definition_id", eDef.ID()),
			errs.D("event_definition_type", eDef.Type()),
			errs.E(err))
	}

	return nil
}

// ------------------ instance identity & services -----------------------------

// InstanceID returns ID of the Instance.
func (inst *Instance) InstanceID() string {
	return inst.ID()
}

// EventProducer returns the EventProducer of the runtime.
func (inst *Instance) EventProducer() eventproc.EventProducer {
	return inst
}

// RenderRegistrator returns the render registrator for the instance.
func (inst *Instance) RenderRegistrator() interactor.Registrator {
	return inst.rr
}

// =============================================================================
// Interfaces check
var (
	_ eventproc.EventProducer   = (*Instance)(nil)
	_ scope.RuntimeVarsSupplier = (*Instance)(nil)
)
