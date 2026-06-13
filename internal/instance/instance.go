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
	"sync/atomic"
	"time"

	"golang.org/x/exp/maps"

	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/interactor"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
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
	// EngineRuntime is the Thresher's resolved engine-level extension set,
	// embedded so node executors reach Logger()/Clock()/Repository()/... via
	// the RuntimeEnvironment without per-method delegates.
	engrenv.EngineRuntime
	rr                  interactor.Registrator
	parentEventProducer eventproc.EventProducer
	// dataPlane is the instance's container-scope tree — the single
	// authority for persistent process data (ADR-010 §2.2). It owns its own
	// serialization; the instance never holds live data itself.
	dataPlane *scope.Scope
	// tracks is the live track registry, owned by loop() (not guarded by m).
	tracks map[string]*track
	// tracksSnap is a copy-on-write snapshot for lock-free GetTokens /
	// TokenHistory reads.
	tracksSnap atomic.Pointer[[]*track]
	// lastErr is a fatal fork-construct error, if any.
	lastErr   atomic.Pointer[error]
	s         *snapshot.Snapshot
	events    chan trackEvent // tracks -> loop()
	loopDone  chan struct{}   // closed when loop() exits
	now       func() time.Time
	rootScope scope.DataPath
	foundation.BaseElement
	trackCount atomic.Int64
	state      atomic.Uint32 // written only by loop(), read via State()
}

// New creates a new Instance from the Snapshot s and sets state to Created.
// parentRoot is the container-scope path the instance's root scope attaches
// under (sub-process / call-activity nesting, future); scope.EmptyDataPath
// roots the instance at the top.
func New(
	s *snapshot.Snapshot,
	parentRoot scope.DataPath,
	er engrenv.EngineRuntime,
	ep eventproc.EventProducer,
	rr interactor.Registrator,
) (*Instance, error) {
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
		loopDone:            make(chan struct{}),
		parentEventProducer: ep,
		rr:                  rr,
	}
	inst.state.Store(uint32(Created))

	if err := inst.loadProperties(parentRoot); err != nil {
		return nil, errs.New(
			errs.M("couldn't load process'es properties into Instance scope"),
			errs.E(err),
			errs.C(errorClass, errs.BulidingFailed),
			errs.D("process_name", s.ProcessName),
			errs.D("process_id", s.ProcessID))
	}

	if err := inst.createTracks(); err != nil {
		return nil, err
	}

	// TracksCount reflects all tracks created (initial + forks); seed it with
	// the initial tracks. The loop adds forks; ended tracks are retained.
	inst.trackCount.Store(int64(len(inst.tracks)))

	return &inst, nil
}

// loadProperties creates the instance's data plane rooted under parentRoot
// and commits the process properties into the root container scope.
func (inst *Instance) loadProperties(parentRoot scope.DataPath) error {
	root := parentRoot
	if root == scope.EmptyDataPath {
		root = scope.RootDataPath
	}

	var err error

	inst.rootScope, err = root.Append(inst.s.ProcessName)
	if err != nil {
		return fmt.Errorf("couldn't create instance's scope data path: %w", err)
	}

	inst.dataPlane, err = scope.New(inst.rootScope, inst)
	if err != nil {
		return fmt.Errorf("couldn't create instance's data plane: %w", err)
	}

	dd := make([]data.Data, 0, len(inst.s.Properties))
	for _, p := range inst.s.Properties {
		dd = append(dd, p)
	}

	return inst.dataPlane.Commit(inst.rootScope, dd...)
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
}

// LastErr returns the fatal error that stopped the instance (e.g. a fork
// whose target node could not be constructed), or nil. Set only by loop().
func (inst *Instance) LastErr() error {
	if e := inst.lastErr.Load(); e != nil {
		return *e
	}

	return nil
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

	inst.ctx = ctx
	inst.startTime = inst.now()
	inst.setState(Active)

	// initial tracks were built by createTracks() during New; hand them to the
	// loop, which becomes the sole owner of lifecycle state from here on.
	initial := maps.Values(inst.tracks)

	go inst.loop(ctx, initial)

	return nil
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

	// spawn registers a track, adds it to the read snapshot, counts it
	// active, and runs it in its own goroutine.
	spawn := func(t *track) {
		inst.tracks[t.ID()] = t
		inst.addToSnap(t)
		active++

		// run the track and report back to the loop. A track that reached a
		// synchronizing join without completing it ends its goroutine in
		// AwaitingMerge — reported as evAwaiting, not evEnded, so the loop keeps
		// it as awaiting (its run() will not resume).
		go func(t *track) {
			t.run(ctx)

			kind := evEnded
			if t.inState(TrackAwaitingMerge) {
				kind = evAwaiting
			}

			inst.emit(trackEvent{kind: kind, track: t})
		}(t)
	}

	// stopAll moves the instance to Terminating (once) and signals every
	// live track to stop.
	stopAll := func() {
		if stopping {
			return
		}

		stopping = true
		inst.setState(Terminating)

		for _, t := range inst.tracks {
			t.stop()
		}
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
			switch ev.kind {
			case evFork:
				inst.spawnForks(ev, spawn, stopAll, stopping)

			case evEnded:
				active--

			case evAwaiting:
				// the track reached a synchronizing join, did not complete it,
				// and its goroutine returned — no longer active, but retained as
				// awaiting until the join fires (ADR-005 §2.4).
				active--

			case evMerged:
				inst.applyMerged(ev)
			}
		}
	}

	if stopping {
		inst.setState(Terminated)
	} else {
		inst.setState(Completed)
	}
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

// applyMerged flips the tracks the surviving track absorbed at a synchronizing
// join to Merged (their token projects Consumed). It resolves the absorbed ids
// against the loop-owned tracks map; the awaiting goroutines have already
// returned, so the loop is the sole writer of their state. Called only from
// loop().
func (inst *Instance) applyMerged(ev trackEvent) {
	for _, id := range ev.mergedIDs {
		if m := inst.tracks[id]; m != nil {
			m.updateState(TrackMerged)
		}
	}
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
func (inst *Instance) createTracks() error {
	for _, n := range inst.s.Nodes {
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
