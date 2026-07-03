// Track represents a single flow of process.
// Every process has one or a few entry points (event, nodes with no
// incoming sequence flow). Those entry points becomes a begin of
// track.
//
// Track starts execution from a start node.
//
//   - If node awaits an evenet to continue, then it event definition
//     registered in instance and track state becomes to TrackAwaitEvent.
//     Once event sent to track via ProcessEvent, then track continues.
//
//   - Node execution is a single Execute step: the track loads the node's
//     incoming data, runs the node's Exec, and uploads its outgoing data. On
//     success Exec returns a list of outgoing flows.
//
// If number of outgouing flows is not zero, then they processed as followed:
//
//   - first flow becomes the next step of the track.
//     If there is a cyclic flow to node itself, then the first of them would
//     be the next step of the track. If there is more than on cyclic flow,
//     goBpm has no mechanism to set priority between them.
//
//   - for the rest of the outgoing flows new tracks would be created and
//     added to the instance.
//
//   - token in the track would split on number of flows and first one will
//     assign to the track itself in next step, and the rest of them will
//     be set to the others child tracks.
//
// if there is no outgouing flows, then track ends and token died.
//
// ## Human interaction
//
// If node nedds to interacto with the human, then it should support
//

package instance

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/exec"
	"github.com/dr-dobermann/gobpm/pkg/interactor"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// trackState represent the state of the whole track
type trackState uint8

// Track state constants define the possible states of a track during
// process execution.
const (
	// TrackCreated represents a newly created track
	TrackCreated trackState = iota
	// TrackReady represents a track ready for execution
	TrackReady

	// Intermediate
	// TrackExecutingStep represents a track currently executing a step
	TrackExecutingStep
	// TrackProcessStepResults represents a track processing step results
	TrackProcessStepResults
	// TrackWaitForEvent represents a track waiting for an event
	TrackWaitForEvent
	// TrackAwaitingMerge represents a track that reached a synchronizing join,
	// did not complete it, and whose goroutine has returned — it is retained as
	// a record until the join fires (ADR-005 §2.4). Its token projects Alive.
	TrackAwaitingMerge
	// TrackAwaitSync is the reachability-join (OR-join) counterpart of
	// TrackAwaitingMerge: the track parked at a converging InclusiveGateway and
	// may yet be **resumed** as the survivor (or merged away) once the loop's
	// reachability recheck fires the join (SRD-022). Its token projects Alive.
	TrackAwaitSync

	// Final statuses
	// TrackMerged represents a track that has been merged
	TrackMerged
	// TrackEnded represents a track that has ended normally
	TrackEnded
	// TrackCanceled represents a track that has been canceled
	TrackCanceled
	// TrackFailed represents a track that has failed
	TrackFailed
)

// String returns the human-readable name of the track state.
func (t trackState) String() string {
	return []string{
		"TrackCreated",
		"TrackReady",
		"TrackExecutingStep",
		"TrackProcessStepResults",
		"TrackWaitForEvent",
		"TrackAwaitingMerge",
		"TrackAwaitSync",
		"TrackMerged",
		"TrackEnded",
		"TrackCanceled",
		"TrackFailed",
	}[t]
}

// stepState describes the state of the single tack step currently executing
// or previously executed.
type stepState uint8

const (
	// StepCreated represents a newly created step state.
	StepCreated stepState = iota
	// StepStarted represents a step that has been started.
	StepStarted
	// StepExecuting represents a step that is currently executing.
	StepExecuting
	// StepAwaitsResults represents a step awaiting results.
	StepAwaitsResults
	// StepEnded represents a step that has ended.
	StepEnded
	// StepFailed represents a step that has failed.
	StepFailed
)

// String returns the human-readable name of the step state.
func (ss stepState) String() string {
	return []string{
		"Created",
		"Started",
		"Executing",
		"AwaitsResults",
		"Ended",
		"Failed",
	}[ss]
}

// stepInfo keeps information about single track step
type stepInfo struct {
	node flow.Node
	// inFlow is the sequence flow the track traversed to reach this node (nil
	// for an entry node). A synchronizing join reads its id to record which
	// incoming flow delivered the arriving token.
	inFlow *flow.SequenceFlow
	state  stepState
}

// track processed single line of the process from start noed or
// from fork of sequence flow.
type track struct {
	ctx context.Context
	// cancel cancels THIS track's context only (a child of the instance context),
	// so the loop can interrupt a single guarded track for an interrupting
	// boundary without touching its siblings (SRD-029 FR-4). Loop-owned: derived
	// and stored by the loop's spawn, called only by the loop. nil until spawned.
	cancel   context.CancelFunc
	lastErr  error
	instance *Instance
	foundation.BaseElement
	// prev is the lineage of this track: the ids of the ancestor tracks it
	// descends from (forks) or absorbed (synchronizing-join merges). Ids, not
	// pointers, so a survivor does not retain the merged-away (dead) tracks.
	prev []string
	// hist is the append-only list of track-state transitions (SRD-001 M3).
	// It is written only by this track's goroutine via record() and published
	// copy-on-write, so token projection / path history / timing are derived
	// from it lock-free by any reader.
	hist atomic.Pointer[[]stepUpdate]
	// mergedInto is the survivor track id this track was absorbed into at a
	// synchronizing join. The loop is the sole writer (the absorbed track's own
	// goroutine has already returned); path() readers are concurrent, so it is
	// atomic like hist. nil until merged. SRD-022 FR-8.
	mergedInto atomic.Pointer[string]
	// parkCh resumes a track blocked at a reachability join (OR-join): the track
	// suspends its goroutine on it and the loop signals once it has decided the
	// track's fate (survivor → proceed, merged → return). Buffered(1) so the
	// loop never blocks on the signal. SRD-022.
	parkCh chan struct{}
	// evtCh delivers a fired event to this track while it is parked in TrackWaitForEvent
	// (SRD-027 FR-1). The per-instance loop is the SOLE sender and sole closer; the track
	// only receives. Buffered to one slot (eventBufferDepth) so the loop never blocks on the
	// send — with flip-on-dispatch the loop delivers at most one event per parked episode.
	evtCh chan flow.EventDefinition
	// taskID is the engine-minted id of a UserTask this track parks on as a human
	// task (SRD-034). Set once by checkNodeType before parking; read by spawn on
	// the loop goroutine (sequentially, before the run goroutine starts) so a task
	// parked at construction is registered. Empty for a non-UserTask wait.
	taskID string
	steps  []*stepInfo
	// msgDefIDs are the ids of the Message catch definitions this track parks on, set by
	// checkNodeType at construction (SRD-027 FR-8). The loop indexes them → this track so a
	// fired message resolves back to it; spawn reads them for a track that starts parked
	// before the loop drains events. Construction-immutable, so the loop reads it lock-free.
	msgDefIDs []string
	m         sync.RWMutex
	state     trackState
	stopIt    atomic.Bool
}

// record appends a track-state transition to the history, copy-on-write, and
// publishes it atomically. It runs on the track's own run goroutine, and also on
// the loop goroutine when the loop finalizes a QUIESCENT merged track
// (applyMerged / recheckParked -> updateState(TrackMerged) -> record). That track's
// own goroutine has already returned (AwaitingMerge) or is suspended on parkCh
// (AwaitSync), so the loop-side read of t.steps is ordered after the track's last
// write by the emit / parkCh handoff (ADR-001 single-writer of a quiescent track,
// SRD-028 §3.6); t.m guards that read uniformly with the track's own appends.
func (t *track) record(state trackState) {
	t.m.RLock()
	node := t.steps[len(t.steps)-1].node
	t.m.RUnlock()

	old := t.hist.Load()

	var base []stepUpdate
	if old != nil {
		base = *old
	}

	at := t.instance.now()

	next := make([]stepUpdate, len(base), len(base)+1)
	copy(next, base)
	next = append(next, stepUpdate{node: node, state: state, at: at})

	t.hist.Store(&next)

	// Publish the transition to host observers (SRD-018): identity + projected
	// token state only, never payload. Non-blocking at the sink; a no-op when
	// no one is observing.
	t.instance.notify(ObsNodeProgress, node.ID(), node.Name(),
		tokenStateFor(state).String())
}

// Token returns the track's current token projection (lock-free).
func (t *track) Token() Token {
	h := t.hist.Load()
	if h == nil || len(*h) == 0 {
		return Token{}
	}

	last := (*h)[len(*h)-1]

	return Token{Node: last.node, State: tokenStateFor(last.state)}
}

// path returns the recorded token path of this track (lock-free).
func (t *track) path() TokenPath {
	parent := ""
	if n := len(t.prev); n != 0 {
		parent = t.prev[n-1]
	}

	tp := TokenPath{TrackID: t.ID(), ParentID: parent}

	if mi := t.mergedInto.Load(); mi != nil {
		tp.MergedInto = *mi
	}

	h := t.hist.Load()
	if h != nil {
		for _, u := range *h {
			ts := tokenStateFor(u.state)
			tp.Steps = append(tp.Steps, StepVisit{Node: u.node, At: u.at, State: ts})
			tp.Terminal = ts
		}
	}

	return tp
}

// eventBufferDepth is the per-track inbound event-channel (evtCh) capacity. One slot is
// exactly enough: the loop dispatches at most one event per parked episode (it removes the
// track from its waiting set on first delivery), and a single slot decouples the loop's send
// from the track's scheduling so the loop never blocks. Unbuffered would risk blocking the loop
// in the window between the track's evWaiting and its receive. SRD-027 §3.6.
const eventBufferDepth = 1

// newTrack creates the new track from the start flow.Node and sets it
// in TrackReady state.
// newTrack retruns created track's pointer on success or error on failure.
//
// For processes initial nodes, token is empty. It creates only if node
// gets control over event or direct execution.
func newTrack(
	start flow.Node,
	inst *Instance,
	prevTrack *track,
) (*track, error) {
	_, ok := start.(exec.NodeExecutor)
	if !ok {
		return nil, errs.New(
			errs.M("%q node hasn't NodeExecutor interface", start.Name()),
			errs.C(errorClass, errs.TypeCastingError))
	}

	if inst == nil {
		return nil,
			errs.New(
				errs.M("no instance"),
				errs.C(errorClass, errs.EmptyNotAllowed))
	}

	be, err := foundation.NewBaseElement()
	if err != nil {
		return nil, fmt.Errorf("failed to create base element for track: %w", err)
	}

	t := track{
		BaseElement: *be,
		prev:        []string{},
		steps: []*stepInfo{
			{
				node:  start,
				state: StepCreated,
			},
		},
		state:    TrackReady,
		instance: inst,
		parkCh:   make(chan struct{}, 1),
		evtCh:    make(chan flow.EventDefinition, eventBufferDepth),
	}

	if prevTrack != nil {
		t.prev = append(t.prev, append(prevTrack.prev, prevTrack.ID())...)
	}

	// History is recorded once the track runs (per-node visits + state
	// transitions), so it uses the running clock; before Run, Token() returns
	// the zero projection. checkNodeType below may add a WaitForEvent entry
	// for event-start nodes.
	if err := t.checkNodeType(start); err != nil {
		return nil, err
	}

	return &t, nil
}

// checkNodeType determines if node awaits for event or human interaction
// and updates track state on positive comparison.
func (t *track) checkNodeType(node flow.Node) error {
	// A UserTask is a human-interaction wait node (SRD-034): it parks for a human
	// Complete, not for a hub-delivered event. Recognize it before the event-node
	// path (it is not a flow.EventNode) and park it without any hub registration.
	if _, ok := node.(interactor.HumanTask); ok {
		return t.parkHumanTask(node)
	}

	en, ok := node.(flow.EventNode)
	if !ok {
		return nil
	}

	// Only a node that can PROCESS a fired event waits for one. A throw event
	// (EndEvent, IntermediateThrowEvent) is a flow.EventNode but not an
	// eventproc.EventProcessor — it emits its definitions in Exec and must not
	// be parked as a waiter for the message it is about to throw.
	if _, ok := node.(eventproc.EventProcessor); !ok {
		return nil
	}

	defs := en.Definitions()
	if len(defs) == 0 {
		return nil
	}

	// Record the Message catch-definition ids so the loop can index them → this track
	// (SRD-027 FR-8): carried in the evWaiting emit below for a mid-run wait, and read by
	// spawn for a track that starts parked before the loop drains events.
	t.msgDefIDs = messageDefIDs(defs)

	// Declare the wait BEFORE registering: a waiter may deliver an event
	// synchronously on registration (a MessageWaiter draining a message the
	// broker already buffered fires at once), and ProcessEvent only accepts an
	// event while the track is in TrackWaitForEvent. Setting the state first
	// removes that race; timers, which fire later, are unaffected.
	t.updateState(TrackWaitForEvent)

	// Tell the loop this track is parked BEFORE registering its waiters, so a fired
	// event (dispatched by the loop as evDeliver) can never reach the loop before the
	// track is recorded as parked-and-undelivered (SRD-027 FR-5). The emit carries the
	// Message catch-definition IDs so the loop can index them → this track (FR-8). Gated
	// on Active: at construction (New, before the loop runs) the loop records the track
	// via spawn instead, and emitting here would block on the not-yet-draining inst.events.
	if t.instance.State() == Active {
		t.instance.emit(trackEvent{
			kind:      evWaiting,
			track:     t,
			msgDefIDs: t.msgDefIDs,
		})
	}

	// Per-trigger registration is the one place the hybrid boundary is chosen (SRD-027
	// FR-8 / §3.7): a Message catch registers the Instance (it owns correlation), every
	// other trigger registers the track.
	for _, d := range defs {
		proc := eventproc.EventProcessor(t)
		if d.Type() == flow.TriggerMessage {
			proc = t.instance
		}

		if err := t.instance.RegisterEvent(proc, d); err != nil {
			return errs.New(
				errs.M("couldn't register event definitions"),
				errs.C(errorClass, errs.BulidingFailed),
				errs.D("node_id", en.ID()),
				errs.D("node_name", en.Name()),
				errs.D("event_definition_id", d.ID()),
				errs.E(err))
		}
	}

	return nil
}

// parkHumanTask parks the track on a UserTask (SRD-034): it mints a task id, marks
// the track WaitForEvent (so run parks it on evtCh), and — when the loop is running
// — emits evTaskWaiting so the loop registers the task and announces it to the
// TaskDistributor. At construction the loop isn't draining events yet, so spawn
// reads t.taskID and registers it instead (mirroring evWaiting's construction
// path). The UserTask registers NO hub waiter — completion arrives via Complete,
// delivered to evtCh as a synthetic event, not fired through the hub.
func (t *track) parkHumanTask(node flow.Node) error {
	t.taskID = foundation.GenerateID()

	t.updateState(TrackWaitForEvent)

	if t.instance.State() == Active {
		t.instance.emit(trackEvent{
			kind:   evTaskWaiting,
			track:  t,
			node:   node,
			taskID: t.taskID,
		})
	}

	return nil
}

// messageDefIDs returns the ids of the Message-triggered definitions in defs (SRD-027
// FR-8): the loop indexes these → the parked track so a fired message resolves back to
// it. Returns nil when none are Message-triggered (a Signal/Timer-only wait).
func messageDefIDs(defs []flow.EventDefinition) []string {
	var ids []string

	for _, d := range defs {
		if d.Type() == flow.TriggerMessage {
			ids = append(ids, d.ID())
		}
	}

	return ids
}

// inState checks if track state is equal to any track state from the ss.
// inState reports whether the track's current state is any of ss.
func (t *track) inState(ss ...trackState) bool {
	t.m.RLock()
	state := t.state
	t.m.RUnlock()

	return slices.Contains(ss, state)
}

// updateState sets new state for the track if its not in final state.
// If track has a token, its state will be updated accordingly.
func (t *track) updateState(newState trackState) {
	t.m.RLock()
	state := t.state
	t.m.RUnlock()

	if state == newState {
		return
	}

	t.m.Lock()
	t.state = newState
	t.m.Unlock()

	// Per-node Executing entries are recorded in prepareNodeExecution so each
	// visited node appears even when the track stays in ExecutingStep across
	// consecutive nodes; here we record the other (wait / terminal) states.
	if newState != TrackExecutingStep {
		t.record(newState)
	}
}

// currentStep returns current step of the track.
func (t *track) currentStep() *stepInfo {
	t.m.RLock()
	defer t.m.RUnlock()

	return t.steps[len(t.steps)-1]
}

// stop terminates track execution.
func (t *track) stop() {
	t.stopIt.Store(true)
}

// run start execution loop of the track which ends by ctx's cancel or
// when there is no outgoing flows from the processing nodes.
func (t *track) run(
	ctx context.Context,
) {
	if t.stopIt.Load() || !t.inState(TrackReady, TrackWaitForEvent) {
		return
	}

	t.ctx = ctx

	for {
		if t.stopIt.Load() {
			t.updateState(TrackCanceled)

			return
		}

		if t.inState(TrackWaitForEvent) {
			// Park on evtCh instead of busy-waiting (SRD-027 FR-1): the per-instance
			// loop is the SOLE sender and sole closer, so a delivered event is applied
			// on the track's OWN goroutine here — no foreign-goroutine mutation, no
			// event mutex. Zero CPU until the loop dispatches a fired event (already
			// past any correlation gate, §3.4) or closes evtCh on stop (FR-7).
			select {
			case <-ctx.Done():
				t.updateState(TrackCanceled)
				t.lastErr = ctx.Err()

				return

			case eDef, ok := <-t.evtCh:
				if !ok {
					// the loop closed evtCh on stop — terminate like a cancellation.
					t.updateState(TrackCanceled)

					return
				}

				if err := t.deliver(ctx, eDef); err != nil {
					t.lastErr = err
					t.updateState(TrackFailed)

					return
				}
			}

			continue
		}

		select {
		case <-ctx.Done():
			t.updateState(TrackCanceled)
			t.lastErr = ctx.Err()

			return

		default:
		}

		// Read the current step here, after the park: for an Event-Based gateway,
		// deliver() (on THIS goroutine, just above) advanced the track onto the winning
		// arm before returning Ready, so currentStep() observes the arm step, not the
		// stale gate step (SRD-024). Single-writer delivery removes the cross-goroutine
		// flip the old FIX-007 re-read guarded against.
		step := t.currentStep()

		// run while there is a step to take
		if step.state != StepCreated {
			// if the last step is finished
			// stop track running, inactivate token and return
			t.updateState(TrackEnded)

			return
		}

		// at a synchronizing join the node decides whether this token proceeds
		// (the completing arrival, the survivor) or waits (AwaitingMerge — the
		// goroutine returns). Synchronization settles before the node executes.
		if proceed := t.synchronize(step); !proceed {
			return
		}

		nextFlows, err := t.executeNode(ctx, step)
		if err != nil {
			t.discardOrFail(ctx, err)

			return
		}

		err = t.checkFlows(nextFlows)
		if err != nil {
			t.lastErr = err
			t.updateState(TrackFailed)

			return
		}
	}
}

// discardOrFail classifies a non-nil executeNode error (SRD-029 §3.7/§4.5): a
// canceled context is a DISCARD — a boundary fire (or instance terminate)
// interrupted the activity in its execution phase, so the track ends
// TrackCanceled and the result is abandoned; the exception flow, if any, is the
// loop's own action (it applied the fire), so the discard never needs to know
// why it was canceled. Any other error is a genuine failure (TrackFailed) for
// the loop's Error-boundary / instance-fault path.
func (t *track) discardOrFail(ctx context.Context, err error) {
	if ctx.Err() != nil {
		t.updateState(TrackCanceled)
		t.lastErr = ctx.Err()

		return
	}

	t.lastErr = err
	t.updateState(TrackFailed)
}

// synchronize handles a synchronizing-join node (ADR-005 §2.4). For a node that
// is not a synchronizing join, or has at most one incoming flow, it returns true
// (proceed) immediately. Otherwise it calls the node's atomic Arrive with the
// incoming flow this token arrived on:
//
//   - not complete: the track becomes AwaitingMerge, tells the loop (evAwaiting)
//     and returns false — the run goroutine then returns (no goroutine is kept);
//   - complete: this track is the survivor — it declares the merge (evMerged,
//     which flips the absorbed tracks to Merged) before the node executes, then
//     returns true to proceed.
//
// The survivor's prev (its creation lineage) is left untouched: a token at a
// join has many parents, but TokenPath.ParentID holds one. The convergence is
// represented by the absorbed tracks' own path entries — each terminating at
// the join, Consumed — not by folding their ids into the survivor's parent slot.
func (t *track) synchronize(step *stepInfo) (proceed bool) {
	// A converging Complex gateway (ADR-005 v.3 §2.11) is an ActivationJoin, not a
	// SynchronizingJoin — handle it on its own path.
	if aj, ok := step.node.(exec.ActivationJoin); ok &&
		len(step.node.Incoming()) > 1 {
		return t.synchronizeActivation(step, aj)
	}

	sj, ok := step.node.(exec.SynchronizingJoin)
	if !ok || len(step.node.Incoming()) <= 1 {
		return true
	}

	var inFlowID string
	if step.inFlow != nil {
		inFlowID = step.inFlow.ID()
	}

	complete, merged := sj.Arrive(inFlowID, t.ID())
	if complete {
		// the completing arrival is the survivor: declare the merge (the loop
		// flips the absorbed tracks to Merged) and proceed. prev is not touched —
		// see the doc comment on why convergence is not a parent edge.
		t.instance.emit(trackEvent{kind: evMerged, track: t, mergedIDs: merged})

		return true
	}

	// A non-completing arrival at a reachability join (OR-join) cannot decide
	// alone — the loop owns the reachability test and may yet resume this very
	// goroutine as the survivor. So it parks: suspend mid-run, blocked on parkCh,
	// until the loop signals its fate (SRD-022). A plain join (Parallel) instead
	// returns and lets the goroutine end (AwaitingMerge).
	if _, isReach := step.node.(exec.ReachabilityJoin); isReach {
		t.updateState(TrackAwaitSync)
		// Carry the join node so the loop records the park from the event itself, never
		// inferring it from its position view (SRD-028 FR-3).
		t.instance.emit(trackEvent{kind: evParked, track: t, node: step.node})

		select {
		case <-t.parkCh:
			// resumed: the survivor proceeds into the node; a track the loop
			// merged away returns (its run() ends).
			return !t.inState(TrackMerged)

		case <-t.ctx.Done():
			t.updateState(TrackCanceled)

			return false
		}
	}

	t.updateState(TrackAwaitingMerge)

	return false
}

// synchronizeActivation handles a converging Complex gateway (ADR-005 v.3 §2.11): it
// records this arrival and — unless the gateway already fired (a trailing token, then
// consumed) — parks, like the OR-join. The fire/abort decision is the loop's recheck,
// which owns reachability + guard evaluation and instance failure (a guard error or an
// unsatisfiable rule is surfaced there, the single writer of lastErr; SRD-023).
func (t *track) synchronizeActivation(
	step *stepInfo, aj exec.ActivationJoin,
) bool {
	var inFlowID string
	if step.inFlow != nil {
		inFlowID = step.inFlow.ID()
	}

	if aj.Record(inFlowID, t.ID()) {
		// a trailing token after the gateway fired (a discriminator / partial join
		// consumes the later arrivals): end this track, consumed at the join.
		t.updateState(TrackMerged)

		return false
	}

	// Park and let the loop decide (it owns reachability + guard evaluation): on
	// resume this goroutine proceeds as the survivor, or returns if it was merged
	// away; ctx cancel (incl. the loop aborting an unsatisfiable rule) ends it.
	t.updateState(TrackAwaitSync)
	// Carry the join node so the loop records the park from the event (SRD-028 FR-3).
	t.instance.emit(trackEvent{kind: evParked, track: t, node: step.node})

	select {
	case <-t.parkCh:
		return !t.inState(TrackMerged)

	case <-t.ctx.Done():
		t.updateState(TrackCanceled)

		return false
	}
}

// executeNode tries to execute flow.Node n.
// On succes it returns a list (probably empty) of outgoing sequence flows.
// On failure it returns error.
//
// The execution runs on its own frame (ADR-010 §2.3): the consumer role
// loads into it, the node executes against the per-execution environment,
// the producer role fills it, and the frame commits atomically on success.
// The deferred Discard is a no-op after a successful commit, so a failure at
// ANY stage leaves the container scope untouched.
func (t *track) executeNode(
	ctx context.Context,
	step *stepInfo,
) ([]*flow.SequenceFlow, error) {
	ne, ok := step.node.(exec.NodeExecutor)
	if !ok {
		return nil,
			errs.New(
				errs.M("node doesn't provide exec.NodeExecutor interface"),
				errs.C(errorClass, errs.TypeCastingError))
	}

	f, err := t.instance.sc.openFrame(t.ID(), step.node.ID())
	if err != nil {
		return nil,
			errs.New(
				errs.M("couldn't create the execution frame for node %q",
					step.node.Name()),
				errs.C(errorClass, errs.OperationFailed),
				errs.E(err))
	}

	defer f.Discard()

	if perr := t.prepareNodeExecution(ctx, step, f); perr != nil {
		return nil, perr
	}

	nexts, err := t.executeNodeCore(ctx, step, ne, f)

	// SRD-029 §3.7/§4.5 interruption checkpoint: cancellation wins over the
	// returned error AND over success. A boundary fire (or instance terminate)
	// cancels t.ctx; a ctx-honoring op returns early, a ctx-ignoring op returns
	// late — either way the result is abandoned BEFORE finalize, so no output is
	// committed (the deferred f.Discard rolls the frame back) and no flow is
	// followed. Tested on ctx.Err(), not on err: a canceled op may return
	// context.Canceled, a wrapped error, or even nil — if the context is done the
	// result is discarded. The run loop maps a done ctx to TrackCanceled, not
	// TrackFailed, so an interrupted activity is never mis-routed as a failure.
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	if err != nil {
		return nil, err
	}

	if err := t.finalizeNodeExecution(ctx, step, f); err != nil {
		return nil, err
	}

	return nexts, nil
}

// prepareNodeExecution marks the step started and runs the consumer role:
// the node loads its inputs and properties into the execution frame.
func (t *track) prepareNodeExecution(
	ctx context.Context,
	step *stepInfo,
	f *scope.Frame,
) error {
	t.updateState(TrackExecutingStep)
	step.state = StepStarted
	t.record(TrackExecutingStep) // record this node visit (path + timing)

	return t.loadIncomingData(ctx, step.node, f)
}

// executeNodeCore runs the node's executor against the per-execution
// environment and returns its outgoing flows.
func (t *track) executeNodeCore(
	ctx context.Context,
	step *stepInfo,
	ne exec.NodeExecutor,
	f *scope.Frame,
) ([]*flow.SequenceFlow, error) {
	step.state = StepExecuting

	nexts, err := ne.Exec(ctx, newExecEnv(t.instance, f))
	if err != nil {
		return nil, err
	}

	return nexts, nil
}

// finalizeNodeExecution marks the step ended, enters the results-processing
// stage, runs the producer role and commits the execution frame — the only
// moment the node's results reach the container scope, as one atomic batch.
//
// The track transitions to TrackProcessStepResults here so the ADR-001 §4.2
// state machine (… → TrackExecutingStep → TrackProcessStepResults → …) is
// real and observable in the token history, not just a declared constant. The
// stage projects TokenAlive (the token still sits on the node until the
// outgoing flows are resolved).
func (t *track) finalizeNodeExecution(
	ctx context.Context,
	step *stepInfo,
	f *scope.Frame,
) error {
	step.state = StepEnded
	t.updateState(TrackProcessStepResults)

	if err := t.uploadOutgoingData(ctx, step.node, f); err != nil {
		return err
	}

	return f.Commit()
}

// checkFlows processes a node's outgoing flows. The track continues on the
// first (cyclic-preferred) flow carrying its current token; any remaining
// flows are a fork — handed to the loop, which builds one new track per extra
// flow (each new track self-creates its own token on execution). 1:1
// track:token holds: the parent keeps its single token, no split.
func (t *track) checkFlows(flows []*flow.SequenceFlow) error {
	if len(flows) == 0 {
		t.updateState(TrackEnded)
		return nil
	}

	// if any outgoing flow is cyclic on the current node, it becomes the
	// track's next step.
	nextNode := 0
	for i, f := range flows {
		if f.Target().ID() == t.currentStep().node.ID() {
			nextNode = i
			break
		}
	}

	// the track continues on the chosen flow (it carries its single position;
	// no token object). inFlow records that flow so a synchronizing-join target
	// knows which incoming flow this token arrived on.
	nextStep := stepInfo{
		node:   flows[nextNode].Target().Node(),
		inFlow: flows[nextNode],
		state:  StepCreated,
	}

	// Guard the append: checkNodeType below may register a mid-flow event whose
	// waiter fires synchronously (a broker-buffered message) and reads t.steps
	// from its own goroutine via ProcessEvent -> updateState -> record.
	t.m.Lock()
	t.steps = append(t.steps, &nextStep)
	t.m.Unlock()

	// The token continues on this track to nextStep's node. newTrack only
	// classified the track's initial node, so a mid-flow event node (e.g. a
	// ReceiveTask reached from an upstream node) must be classified here too —
	// otherwise it would execute without registering its event or parking the
	// track. checkNodeType is a no-op for non-event nodes.
	if err := t.checkNodeType(nextStep.node); err != nil {
		return err
	}

	// Report the advance to the loop — the sole owner of the position view (ADR-017 Rule 2,
	// SRD-028 FR-2). The node is carried in the event so the loop never reads currentStep
	// cross-goroutine. Reached only from run() (instance Active), so no construction gating.
	// Emitted AFTER checkNodeType: for a wait node, checkNodeType makes the token observably
	// WaitForEvent and then registers its hub waiters; inserting this loop round-trip before
	// that registration would widen the window in which a fired event finds no subscriber and
	// is lost. The position view does not need the move before evWaiting (a join recheck is
	// triggered by a death/park, never by a move).
	t.instance.emit(trackEvent{kind: evMoved, track: t, node: nextStep.node})

	// the remaining flows fork: build a fresh slice (don't mutate the caller's)
	// and hand it to the loop, which constructs the new tracks. The track never
	// mutates instance state itself.
	extras := make([]*flow.SequenceFlow, 0, len(flows)-1)
	for i, f := range flows {
		if i != nextNode {
			extras = append(extras, f)
		}
	}

	if len(extras) != 0 {
		t.instance.emit(trackEvent{kind: evFork, track: t, flows: extras})
	}

	return nil
}

// unregisterEvent unregisters all EventNode events on instance.
func (t *track) unregisterEvent(n flow.Node) error {
	en, ok := n.(flow.EventNode)
	if !ok {
		return errs.New(
			errs.M("node %q[%s] doesn't implement flow.EventNode interface",
				n.Name(), n.ID()),
			errs.C(errorClass, errs.TypeCastingError))
	}

	for _, eDef := range en.Definitions() {
		if err := t.instance.UnregisterEvent(t, eDef.ID()); err != nil {
			return errs.New(
				errs.M("failed to unregister event"),
				errs.C(errorClass, errs.OperationFailed),
				errs.D("track_id", t.ID()),
				errs.D("event_definition_id", eDef.ID()),
				errs.D("event_definition_type", eDef.Type()),
				errs.E(err))
		}
	}

	return nil
}

// loadIncomingData checks if the flow.Node n implements flow.NodeDataConsumer
// and if so, calls the LoadData of the Node from input DataObjects.
func (t *track) loadIncomingData(
	ctx context.Context,
	n flow.Node,
	f *scope.Frame,
) error {
	dc, ok := n.(exec.NodeDataConsumer)
	if !ok {
		return nil
	}

	return dc.LoadData(ctx, f)
}

// uploadOutgoingData checks if the flow.Node n impmements flow.NoadDataProducer
// and if so, calls the UploadData of the Node.
func (t *track) uploadOutgoingData(
	ctx context.Context,
	n flow.Node,
	f *scope.Frame,
) error {
	dp, ok := n.(exec.NodeDataProducer)
	if !ok {
		return nil
	}

	return dp.UploadData(ctx, f)
}

// --------------------- exec.EventProcessor interface -------------------------

// ProcessEvent (eventproc.EventProcessor) is called by a Signal/Timer producer on its OWN
// goroutine when an event fires (Message is registered at instance granularity instead —
// SRD-027 FR-8). It does NOT touch track state: it hands the event to the per-instance loop
// (FR-2), which dispatches it to this track's evtCh, where deliver() applies it on the
// track's own goroutine. Returns once enqueued, not once applied.
func (t *track) ProcessEvent(
	_ context.Context,
	eDef flow.EventDefinition,
) error {
	t.instance.emit(trackEvent{kind: evDeliver, track: t, eDef: eDef})

	return nil
}

// deliver applies a fired event to the waiting node on the track's OWN goroutine: run()
// receives it from evtCh — the loop having already passed the correlation gate (§3.4) —
// and calls this. It lets the node process the payload, unregisters the node's event
// definitions, advances onto the winning arm for an Event-Based gateway, and returns the
// track to Ready so run() resumes (SRD-027 FR-2). No event mutex and no WaitForEvent
// guard: the loop guarantees a single delivery to a parked track, so this goroutine is
// the only one touching the track's state.
func (t *track) deliver(
	ctx context.Context,
	eDef flow.EventDefinition,
) error {
	if ctx == nil {
		ctx = t.ctx
	}

	// Read the waiting node's position. t.steps is written only by this goroutine, but t.m is
	// held here uniformly with the other steps accessors (currentStep / record) so the loop's
	// merge-path record() — finalizing a quiescent merged track — never races an append
	// (SRD-028 §3.6). path() / Token() read the lock-free hist projection, not t.steps.
	t.m.RLock()
	n := t.steps[len(t.steps)-1].node
	t.m.RUnlock()

	ep, ok := n.(eventproc.EventProcessor)
	if !ok {
		return errs.New(
			errs.M("node %q(%s) doesn't support event processing",
				n.Name(), n.ID()),
			errs.C(errorClass, errs.TypeCastingError))
	}

	if err := ep.ProcessEvent(ctx, eDef); err != nil {
		return err
	}

	// A UserTask (human task) parked without a hub waiter (parkHumanTask) — there
	// is nothing to unregister. Only an event catch (flow.EventNode) is torn down
	// from the hub after delivery.
	if _, isEvent := n.(flow.EventNode); isEvent {
		if err := t.unregisterEvent(n); err != nil {
			return errs.New(
				errs.M("node %q[%s] unregister events failed", n.Name(), n.ID()),
				errs.E(err))
		}
	}

	// An Event-Based gateway subscribes on behalf of its arms (SRD-024 §4.1): the
	// fired event belongs to one of those arm nodes, not the gate. Advance the track
	// onto the winning arm so run() executes the arm — its payload was just bound by
	// the gate's delegated ProcessEvent and it is not re-registered as a waiter, so it
	// continues straight to its outgoing flow; the gate itself is never executed. A
	// plain catch event (no ArmFor) is executed in place, as before.
	if er, ok := n.(eventRouter); ok {
		t.advanceToArm(n, er, eDef)
	}

	t.updateState(TrackReady)

	return nil
}

// eventRouter is implemented by a node (the Event-Based gateway) that subscribes for
// several arm nodes at once and resolves a fired event to the arm that owns it.
type eventRouter interface {
	ArmFor(flow.EventDefinition) (flow.Node, bool)
}

// advanceToArm appends a step for the gate's winning arm so the run loop executes the
// arm (its event already bound, the arm not re-registered as a waiter) rather than the
// gate; the gate→arm sequence flow becomes the step's inFlow. It runs on the waiter
// goroutine, so the t.steps append is guarded by t.m (the run goroutine reads
// currentStep under the same lock).
func (t *track) advanceToArm(
	gate flow.Node,
	er eventRouter,
	eDef flow.EventDefinition,
) {
	arm, ok := er.ArmFor(eDef)
	if !ok {
		// Unreachable in practice: the gate's ProcessEvent (called just above)
		// already resolved and bound this arm, so ArmFor cannot miss here. If it
		// somehow did, append nothing — the loop re-enters the gate, whose Exec
		// fails loudly, rather than advancing onto a nil arm.
		return
	}

	var armFlow *flow.SequenceFlow

	for _, of := range gate.Outgoing() {
		if of.Target().Node().ID() == arm.ID() {
			armFlow = of

			break
		}
	}

	t.m.Lock()
	t.steps = append(t.steps, &stepInfo{
		node:   arm,
		inFlow: armFlow,
		state:  StepCreated,
	})
	t.m.Unlock()

	// Report the arm advance to the loop, like checkFlows (ADR-017 Rule 2, SRD-028 FR-2):
	// the winning arm becomes this track's position in the loop's own view.
	t.instance.emit(trackEvent{kind: evMoved, track: t, node: arm})
}

// -----------------------------------------------------------------------------
