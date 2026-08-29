// Track represents a single execution flow of a process.
// Every process has one or more entry points (e.g., events or nodes with no
// incoming sequence flows). These entry points become the beginning of a
// track.
//
// A track starts execution from a start node.
//
//   - If a node awaits an event to continue, its event definition is
//     registered in the instance, and the track state changes to TrackAwaitEvent.
//     Once the event is sent to the track via ProcessEvent, the track continues.
//
//   - Node execution is a single Execute step: the track loads the node's
//     incoming data, runs the node's Exec, and uploads its outgoing data. On
//     success, Exec returns a list of outgoing flows.
//
// If the number of outgoing flows is not zero, they are processed as follows:
//
//   - The first flow becomes the next step of the track.
//     If there is a cyclic flow to the node itself, the first one will
//     be the next step of the track. If there is more than one cyclic flow,
//     GoBPM has no mechanism to set priority between them.
//
//   - For the rest of the outgoing flows, new child tracks are created and
//     added to the instance.
//
//   - The token in the track splits according to the number of flows. The first
//     token is assigned to the current track for the next step, and the rest
//     are assigned to the newly created child tracks.
//
// If there are no outgoing flows, the track ends and the token dies.
//
// ## Human interaction
//
// If a node needs to interact with a human, it should support the
// interactor.HumanTask interface.
//

package instance

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/internal/eventproc/eventhub/waiters"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/exec"
	"github.com/dr-dobermann/gobpm/pkg/interactor"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/msgflow"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/set"
	"github.com/dr-dobermann/gobpm/pkg/tasks"

	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
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

	// TrackDehydrated represents a track whose long wait was externalized and
	// whose goroutine has RETURNED (ADR-007 v.2 §2.1) — terminal for the
	// goroutine, NOT for the flow: the wait outlives the goroutine as the loop's
	// bookkeeping and the checkpoint record, and a trigger re-materializes it as
	// a continuation fork. Retained as a live record (like TrackAwaitingMerge);
	// its token projects Alive at the wait node.
	TrackDehydrated

	// TrackIncident represents a track whose technical failure raised an
	// incident (ADR-036 §2.2): terminal for the track — the incident record
	// carries the continuation, and a retry is a fresh track spawned from it,
	// never a resumption. While the incident is open the node's token stays
	// visible (SRD-079 FR-4) and the track's boundaries stay armed.
	TrackIncident

	// TrackIterating represents a track whose activity is running its node
	// MORE THAN ONCE — a Multi-Instance or a Standard Loop (ADR-025
	// §2.13b.1e). It is a live, working state, like TrackExecutingStep, and
	// it exists because an activity is ONE step of its token however many
	// instances run it: without it, each instance's execution reported a
	// step of its own, and a decorator had to reach into every instance and
	// suppress the transition.
	//
	// Per-instance executions fall BELOW this state's granularity — the
	// machine simply does not accept a step transition while a track is
	// iterating (see stepTransitionsVisible). What each instance is doing
	// travels as attributes rather than as states (§2.13b.1f).
	TrackIterating

	// TrackHostingScope represents a track whose activity has forked its
	// token into a child scope and is waiting for that scope to drain — an
	// embedded Sub-Process (ADR-025 §2.13b.1e).
	//
	// It reads as EXECUTING today, which is the same defect ADR-025 §2.13
	// named one level down and fixed only inside the runtime: "parked for a
	// child's drain was, from outside the runner's own stack,
	// indistinguishable from executing". The executor learned the
	// difference; the token never did. It is NOT a wait in the
	// TrackWaitForEvent sense — nothing external will wake it, its own body
	// will — which is why it is a state of its own rather than a reuse.
	TrackHostingScope
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
		"TrackDehydrated",
		"TrackIncident",
		"TrackIterating",
		"TrackHostingScope",
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

// execHandle carries an activityExec through an atomic.Pointer — the
// interface value needs a concrete box to live in.
//
// node is what the executor was built FOR. The handle outlives a single call
// now (arrival resolves it, execution reuses it — SRD-090.B FR-1), so a
// reader has to be able to tell whether the executor it finds belongs to the
// step it is asking about: a track that advanced would otherwise be handed
// the previous node's executor and re-run it.
type execHandle struct {
	e    activityExec
	node flow.Node
}

// track processed single line of the process from start noed or
// from fork of sequence flow.
type track struct {
	lastErr error
	ctx     context.Context
	hist    atomic.Pointer[[]stepUpdate]
	// exec is the executor currently running this track's step, or nil
	// between steps. The track's OWN goroutine writes it; the LOOP reads it
	// to ask what the activity awaits (SRD-090.A FR-8) — which is why it is
	// atomic and why the answer comes from the executor rather than from a
	// mirror the loop would have to keep in step.
	//
	// A pointer to a one-field struct because the value is an interface:
	// atomic.Pointer needs a concrete type to point at.
	exec        atomic.Pointer[execHandle]
	instance    *Instance
	cancel      context.CancelFunc
	miState     *miState
	mergedInto  atomic.Pointer[string]
	parkCh      chan struct{}
	dehydrateCh chan struct{} // closed by the loop to release a parked wait's goroutine (SRD-071)
	evtCh       chan flow.EventDefinition
	taskID      string
	scopePath   scope.DataPath
	// receivedItem is THIS delivery's captured payload (ADR-006 v.5
	// §2.9.1, SRD-085 FR-1): deliver() captures it from the fired
	// definition on the track's own goroutine, the next node upload
	// stages it into the execution frame and clears it. Track-goroutine
	// owned — never read off-goroutine.
	receivedItem *data.ItemDefinition

	// adHocActivity names the inner activity this track was routed to inside an
	// Ad-Hoc scope, empty for every other track (SRD-074 §3.4). Set pre-spawn on
	// the loop goroutine and read after the track is terminal, so it needs no
	// synchronization: it is how the settling loop knows which activity to count
	// as completed.
	adHocActivity string
	scopeSeg      string
	foundation.BaseElement
	prev      []string
	msgDefIDs []string
	// msgIterKeyName/msgIterKey are the iteration-correlation pair the
	// registration evaluated for a catch that declares one (SRD-085
	// FR-3): the declared key's NAME and this execution's subscription
	// VALUE, derived from the iteration's own scope. Guarded by t.m
	// with msgDefIDs; read by the loop when indexing the wait.
	msgIterKeyName string
	msgIterKey     string
	condDefs       []*events.ConditionalEventDefinition
	steps          []*stepInfo
	// compWaitRef holds the target ref of the wait-for-completion Compensation
	// throw this track is parked on (SRD-059 FR-5); informational.
	compWaitRef string
	// pendingCompensate is the evCompensate a parkCompensationThrow deferred:
	// checkFlows emits it AFTER its evMoved, so the predecessor's ledger entry
	// (on that evMoved) is applied before the sweep resolves (SRD-059 FR-6).
	pendingCompensate *trackEvent
	// compFrameSeed, on a compensation-handler track, is the ledger entry's
	// data snapshot: seeded into each frame's INPUTS so reads are
	// snapshot-first while Put/outputs still commit to the live scope
	// (SRD-059 FR-4, ADR-026 §2.5). Set by the loop before spawn.
	compFrameSeed []data.Data
	// miSeed is a RESTORED own-iteration position (SRD-082 FR-3): the
	// decorator resumes at the recorded pass with the recorded outputs
	// instead of iterating from zero. Set by restore before spawn,
	// consumed once by the runner.
	miSeed *checkpoint.MIRecord

	// iterSeed is a RESTORED activity's executor set (SRD-090.A FR-7):
	// which ordinals were still live when the capture was taken. A
	// sequential decorator needs only the completed count (miSeed carries
	// it), but a parallel one completes out of order — the count alone
	// cannot say WHICH ordinals are done, and re-running a completed
	// instance is exactly what FR-7 forbids. Consumed once, by the
	// activity the track was restored on (takeIterSeed).
	iterSeed *checkpoint.IterationRecord

	// parkedTaskIDs are the fan-out's parked-work identities as they stood
	// at the moment the loop released the track, keyed by ordinal
	// (ADR-020 §2.12).
	//
	// The capture cannot read them off the executor, because there is none
	// left to read: the executor is cleared when executeStep returns, which
	// for a dehydration is BEFORE the cut that records the wait. Every
	// capture taken mid-run therefore saw the ids, and the one that mattered
	// — the persisted one — saw nothing, so a restored fan-out minted fresh
	// ids and every handle the distributor was holding named nothing.
	//
	// Written by dehydrateTrack and read by the capture, both on the loop
	// goroutine, so it needs no lock of its own.
	parkedTaskIDs map[int]string

	// pendingCompletions are completions that arrived for an iterated
	// activity whose decorator is not running yet, keyed by ordinal.
	//
	// A restored fan-out is rebuilt by the very action being applied to it,
	// and the loop is serving requests before the track's goroutine has
	// reached its step — so the ordinary case is that the first completion
	// arrives with no executor to receive it. It cannot be refused (the
	// instance is resident, so there is no hydration to replay after) and it
	// cannot be resolved lazily either: that builds an executor nobody runs
	// and loses the completion inside it.
	//
	// Held here, on the TRACK, which outlives every decorator run, and taken
	// by the decorator when it starts.
	//
	// Guarded by iterMu: the LOOP writes it (deliverCompletion) while the
	// TRACK's own goroutine takes it (the decorator, as it starts).
	pendingCompletions map[int]flow.EventDefinition
	// compScopeSeed, on a compensation event-sub handler host, is the snapshot
	// committed into the handler's fresh child scope at open (shadowing
	// reads). Set by the loop before spawn.
	// timerDeadline pins a parked timer wait for the checkpoint
	// (SRD-070 FR-3) — recorded at arming, zero otherwise (timerCycles
	// rides below the slices for fieldalignment). timerHinted marks a
	// RESTORED plan: the DeadlineHinter capability then overrides the
	// waiter's re-evaluation (SRD-070 FR-6).
	timerDeadline time.Time
	compScopeSeed []data.Data
	timerCycles   int
	loopCounter   int
	m             sync.RWMutex

	// iterMu guards the fan-out registers below (parkedTaskIDs,
	// pendingCompletions). Its own lock rather than m, because the capture
	// reads them while holding m's read lock.
	iterMu sync.Mutex
	stopIt atomic.Bool
	// held is set at arm time when this track's wait registered an engine-level
	// holder that can wake a released instance (SRD-071 FR-3): a dehydratable
	// timer whose deadline the engine timer service accepted. It gates the idle
	// detector — an unheld wait keeps the instance resident (no wait releases
	// without something that can wake it). atomic: the loop's detector reads it
	// across goroutines from the arming track.
	held        atomic.Bool
	timerHinted bool
	// compWaitRestored marks a RESTORED wait-throw thrower (SRD-082
	// FR-6): it re-parks without re-throwing — its adopted sweep is
	// already running and will deliver the sentinel. Set at restore,
	// before the track goroutine exists; consumed once.
	compWaitRestored bool
	// callRestored marks a RESTORED Call Activity caller (SRD-082
	// FR-7): it re-parks without re-invoking — the adoption re-links to
	// the recorded child. Same lifecycle as compWaitRestored.
	callRestored bool
	// skipInitialArm suppresses the spawn-time boundary arming ONCE — for an
	// incident-retry respawn, whose watches transfer from the failed attempt
	// instead of re-arming (SRD-079 FR-6: a repeated failure must not reset
	// an SLA clock). Consumed by armBoundaries; later moves arm normally.
	skipInitialArm bool
	// woken marks a continuation-fork track spawned to WAKE a dehydrated wait
	// (SRD-071 FR-4): it re-enters the wait node with the trigger already
	// present in evtCh and fires through it, so it must NOT be re-armed as a
	// waiter — recordBornWaiter skips it. Its persisted prev inherits the
	// dehydrated track's lineage without appending it (bounded across cycles,
	// §4.1).
	woken bool
	state trackState
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

	// Publish the transition to host observers (SRD-018): identity + the real
	// node phase, never payload. Non-blocking at the sink; a no-op when no one is
	// observing. The phase is the un-collapsed track state (SRD-041 FR-6), not
	// the 3-value token projection — that projection stays on the handle's token
	// view (Tokens()/History()).
	t.instance.report(observability.Fact{
		Kind:     observability.KindNodeProgress,
		Phase:    nodePhaseFor(state),
		NodeID:   node.ID(),
		NodeName: node.Name(),
	})
}

// trackPhase maps each track state to its observable node phase (SRD-041 §3.4) —
// a data table, not a switch. Several internal states share one phase (the three
// wait states → Parked, the two active states → Executing), so the stream reports
// node progress without leaking the track's internal state machine.
var trackPhase = map[trackState]observability.Phase{
	TrackCreated:            observability.PhaseEntered,
	TrackReady:              observability.PhaseEntered,
	TrackExecutingStep:      observability.PhaseExecuting,
	TrackProcessStepResults: observability.PhaseExecuting,
	TrackWaitForEvent:       observability.PhaseParked,
	TrackAwaitingMerge:      observability.PhaseParked,
	TrackAwaitSync:          observability.PhaseParked,
	TrackMerged:             observability.PhaseMerged,
	TrackEnded:              observability.PhaseCompleted,
	TrackCanceled:           observability.PhaseCanceled,
	TrackFailed:             observability.PhaseFailed,
	TrackDehydrated:         observability.PhaseDehydrated,
	TrackIncident:           observability.PhaseIncident,
	// Both new states map onto the EXISTING executing phase (ADR-025
	// §2.13b.1f): an iterating or scope-hosting token IS executing its
	// activity, so no new value reaches a host and the added precision
	// stays internal. A distinct phase is SRD-090.C's call, made with the
	// token projection in hand.
	TrackIterating:    observability.PhaseExecuting,
	TrackHostingScope: observability.PhaseExecuting,
}

// nodePhaseFor returns the observable node phase for a track state. Every valid
// track state is in trackPhase, so a real state always maps; an out-of-range
// state (never produced by the engine) yields the zero phase rather than
// panicking on trackState.String()'s bounds.
func nodePhaseFor(state trackState) observability.Phase {
	return trackPhase[state]
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
		state:       TrackReady,
		instance:    inst,
		parkCh:      make(chan struct{}, 1),
		dehydrateCh: make(chan struct{}),
		evtCh:       make(chan flow.EventDefinition, eventBufferDepth),
	}

	if prevTrack != nil {
		t.prev = append(t.prev, append(prevTrack.prev, prevTrack.ID())...)
		// a fork stays in its spawning track's scope (SRD-049 FR-7); scope
		// seeds get their child path from the loop pre-spawn.
		t.scopePath = prevTrack.scopePath
	} else {
		t.scopePath = inst.sc.root
	}

	// History is recorded once the track runs (per-node visits + state
	// transitions), so it uses the running clock; before Run, Token() returns
	// the zero projection. checkNodeType below may add a WaitForEvent entry
	// for event-start nodes.
	if err := t.checkNodeType(start, true); err != nil {
		return nil, err
	}

	return &t, nil
}

// checkNodeType determines if node awaits for event or human interaction
// and updates track state on positive comparison. atConstruction marks the
// newTrack call: it may run ON THE LOOP GOROUTINE (spawnForks builds a
// fork-born track there), where emitting evWaiting would deadlock the loop
// on its own channel — the born-parked track is recorded by spawn's
// recordBornWaiter instead (SRD-027 FR-5). Only the mid-run path (the
// track's own goroutine) emits.
// resolveExec returns the executor this track addresses for the given step,
// building it on first use and retaining it (SRD-090.B FR-1).
//
// ONE object per step, from arrival until the step ends: classification,
// arming, execution and unregistration all address it. Two constructions
// would produce two objects with one identity — the hub compares subscribers
// by ID — and the subscription set would live on whichever was not
// dispatching.
func (t *track) resolveExec(step *stepInfo) activityExec {
	if h := t.exec.Load(); h != nil && h.node == step.node {
		return h.e
	}

	e := execFor(t, step)
	t.exec.Store(&execHandle{e: e, node: step.node})

	return e
}

func (t *track) checkNodeType(node flow.Node, atConstruction bool) error {
	return t.checkNodeTypeFor(nil, node, atConstruction)
}

// checkNodeTypeFor classifies node on behalf of ONE execution.
//
// A CONCURRENT instance classifies its own node, because parking is per
// execution: the activity is classified once when the token arrives, so
// without this N instances of a fan-out would announce one task between them
// and the rest would complete without anyone doing them. `e` is nil for every
// other caller, whose execution and track are the same thing.
func (t *track) checkNodeTypeFor(
	e *nodeExec, node flow.Node, atConstruction bool,
) error {
	// Non-event wait nodes (human task / composite / call / worker) park via
	// their capability, dispatched in checkActivityWaitKind.
	if done, err := t.checkActivityWaitKind(e, node, atConstruction); done {
		return err
	}

	// A throw event (EndEvent, IntermediateThrowEvent) emits its definitions
	// in Exec and must not be parked as a waiter for the event it is about to
	// throw — with ONE exception: a wait-for-completion Compensation throw IS
	// a wait node (SRD-059 FR-5). Dispatched in checkThrowNode.
	if done, err := t.checkThrowNode(node, atConstruction); done {
		return err
	}

	en, ok := node.(flow.EventNode)
	if !ok {
		return nil
	}

	// Only a node that can PROCESS a fired event waits for one.
	if _, ok := node.(eventproc.EventProcessor); !ok {
		return nil
	}

	defs := en.Definitions()
	if len(defs) == 0 {
		return nil
	}

	// Record the Message catch-definition ids so the loop can index them → this track
	// (SRD-027 FR-8): carried in the evWaiting emit below for a mid-run wait, and read by
	// spawn for a track that starts parked before the loop drains events. Conditional
	// definitions are recorded the same way (SRD-048 FR-7) — the loop arms them itself.
	msgIDs := messageDefIDs(defs)

	// a catch declaring iteration correlation (SRD-085 FR-3) evaluates
	// its subscription-side value HERE, over this execution's own scope
	// — where the iteration's split item is bound — so the loop's
	// routing and the hub's subscription filter read one value.
	keyName, keyValue, err := t.iterationKey(node, msgIDs)
	if err != nil {
		return err
	}

	t.m.Lock()
	t.msgDefIDs = msgIDs
	t.condDefs = conditionalDefs(defs)
	t.msgIterKeyName = keyName
	t.msgIterKey = keyValue
	t.m.Unlock()

	if keyValue != "" {
		t.instance.corr.markIterationKeyName(keyName)
		t.instance.corr.addIterKey(t.ID(), keyValue)
		// a live message waiter subscribed before this iteration parked
		// (a sibling registered first) carries the broker subscription —
		// grow it with this iteration's value, exactly as a
		// newly-learned conversation value grows it (SRD-017 §4.5); for
		// the FIRST iteration the subscribe itself reads the value from
		// CorrelationKeys(), and AddEventKey no-ops benignly.
		t.instance.corr.extendReceivers(keyValue)
	}

	// Stash the timer plan BEFORE the wait is declared (SRD-070 FR-3):
	// the evWaiting emit below lets the loop checkpoint immediately, and
	// the captured record must already carry the deadline descriptor —
	// stashing after the emit races the capture (caught under -race).
	for _, d := range defs {
		t.stashTimerPlan(d, t)
	}

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
	// on Active AND on a mid-run call: at construction the loop records the track via
	// spawn's recordBornWaiter instead — for a pre-loop track the events channel is not
	// yet draining, and for a fork-born track checkNodeType runs ON the loop goroutine,
	// where this emit would deadlock the loop on its own channel.
	if !atConstruction && t.instance.State() == Active {
		t.instance.emit(trackEvent{
			kind:      evWaiting,
			track:     t,
			node:      en,
			msgDefIDs: t.msgDefIDs,
			condDefs:  t.condDefs,
		})
	}

	if err := t.armWaiters(en, defs); err != nil {
		return err
	}

	// The wait is now holdable: tell the loop, so its dehydration check runs
	// once more with the holder in place (SRD-095 FR-8). Same gating as the
	// evWaiting emit above — at construction the loop is not draining yet,
	// and a fork-born track's checkNodeType runs on the loop goroutine.
	if !atConstruction && t.instance.State() == Active {
		t.instance.emit(trackEvent{kind: evWaitArmed, track: t, node: en})
	}

	return nil
}

// iterationKey evaluates a catch's declared iteration correlation over
// this execution's scope (SRD-085 FR-3, ADR-006 v.5 §2.9.3). A node
// without the capability — or without message definitions — yields
// ("", "").
func (t *track) iterationKey(
	node flow.Node, msgIDs []string,
) (string, string, error) {
	ic, ok := node.(interface {
		IterationCorrelation() (string, data.FormalExpression)
	})
	if !ok || len(msgIDs) == 0 {
		return "", "", nil
	}

	keyName, expr := ic.IterationCorrelation()
	if expr == nil {
		return "", "", nil
	}

	frame, err := t.instance.sc.openFrameAt(
		"iter-corr", node.ID(), t.scopePath)
	if err != nil {
		return "", "", err
	}
	defer frame.Discard()

	v, err := t.instance.ExpressionEngine().Evaluate(
		context.Background(), expr, newExecEnv(t.instance, frame, t))
	if err != nil {
		return "", "", errs.New(
			errs.M("iteration correlation of %q failed", node.Name()),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err))
	}

	return keyName, fmt.Sprint(v.Get(context.Background())), nil
}

// armWaiters subscribes the node's definitions. Per-trigger registration is the
// one place the hybrid boundary is chosen (SRD-027 FR-8 / §3.7): a Message
// catch registers the Instance (it owns correlation), every other trigger
// registers the track. A Conditional catch registers NOTHING — its trigger
// source is the instance's own commits, so the subscription is loop-owned,
// carried on the evWaiting emit (SRD-048 FR-7, ADR-006 v.3 §2.7). A wait the
// ENGINE can hold is handed over instead, so it survives dehydration
// (SRD-071 FR-3).
func (t *track) armWaiters(en flow.EventNode, defs []flow.EventDefinition) error {
	// A node may arm SEVERAL waits at once — an Event-Based Gateway races its
	// arms, so Definitions() is their union (SRD-071 FR-3a). The track counts as
	// held only if EVERY one of them found a holder: releasing the instance
	// while a single arm still depends on an in-process waiter would strand that
	// arm — it could never fire again (ADR-007 v.2 §2.4, the per-arm guard). A
	// conditional arm is never holdable at all (its trigger is the instance's own
	// data), so an EBG carrying one always stays resident.
	allHeld := true

	owner := t.activityOwner()

	for _, d := range defs {
		// A SIBLING ALREADY ARMED THIS DEFINITION. Its hold and its hub
		// subscription serve this instance too, because both belong to the
		// activity rather than to one of its instances (SRD-090.B FR-2) —
		// so recording the waiter is all this pass owes. Arming again would
		// take a second hold under the same (instance, track) key, and the
		// first delivery's ReleaseWaits would then withdraw the lot.
		if owner != nil && !owner.awaiting(d, t.execOrdinal()) {
			continue
		}

		if t.holdWait(d) {
			continue
		}

		allHeld = false

		if d.Type() == flow.TriggerConditional {
			continue
		}

		// WHO registers is the executor's answer (SRD-090.B FR-1). A
		// decorator subscribes for the whole ACTIVITY and holds one
		// subscription across every pass, where a track's registration is
		// keyed to an arrival an in-place iteration never repeats. A leaf
		// owns its own wait and answers nil, leaving the per-trigger rule
		// below exactly as it was: a Message registers the Instance, which
		// owns correlation, anything else registers the track.
		proc := eventproc.EventProcessor(t)

		switch {
		case owner != nil:
			proc = owner

		case d.Type() == flow.TriggerMessage:
			proc = t.instance
		}

		if err := t.instance.RegisterEvent(proc, d); err != nil {
			return errs.New(
				errs.M("couldn't register event definitions"),
				errs.C(errorClass, errs.BulidingFailed),
				errs.D(observability.AttrNodeID, en.ID()),
				errs.D(observability.AttrNodeName, en.Name()),
				errs.D(observability.AttrEventDefinitionID, d.ID()),
				errs.E(err))
		}
	}

	t.held.Store(allHeld && len(defs) > 0)

	return nil
}

// activitySubscriber reports the executor that subscribes for this track's
// current activity, or nil when the existing per-trigger rule stands.
// execOrdinal is the ordinal of the instance parking right now — the join key
// the subscription set records a waiter under (SRD-090.B FR-2/FR-3).
//
// Asked of the executor, which is the only thing that knows: a decorator's
// live instance reports its own ordinal, and a plain activity is instance
// zero of one.
func (t *track) execOrdinal() int {
	h := t.exec.Load()
	if h == nil {
		return 0
	}

	return h.e.state().ordinal
}

func (t *track) activityOwner() activitySubscriber {
	step := t.currentStep()
	if step == nil || step.node == nil {
		return nil
	}

	// Resolved from the track's OWN step, not from a synthesized one: the
	// executor built here is the object executeStep will reuse, so it must be
	// built from the same step or it would lose what the step carries (the
	// arrival flow, the pass state). Resolving lazily also keeps every plain
	// node's path untouched — its executor answers nil, so nothing about
	// arming changes for it (SRD-090.B FR-1/NFR-1).
	return t.resolveExec(step).subscriber()
}

// ownerIfResolved is activityOwner without the resolution: it answers only
// when an executor already exists, and never builds one.
//
// The LOOP uses it. activityOwner resolves lazily and STORES what it built,
// so calling it off the track's own goroutine mutates the track's executor
// handle — which once handed an advancing track the previous node's executor
// and re-ran a node. A reader that only wants to know whether an activity
// owns waits must not be able to cause that.
// seededTaskIDs returns the parked-work identities a RESTORED fan-out recorded
// for the instances that still hold work, keyed by ordinal (ADR-020 §2.12).
//
// Read from the seed rather than from an executor because there is none yet:
// this runs on the loop goroutine before the track's own starts. The seed is
// still in place — the decorator takes it later, on the track's goroutine.
//
// A COMPLETED instance is skipped: its task was withdrawn when it was done,
// and re-registering it would offer work nobody can do.
func (t *track) seededTaskIDs() map[int]string {
	if t.iterSeed == nil {
		return nil
	}

	ids := map[int]string{}

	for _, in := range t.iterSeed.Instances {
		if in.TaskID == "" || in.State == iterationCompleted {
			continue
		}

		ids[in.Ordinal] = in.TaskID
	}

	return ids
}

// seededEligibility returns the verdict the checkpoint recorded for instance
// ord's announcement, or nil if it recorded none — a document written before
// the field existed, which restores as it always did.
func (t *track) seededEligibility(ord int) *checkpoint.TaskEligibility {
	if t.iterSeed == nil {
		return nil
	}

	for _, in := range t.iterSeed.Instances {
		if in.Ordinal == ord {
			return in.Eligible
		}
	}

	return nil
}

// iterationsBusy reports whether an iterated activity on this track has an
// instance executing rather than parked — one being handed a completion, or
// one already awake and running its node.
//
// The loop asks before releasing a track for dehydration. The track's own
// state cannot answer: it reads WaitForEvent because its OTHER instances are
// parked, and its single `waiting` entry stands for all N, so neither can say
// that one of them is working.
func (t *track) iterationsBusy() bool {
	h := t.exec.Load()
	if h == nil {
		return false
	}

	d, iterated := h.e.(*iterDecorator)
	if !iterated {
		return false
	}

	return d.busyIterations()
}

// holdCompletion keeps a completion for an iterated activity that is not
// running its instances yet — see track.pendingCompletions.
func (t *track) holdCompletion(ord int, def flow.EventDefinition) {
	t.iterMu.Lock()
	defer t.iterMu.Unlock()

	if t.pendingCompletions == nil {
		t.pendingCompletions = map[int]flow.EventDefinition{}
	}

	t.pendingCompletions[ord] = def
}

// takePendingCompletions hands the held completions to the decorator that is
// starting, and forgets them: they belong to its instances from here.
func (t *track) takePendingCompletions() map[int]flow.EventDefinition {
	t.iterMu.Lock()
	defer t.iterMu.Unlock()

	held := t.pendingCompletions
	t.pendingCompletions = nil

	return held
}

// offerToPass hands an occurrence to the single pass parked on this track,
// reporting whether it landed.
//
// Non-blocking: the LOOP is the sender, so a channel that cannot take another
// occurrence must never stall the single writer (SRD-027 FR-4's rule).
func (t *track) offerToPass(eDef flow.EventDefinition) bool {
	select {
	case t.evtCh <- eDef:
		return true
	default:
		return false
	}
}

// recordIterationOwner notes who completed instance ord of the iterated
// activity at node (SRD-090.D FR-4).
//
// On the track rather than in the decorator's own state because the register
// outlives the activity: the question it answers — who approved item 2 — is
// asked by nodes downstream of an activity that has finished and taken its
// decorator with it.
func (t *track) recordIterationOwner(node flow.Node, ord int, owner string) {
	if node == nil || t.instance == nil {
		return
	}

	t.instance.iterationOwners.record(node.ID(), ord, owner)
}

// iterationDataOf is the data an ITERATION carries of its own — the element it
// was seeded with, and the engine's per-iteration names. Empty for a plain
// node, which has nothing to distinguish.
func iterationDataOf(e *nodeExec) []data.Data {
	if e == nil {
		return nil
	}

	return e.local
}

// rememberTaskID records instance ord's parked-work identity as it is minted
// or adopted, so the checkpoint records what each instance was announced under
// (ADR-020 §2.12).
func (t *track) rememberTaskID(ord int, id string) {
	if id == "" {
		return
	}

	t.iterMu.Lock()
	defer t.iterMu.Unlock()

	if t.parkedTaskIDs == nil {
		t.parkedTaskIDs = map[int]string{}
	}

	t.parkedTaskIDs[ord] = id
}

// forgetTaskID drops instance ord's identity once it is accounted for: a later
// pass of the same activity mints its own rather than reusing a handle that
// now names nothing.
func (t *track) forgetTaskID(ord int) {
	t.iterMu.Lock()
	defer t.iterMu.Unlock()

	delete(t.parkedTaskIDs, ord)
}

// taskIDRegister copies the live identities for the capture to record.
func (t *track) taskIDRegister() map[int]string {
	t.iterMu.Lock()
	defer t.iterMu.Unlock()

	if len(t.parkedTaskIDs) == 0 {
		return nil
	}

	out := make(map[int]string, len(t.parkedTaskIDs))
	for k, v := range t.parkedTaskIDs {
		out[k] = v
	}

	return out
}

func (t *track) ownerIfResolved() activitySubscriber {
	h := t.exec.Load()
	if h == nil {
		return nil
	}

	return h.e.subscriber()
}

// holdWait offers a definition to the engine's durable holders (SRD-071 FR-3),
// reporting whether one took it — in which case NO in-hub waiter is created for
// it. A timer hands over its absolute deadline (FR-6), a message/signal its hub
// subscription (FR-7). Declining (no registry, a sub-threshold or repeating
// timer, a conditional) falls through to the in-hub waiter — today's behavior,
// never a lost trigger. Whether the TRACK counts as held is armWaiters' call:
// every one of its waits must be held, not just this one.
func (t *track) holdWait(d flow.EventDefinition) bool {
	switch {
	case d.Type() == flow.TriggerTimer:
		return t.holdTimer(d)

	case holdableSubscriptions.Has(d.Type()):
		return t.holdSubscription(d)
	}

	return false
}

// dehydrationTimerThreshold is the minimum remaining time a timer wait must
// carry to be worth releasing the instance's goroutines for (ADR-007 v.2 §2.4):
// a short wait is not worth a checkpoint + hydrate round-trip, so it stays
// resident on an in-hub waiter. A deadline farther out than this dehydrates.
const dehydrationTimerThreshold = time.Hour

// holdTimer tries to register a dehydratable timer's deadline with the engine
// timer service (SRD-071 FR-3/FR-6): the durable holder that fires it even
// after the instance releases its goroutines. It returns true only when the
// deadline was handed off — no in-hub waiter is then created for it, and the
// track is marked held so the idle detector may release the instance. It
// returns false (keep the in-hub waiter) when there is no holder, the deadline
// was not stashed, it is within the threshold (too short to be worth it), or
// the holder declined — never losing the timer.
func (t *track) holdTimer(d flow.EventDefinition) bool {
	inst := t.instance
	if inst.waitHolders == nil {
		return false
	}

	t.m.RLock()
	deadline, cycles := t.timerDeadline, t.timerCycles
	t.m.RUnlock()

	// M3 releases only one-shot timers (cyclesLeft == 0 — a bare Time/Duration
	// timer). A repeating timer (cyclesLeft > 0) would lose its later cycles to
	// the fire-once wake, so it stays resident on its in-hub waiter until a
	// cyclic-aware holder lands.
	if cycles > 0 {
		return false
	}

	if deadline.IsZero() ||
		deadline.Before(inst.now().Add(dehydrationTimerThreshold)) {
		return false
	}

	if err := inst.waitHolders.HoldTimer(
		inst.ID(), t.ID(), d, deadline, cycles, exec.WaitNode); err != nil {
		// the holder declined — keep the wait resident on an in-hub waiter
		// rather than lose the timer (SRD-071 FR-3, the never-a-lost-trigger
		// invariant).
		return false
	}

	return true
}

// holdTask hands a parked human task to the engine's durable holders (SRD-071
// FR-8): the task already lives in the distributor's inbox independent of the
// instance's residency, so the hold only has to tell the engine WHICH track the
// task belongs to — an action on it then wakes that instance. Returns false (the
// wait stays resident) without a holder registry.
func (t *track) holdTask(flow.Node) bool {
	inst := t.instance
	if inst.waitHolders == nil {
		return false
	}

	return inst.waitHolders.HoldTask(inst.ID(), t.ID(), t.taskID) == nil
}

// releaseHolds withdraws every engine-level hold this track's wait registered —
// its timer deadline, its subscriptions, the whole set an Event-Based Gateway
// armed (SRD-071 FR-3b). It is the ONE place that withdrawal happens, and every
// path a wait can exit by calls it: the wait fired (applyWaitPlane), the track
// ended or failed without delivery (the loop's evEnded/evFailed — an
// interrupting boundary canceling its host lands here), or the instance tore
// down (stopAll). The one path that deliberately does NOT call it is
// dehydration: there the hold is the wake source and must outlive the
// goroutine.
//
// Idempotent — the flag is swapped, so a teardown following a delivery is a
// no-op. A hold that outlives its wait is never benign: a stale deadline or
// subscription wakes a later cycle for a wait that no longer exists.
func (t *track) releaseHolds() {
	// The hold belongs to the ACTIVITY, not to the instance that just
	// delivered (SRD-090.B FR-2). ReleaseWaits withdraws every hold taken
	// for this track, so releasing while a sibling still waits would leave
	// that sibling with nothing able to wake a released instance — the
	// sibling-teardown failure ADR-006 §2.9.5 names, one layer below the
	// hub.
	if owner := t.activityOwner(); owner != nil && owner.anyWaiting() {
		return
	}

	if t.held.Swap(false) && t.instance.waitHolders != nil {
		t.instance.waitHolders.ReleaseWaits(t.instance.ID(), t.ID())
	}
}

// holdableSubscriptions are the triggers whose hub subscription the engine can
// hold on a released instance's behalf (SRD-071 FR-7). A Conditional is absent
// by construction — it never reaches here (its subscription is loop-owned).
var holdableSubscriptions = set.New[flow.EventTrigger](
	flow.TriggerMessage,
	flow.TriggerSignal,
)

// holdSubscription tries to hand a message/signal wait's hub subscription to
// the engine holder (SRD-071 FR-3/FR-7) — the durable subscriber that survives
// the instance's release. It returns true only when the holder took it, in
// which case the instance registers NO subscription of its own and the track is
// marked held so the idle detector may release the instance. It returns false
// (register as before, stay resident) when there is no holder registry or the
// holder declined — never losing the trigger.
func (t *track) holdSubscription(d flow.EventDefinition) bool {
	inst := t.instance
	if inst.waitHolders == nil {
		return false
	}

	// the conversation keys the instance's OWN registration would contribute
	// (SRD-017 §4.3): the holder must subscribe to the same conversation.
	if err := inst.waitHolders.HoldSubscription(
		inst.ID(), t.ID(), d, inst.CorrelationKeys(), exec.WaitNode); err != nil {
		return false
	}

	return true
}

// stashTimerPlan records a timer wait's firing plan for the checkpoint
// (SRD-070 FR-3): the ABSOLUTE deadline captured at arming is the
// restore authority — re-evaluating a Duration would restart it. A
// non-timer definition or an unevaluable plan stashes nothing (the
// checkpoint simply carries no descriptor).
func (t *track) stashTimerPlan(
	d flow.EventDefinition, proc eventproc.EventProcessor,
) {
	if d == nil || d.Type() != flow.TriggerTimer {
		return
	}

	deadline, cycles, err := waiters.TimerPlan(d, proc, t.instance)
	if err != nil {
		return
	}

	// Under the track mutex: the checkpoint capture (the loop goroutine)
	// reads these while this track's goroutine arms (SRD-070 FR-4).
	t.m.Lock()
	t.timerDeadline = deadline
	t.timerCycles = cycles
	t.m.Unlock()
}

// checkActivityWaitKind classifies the non-event wait nodes (done=true when
// the node was recognized and handled): a UserTask parks for a human Complete
// (SRD-034); a Call Activity parks for its child instance (SRD-050); a
// ServiceTask marked WithWorker parks for the worker's report (SRD-036 —
// checked after the call capability, a CallActivity is not an
// ExternalWorker). Each is recognized by capability, keeping the runtime
// model-agnostic.
//
// A COMPOSITE is deliberately absent (SRD-090.A M3c). It used to park here,
// which made entering a Sub-Process a wait — and it is not one: the token
// forks into a child scope and the body's own tracks do the waiting. Its
// executor opens the scope from the step instead, like every other activity
// instance, which is FR-2's one decision and FR-11's transparency in the
// same move.
func (t *track) checkActivityWaitKind(
	e *nodeExec,
	node flow.Node,
	atConstruction bool,
) (bool, error) {
	if _, ok := node.(interactor.HumanTask); ok {
		// An ITERATED activity is parked by its INSTANCES, not by the arrival
		// that reaches the activity. Arrival happens once however many times
		// the node runs, so parking here would announce one task for the
		// activity on top of the one each instance announces for itself —
		// and that activity-level task belongs to no execution, so nothing
		// would ever complete it.
		//
		// e != nil means an instance IS the caller, which is the case that
		// must go through.
		if e == nil && drivesOwnIteration(node) {
			return true, nil
		}

		return true, t.parkHumanTask(e, node)
	}

	if _, ok := node.(interface{ CalledKey() string }); ok {
		return true, t.parkCallActivity(node, atConstruction)
	}

	if ew, ok := node.(tasks.ExternalWorker); ok {
		if _, isWorker := ew.WorkerTopic(); isWorker {
			return true, t.parkServiceTask(node)
		}
	}

	return false, nil
}

// checkThrowNode classifies a throw event (only throwEvent carries the
// CompensationWaitRef capability): a wait-for-completion Compensation throw
// parks as a wait node (SRD-059 FR-5); every other throw never parks — it
// emits its definitions in Exec (done=true, no error). done=false for a
// non-throw node.
func (t *track) checkThrowNode(
	node flow.Node,
	atConstruction bool,
) (bool, error) {
	tw, ok := node.(interface{ CompensationWaitRef() (string, bool) })
	if !ok {
		return false, nil
	}

	if ref, wait := tw.CompensationWaitRef(); wait {
		return true, t.parkCompensationThrow(node, ref, atConstruction)
	}

	return true, nil
}

// parkCompensationThrow parks the track on a wait-for-completion Compensation
// throw (SRD-059 FR-5): the thrower waits on evtCh until the loop's sweep
// drains and delivers the completion sentinel. Mid-run the loop is told via
// evCompensate; at construction (a fork born ON the throw, which runs on the
// loop goroutine — the SRD-048 deadlock rule) the spawn path starts the sweep
// via recordBornWaiter instead.
func (t *track) parkCompensationThrow(
	node flow.Node,
	ref string,
	atConstruction bool,
) error {
	t.compWaitRef = ref
	t.updateState(TrackWaitForEvent)

	if !atConstruction && t.instance.State() == Active {
		// DEFERRED: checkFlows emits this AFTER its evMoved, so the ledger
		// entry of the just-completed predecessor (carried on that evMoved)
		// lands before the sweep resolves — FIFO makes the completion visible
		// to its own downstream throw (SRD-059 FR-6).
		t.pendingCompensate = &trackEvent{
			kind:     evCompensate,
			track:    t,
			node:     node,
			compRef:  ref,
			compWait: true,
		}
	}

	return nil
}

// parkCallActivity parks the track on a Call Activity (SRD-050 FR-5): the host
// waits on evtCh for the child instance's completion. Mid-run the loop is told
// via evCallWaiting; at construction (a fork born ON a Call Activity, which runs
// on the loop goroutine — the SRD-048 deadlock rule) the spawn path launches the
// call via recordBornWaiter instead.
func (t *track) parkCallActivity(node flow.Node, atConstruction bool) error {
	t.updateState(TrackWaitForEvent)

	if !atConstruction && t.instance.State() == Active {
		t.instance.emit(trackEvent{
			kind:  evCallWaiting,
			track: t,
			node:  node,
		})
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
func (t *track) parkHumanTask(e *nodeExec, node flow.Node) error {
	taskID, ord := t.humanTaskIdentity(e)

	// THIS execution is what is waiting (ADR-025 §2.13). A concurrent
	// instance records it on itself, so the decorator's await is the
	// conjunction over its instances rather than one instance's answer
	// standing for all of them (§2.13b.1e).
	if e != nil {
		e.parked.Store(true)
	}

	t.updateState(TrackWaitForEvent)

	// hand the task to the engine's holder, so an action on it can wake a
	// released instance (SRD-071 FR-3/FR-8). Declined (no registry) → the wait
	// simply stays resident.
	t.held.Store(t.holdTask(node))

	if t.instance.State() == Active {
		t.instance.emit(trackEvent{
			kind:      evTaskWaiting,
			track:     t,
			node:      node,
			taskID:    taskID,
			ord:       ord,
			iterLocal: iterationDataOf(e),
		})
	}

	return nil
}

// humanTaskIdentity gives this execution its parked-work identity and returns
// it with the ordinal that owns it.
//
// An ITERATED activity's identity belongs to the INSTANCE, not to the track
// they share: N instances parked at once must announce N addressable tasks, or
// only one is reachable and the rest complete without anyone doing them
// (ADR-020 §2.12). The decorator also learns the instance is parked, which is
// what opens its delivery box — a capability wait has no event definition to
// open one as a side effect of.
//
// A LONE activity keeps the track's single slot, so nothing about the
// non-iterated path changes.
//
// Either way the id is minted once and reused while the instance stays parked:
// a RESTORED execution carries its recorded id (SRD-071 FR-8), because the task
// outlives the instance's residency in the distributor's inbox and re-minting
// would invalidate the reference someone is about to act on.
func (t *track) humanTaskIdentity(e *nodeExec) (string, int) {
	owner := t.activityOwner()
	if owner == nil {
		t.m.Lock()
		defer t.m.Unlock()

		if t.taskID == "" {
			t.taskID = foundation.GenerateID()
		}

		return t.taskID, 0
	}

	// a CONCURRENT instance knows its own ordinal; the track's current
	// executor is the decorator, whose ordinal is the activity's, not this
	// instance's.
	ord := t.execOrdinal()
	if e != nil {
		ord = e.ord
	}

	// a restored instance adopts the id its checkpoint recorded before it is
	// asked for one, so the recorded value wins over a fresh mint.
	t.m.RLock()
	recorded := t.taskID
	t.m.RUnlock()

	owner.adoptTaskID(ord, recorded)
	owner.parking(ord)

	id := owner.taskIDFor(ord)
	t.rememberTaskID(ord, id)

	return id, ord
}

// parkServiceTask parks a worker-dispatched ServiceTask as an external-worker
// wait node (SRD-036): it mints a JobID (embedding the instance id so a
// completion routes back to this instance), enters TrackWaitForEvent, and emits
// evJobWaiting so the loop binds the operation input and enqueues the job. The
// track then waits on its evtCh for the worker's outcome, exactly like a UserTask
// waits for a Complete. A ServiceTask is never an initial node (it always has an
// incoming flow), so it is reached mid-run with the instance Active — the
// construction (spawn) path parkHumanTask guards against is unreachable here.
func (t *track) parkServiceTask(node flow.Node) error {
	jobID := tasks.MakeJobID(t.instance.ID())

	t.updateState(TrackWaitForEvent)

	if t.instance.State() == Active {
		t.instance.emit(trackEvent{
			kind:   evJobWaiting,
			track:  t,
			node:   node,
			taskID: string(jobID),
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

// currentState returns the track's state under the read lock.
func (t *track) currentState() trackState {
	t.m.RLock()
	defer t.m.RUnlock()

	return t.state
}

// currentStep returns current step of the track.
func (t *track) currentStep() *stepInfo {
	t.m.RLock()
	defer t.m.RUnlock()

	return t.steps[len(t.steps)-1]
}

// alive reports whether this track can still act — it has not reached a
// terminal state and has not been asked to stop.
//
// Read by the LOOP about another goroutine's track, which is safe because the
// state is mutex-guarded and the answer is only ever used to decline work: a
// track that dies immediately after this returns true simply gets work it
// then abandons, exactly as it would have without the check.
func (t *track) alive() bool {
	if t.stopIt.Load() {
		return false
	}

	return liveTrackStates[t.currentState()]
}

// stop terminates track execution.
func (t *track) stop() {
	t.stopIt.Store(true)
}

// run start execution loop of the track which ends by ctx's cancel or
// when there is no outgoing flows from the processing nodes.
// errStopped unwinds a unit whose track was taken away while it waited — a
// cancellation, a terminate, or the loop closing its channel. awaitTrigger
// has already set the terminal state; the goroutine's remaining job is to
// stop existing, which is what the run loop does with this.
var errStopped = errors.New("the track was stopped while it waited")

// errRedispatch unwinds a unit whose step is no longer the track's current
// one, which delivery can do: an Event-Based Gateway advances onto the arm
// that fired (deliver → advanceToArm), so the gate's unit must yield rather
// than execute a node the token has moved past.
var errRedispatch = errors.New("the delivery moved this token")

// parkForDelivery blocks until this pass's trigger arrives, when the track is
// parked for one (SRD-090.B FR-2). A no-op for every node that does not wait,
// which is nearly all of them.
//
// It replaces the run loop's pre-step gate. Same act, one level down: the
// unit that runs a node is the thing that waits for it, so an activity
// running its node N times waits N times rather than once.
func (t *track) parkForDelivery(
	ctx context.Context, step *stepInfo, e *nodeExec,
) (*data.ItemDefinition, error) {
	// AN INSTANCE OF A FAN-OUT DOES NOT WAIT HERE. Its wait is the
	// DECORATOR's, which holds every instance's and applies their
	// completions serially on its own goroutine (ADR-025 §2.15a) — by the
	// time this runs, the delivery has already arrived and is being applied.
	//
	// It waited here while each instance was a goroutine of its own, and
	// that is the arrangement the ADR rules out: the node, the step list and
	// the track's own fields are the TOKEN's, not an instance's, and N
	// goroutines traversing them is a race with no natural owner.
	if e != nil && e.concurrent {
		return nil, nil
	}

	if !t.inState(TrackWaitForEvent) {
		return nil, nil
	}

	if !t.awaitTrigger(ctx, t.evtCh, e) {
		return nil, errStopped
	}

	// THIS EXECUTION IS RUNNING AGAIN. The flag is what a sequential
	// decorator reads to refuse advancing past a waiting instance
	// (refuseIfParked), so leaving it set after the delivery arrived would
	// stop the iteration one pass in.
	if e != nil {
		e.parked.Store(false)
	}

	// and the handover this pass was counted for is over — see
	// iterDecorator.completeIteration. Balanced rather than reset: the count
	// belongs to the activity, not to this one wait.
	if sub := t.ownerIfResolved(); sub != nil {
		sub.delivered()
	}

	if cur := t.currentStep(); cur != nil && cur.node != step.node {
		return nil, errRedispatch
	}

	// THE PAYLOAD IS THIS INSTANCE'S, and leaves the track with the unit
	// that received it (ADR-006 §2.9.1: the receiving execution captures the
	// item into its own frame).
	//
	// `deliver` stages it on the track, which is a single slot — fine while
	// one execution waits at a time, and wrong the moment N instances of one
	// activity wait concurrently: each would overwrite the others' item
	// before binding it, off its own goroutine, on a field whose own comment
	// says it is track-goroutine owned. Taking it here puts it on the unit's
	// stack, where it belongs to exactly one execution by construction.
	item := t.receivedItem
	t.receivedItem = nil

	return item, nil
}

// awaitTrigger parks the track on its event channel until a trigger arrives, the
// loop releases it (dehydration, SRD-071), the loop closes evtCh on stop, or the
// context is canceled. Zero CPU while parked (SRD-027 FR-1): the loop is the sole
// sender and sole closer, so a delivered event is applied on this goroutine. It
// returns true to continue the run loop (event delivered), false when the
// goroutine must return — setting the terminal state (Canceled / Dehydrated /
// Failed) accordingly.
func (t *track) awaitTrigger(
	ctx context.Context, box chan flow.EventDefinition, e *nodeExec,
) (proceed bool) {
	concurrent := e != nil && e.concurrent

	select {
	case <-ctx.Done():
		t.updateState(TrackCanceled)

		// lastErr IS THE TRACK GOROUTINE'S, and N instances of one activity
		// park here on N goroutines (SRD-090.B M5b) — a cancellation wakes
		// every one of them at once, so writing it from an instance is a
		// data race on a field whose own contract says it has a single
		// writer. It costs nothing to skip: the instance's stop travels back
		// as errStopped, and the track goroutine records the cause when the
		// decorator returns.
		//
		// The same reasoning as receivedItem above, on the other terminal
		// path; updateState is mutex-guarded and idempotent, so it stays.
		if !concurrent {
			t.lastErr = ctx.Err()
		}

		return false

	case <-t.dehydrateCh:
		// the loop released this wait's goroutine (SRD-071 FR-1): the wait is
		// externalized to its holder; exit terminal for the goroutine, retained
		// as a TrackDehydrated record.
		t.updateState(TrackDehydrated)

		return false

	case eDef, ok := <-box:
		if !ok {
			// the loop closed evtCh on stop — terminate like a cancellation.
			t.updateState(TrackCanceled)

			return false
		}

		return t.applyTrigger(ctx, eDef, e)

	// THE TRACK'S OWN CHANNEL STAYS LIVE alongside the instance's box.
	// Only a delivery the DECORATOR routed lands in the box (SRD-090.B
	// FR-3); everything else still arrives here — a wait the engine HELD
	// rather than registered with the hub, a task completion, a call or
	// job outcome. An instance takes whichever comes.
	//
	// For a sequential activity that is exact: one instance waits, so both
	// channels mean the same waiter. A PARALLEL one sharing this channel
	// would hand a holder-delivered trigger to an arbitrary instance —
	// which is M6's, with the restore and residency work that owns the
	// holder seam.
	case eDef, ok := <-t.evtCh:
		if !ok {
			t.updateState(TrackCanceled)

			return false
		}

		return t.applyTrigger(ctx, eDef, e)
	}
}

// applyTrigger delivers a fired definition on the track's own goroutine,
// reporting whether the run loop continues.
func (t *track) applyTrigger(
	ctx context.Context, eDef flow.EventDefinition, e *nodeExec,
) (proceed bool) {
	if err := t.deliver(ctx, eDef, e); err != nil {
		t.lastErr = err
		t.updateState(TrackFailed)

		return false
	}

	return true
}

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

		nextFlows, err := t.executeStep(ctx, step)
		if err != nil {
			// the loop released this executor while it held something open
			// (SRD-090.A FR-8). Not a discard and not a failure: the
			// activity is mid-flight and stays that way durably, the
			// release already set TrackDehydrated, and the goroutine's
			// only remaining job is to stop existing.
			if errors.Is(err, errDehydrated) || errors.Is(err, errStopped) {
				return
			}

			// the delivery moved this token — an Event-Based Gateway
			// advances onto its winning arm inside deliver — so the unit
			// yielded rather than executing a step that is no longer
			// current. Re-dispatch on the step that IS.
			if errors.Is(err, errRedispatch) {
				continue
			}

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

	// A typed BpmnError is a thrown BPMN fault (SRD-041 §3.4): announce Thrown
	// before the track flips to Failed, so the stream carries the fault triple
	// Thrown → (Caught | Uncaught). An untyped error is a technical failure, not
	// a BPMN fault, and only surfaces as the instance's Failed state.
	var be *events.BpmnError
	if errors.As(err, &be) {
		node := t.currentStep().node
		t.instance.report(observability.Fact{
			Kind:     observability.KindFault,
			Phase:    observability.PhaseThrown,
			NodeID:   node.ID(),
			NodeName: node.Name(),
			Details:  map[string]string{observability.AttrError: be.Code},
		})
	}

	// the per-step failure record (SRD-079 FR-11): the failing step is marked
	// before the track-level terminal state, so the step history names where
	// the failure happened, not just that it did.
	t.currentStep().state = StepFailed

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
	// through a UNIT, not straight into the sequence (ADR-025 §2.13b). Its
	// callers — a decorator's exit, a composite's exit — are running the
	// node as the activity's single instance, which is precisely what a
	// unit is, and routing them through it leaves ONE path from "execute
	// this node" to the sequence that does it.
	return newNodeExec(t, step, 0).run(ctx)
}

// executeNodeAs runs the node as ONE instance of its activity.
//
// **The whole sequence belongs to the unit** (ADR-025 §2.13b) — opening the
// frame, seeding it, binding the instance's own data, loading the declared
// inputs, running the node, the cancellation checkpoint, the commit — which
// is why this reads as one function rather than as steps a caller strings
// together. A decorator wraps THIS, not a sequence it has to reproduce.
//
// It remains a track method because every phase legitimately touches track
// state (the compensation seed, the received item, the step, the history);
// the unit owns the ORDER, which is what makes it wrappable.
//
// ai carries what distinguishes this instance from its siblings: the data
// only it sees, and the capture that takes its result before the commit makes
// the output's name a shared one. A plain node is the degenerate case — one
// instance, no local data.
func (t *track) executeNodeAs(
	ctx context.Context,
	step *stepInfo,
	ai activityIteration,
) ([]*flow.SequenceFlow, error) {
	// THE UNIT PARKS ITS OWN PASS (SRD-090.B FR-2). Before this, parking was
	// the run loop's pre-step gate — once per STEP, above executeStep — so
	// every pass of an iterated activity ran below it and, after the first
	// delivery, executed its node without waiting at all. That was #313's
	// defect surviving the ownership change.
	//
	// Ahead of the frame open, because the delivery's payload is captured
	// INTO that frame (ADR-006 §2.9.1): the receiving execution binds its own
	// item, so it must have received it by the time the frame exists.
	received, err := t.parkForDelivery(ctx, step, ai.exec)
	if err != nil {
		return nil, err
	}

	// a fan-out's instance arrives with its payload already in hand: the
	// decorator took the delivery and is applying it (ADR-025 §2.15a).
	if received != nil {
		ai.received = received
	}

	ne, ok := step.node.(exec.NodeExecutor)
	if !ok {
		return nil,
			errs.New(
				errs.M("node doesn't provide exec.NodeExecutor interface"),
				errs.C(errorClass, errs.TypeCastingError),
				errs.D(observability.AttrNodeID, step.node.ID()),
				errs.D(observability.AttrNodeName, step.node.Name()))
	}

	f, err := t.instance.sc.openFrameAt(t.ID(), step.node.ID(), t.scopePath)
	if err != nil {
		return nil,
			errs.New(
				errs.M("couldn't create the execution frame for node %q",
					step.node.Name()),
				errs.C(errorClass, errs.OperationFailed),
				errs.E(err))
	}

	// Stage this instance's payload as soon as its frame exists, not at
	// finalize: the node's own Exec reads it through the execution
	// environment (execEnv.ReceivedItem), which resolves from the FRAME —
	// per execution — before falling back to the track's single slot. A
	// ReceiveTask produces its output from it, so binding it later starved
	// the node that needed it (SRD-090.B M5a).
	if ai.received != nil {
		f.SetReceived(ai.received)
	}
	defer f.Discard()

	// A compensation-handler track reads the ledger entry's snapshot, not the
	// current scope (SRD-059 FR-4, ADR-026 §2.5): the snapshot params are
	// seeded as frame INPUTS — resolved frame-first on reads, and never
	// committed (Commit pushes only outputs and puts, so writes go live).
	if t.compFrameSeed != nil {
		if serr := seedFrameInputs(f, t.compFrameSeed); serr != nil {
			return nil, serr
		}
	}

	// ONE ITERATION of an activity carries its own data frame-local (ADR-025
	// §2.2, SRD-090.A FR-4): binding it at the shared container scope is safe
	// only while one iteration runs at a time, and a parallel Multi-Instance
	// runs them at once. Bound before the node loads its inputs, which
	// resolve frame-first through it.
	if len(ai.local) > 0 {
		if berr := f.BindLocal(ai.local...); berr != nil {
			return nil, berr
		}
	}

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

	if err := t.finalizeNodeExecution(ctx, step, f, ai); err != nil {
		return nil, err
	}

	return nexts, nil
}

// activityIteration is what distinguishes ONE instance of an activity from its
// siblings when a decorator drives several of them (ADR-025 §2.13).
type activityIteration struct {
	// exec is the EXECUTOR running this instance, when one owns it. It rides
	// here so a park is recorded against the execution that is waiting rather
	// than against the track its siblings share (ADR-025 §2.13: "a node
	// executor … owns that node's wait").
	//
	// nil for a plain activity, whose track and execution are the same thing.
	exec *nodeExec

	// capture, when set, is called with this execution's frame once the node
	// has produced its outputs and BEFORE they commit to the shared
	// container scope. It is how a decorator takes ONE instance's declared
	// output for positional assembly (ADR-025 §2.6): with no
	// per-instance scope to read it from, concurrent siblings overwrite the
	// output's name in the container scope, so the value has to be taken
	// while it is still this instance's own.
	capture func(f *scope.Frame) error

	// local is this instance's own data — the 0-based loopCounter and, for a
	// collection-driven Multi-Instance, its split input item. Bound
	// frame-local, so a sibling cannot overwrite it and it never reaches the
	// shared container scope (SRD-090.A FR-4).
	// received is the payload THIS execution's delivery carried, taken off
	// the track by parkForDelivery and bound into this instance's own frame
	// (ADR-006 §2.9.1, SRD-090.B M5a). nil for a node that did not wait.
	received *data.ItemDefinition

	local []data.Data
}

// prepareNodeExecution marks the step started and runs the consumer role:
// the node loads its inputs and properties into the execution frame.
func (t *track) prepareNodeExecution(
	ctx context.Context,
	step *stepInfo,
	f *scope.Frame,
) error {
	if t.stepTransitionsVisible() {
		t.updateState(TrackExecutingStep)
		t.record(TrackExecutingStep) // record this node visit (path + timing)
	}

	step.state = StepStarted

	return t.loadIncomingData(ctx, step.node, f)
}

// stepTransitionsVisible reports whether THIS execution is a step of the
// token, and so should move the track's state and add a history entry.
//
// It is false exactly while the track is ITERATING: an activity is one step
// of its token however many instances run it, so the instances fall below
// the state machine's granularity (ADR-025 §2.13b.1e). Reporting each one
// would say a five-instance activity was five step executions, and the
// history entry is a read-copy-store over an atomic pointer rather than a
// CAS — so concurrent instances would drop entries rather than miscount
// loudly.
//
// This replaced an `inSet` flag threaded from the decorator, through the
// executor, into here: a piece of "I am one of N" traveling inward through
// three layers to ask the driver to do less. The track knows its own state,
// so nothing needs to be passed.
func (t *track) stepTransitionsVisible() bool {
	return !t.inState(TrackIterating)
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

	nexts, err := ne.Exec(ctx, newExecEnv(t.instance, f, t))
	if err != nil {
		return nil, err
	}

	// A diverging gateway's execution IS its branch decision (SRD-041 §3.4):
	// announce the chosen outgoing flows here, at the one node-execution site,
	// rather than coupling each gateway's Exec to observation. A converging
	// gateway (single outgoing merge/join) is a pass-through, not a decision.
	if _, isGateway := step.node.(gatewayNode); isGateway &&
		len(step.node.Outgoing()) > 1 {
		t.announceGatewayDecision(step.node, nexts)
	}

	return nexts, nil
}

// gatewayNode marks a gateway among executed nodes: only gateways carry a default
// flow. It attributes a branch decision without NodeType() (which panics on the
// bare BaseNode some tests use) and without importing the gateways package.
type gatewayNode interface {
	DefaultFlow() *flow.SequenceFlow
}

// announceGatewayDecision emits a gateway's chosen branches (SRD-041 §3.4): the
// gateway node and the ids of the outgoing flows its Exec selected.
func (t *track) announceGatewayDecision(
	node flow.Node,
	chosen []*flow.SequenceFlow,
) {
	ids := make([]string, 0, len(chosen))
	for _, cf := range chosen {
		ids = append(ids, cf.ID())
	}

	t.instance.report(observability.Fact{
		Kind:     observability.KindGatewayDecision,
		Phase:    observability.PhaseBranchesChosen,
		NodeID:   node.ID(),
		NodeName: node.Name(),
		Details:  map[string]string{observability.AttrChosenFlows: strings.Join(ids, ",")},
	})
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
	ai activityIteration,
) error {
	step.state = StepEnded

	if t.stepTransitionsVisible() {
		t.updateState(TrackProcessStepResults)
	}

	// stage the delivery's payload for the catch node's binding
	// (SRD-085 FR-1). It arrives on the instance rather than the track now
	// (parkForDelivery), so N concurrent instances of one activity each bind
	// their own; the track field survives only for the paths that stage an
	// item without parking — an Event-Based Gateway's winning arm, whose
	// upload is this call on the ARM's step.
	if ai.received == nil && (ai.exec == nil || !ai.exec.concurrent) &&
		t.receivedItem != nil {
		f.SetReceived(t.receivedItem)
		t.receivedItem = nil
	}

	if err := t.uploadOutgoingData(ctx, step.node, f); err != nil {
		return err
	}

	// take this instance's own output before the commit makes the name a
	// shared one (ADR-025 §2.6).
	if ai.capture != nil {
		if cerr := ai.capture(f); cerr != nil {
			return cerr
		}
	}

	// The changed-path set is the activity-boundary change signal — one
	// DataChange fact per changed path (SRD-044 FR-4). A failed Commit
	// returns a nil set, so the report is naturally a no-op then.
	changes, err := f.Commit()
	t.reportDataChanges(step.node, changes)
	t.reportDataMovements(step.node, f)
	t.signalDataCommit(step.node, changes)

	return err
}

// checkFlows processes a node's outgoing flows. The track continues on the
// first (cyclic-preferred) flow carrying its current token; any remaining
// flows are a fork — handed to the loop, which builds one new track per extra
// flow (each new track self-creates its own token on execution). 1:1
// track:token holds: the parent keeps its single token, no split.
func (t *track) checkFlows(flows []*flow.SequenceFlow) error {
	return t.advance(succsOf(flows))
}

// advance applies a settled node's successors: no successor ends the track,
// otherwise one continues it and the rest fork. It is the single implementation
// of the continue/fork/end rules, shared by both sources of succession — a
// node's outgoing sequence flows (checkFlows) and an Ad-Hoc Router's answer
// (SRD-074 §3.1).
func (t *track) advance(succs []successor) error {
	if len(succs) == 0 {
		t.updateState(TrackEnded)
		return nil
	}

	// if any successor is cyclic on the current node, it becomes the
	// track's next step.
	nextNode := 0
	for i, s := range succs {
		if s.node.ID() == t.currentStep().node.ID() {
			nextNode = i
			break
		}
	}

	// the track continues onto the chosen successor (it carries its single
	// position; no token object). inFlow records the flow it arrived on so a
	// synchronizing-join target knows which incoming flow this token came from;
	// it is nil for a Router-chosen activity, which arrived on no branch.
	nextStep := stepInfo{
		node:   succs[nextNode].node,
		inFlow: succs[nextNode].inFlow,
		state:  StepCreated,
	}

	// SRD-059 FR-4: a leaf with a Compensation boundary snapshots its visible
	// data HERE, on the track goroutine right after its own commit — the
	// loop's ledger append runs later, and by then a downstream node may have
	// already committed newer values (commits bypass the loop). The capture is
	// carried on the evMoved emit below.
	var compSnap []data.Data

	t.m.RLock()
	completed := t.steps[len(t.steps)-1].node
	t.m.RUnlock()

	if !opensChildScope(completed) &&
		compensationBoundaryHandlerOf(completed) != nil {
		snap, serr := t.instance.sc.plane.SnapshotAt(t.scopePath)
		if serr != nil {
			return errs.New(
				errs.M("couldn't snapshot %q for the compensation ledger",
					completed.Name()),
				errs.C(errorClass, errs.OperationFailed),
				errs.E(serr))
		}

		compSnap = snap
	}

	// Guard the append: checkNodeType below may register a mid-flow event whose
	// waiter fires synchronously (a broker-buffered message) and reads t.steps
	// from its own goroutine via ProcessEvent -> updateState -> record.
	t.m.Lock()
	t.steps = append(t.steps, &nextStep)
	t.m.Unlock()

	// Report the advance to the loop — the sole owner of the position view
	// (ADR-017 Rule 2, SRD-028 FR-2). The node is carried in the event so the
	// loop never reads currentStep cross-goroutine. Reached only from run()
	// (instance Active), so no construction gating.
	//
	// Emitted BEFORE checkNodeType, and the order is load-bearing (SRD-095
	// FR-8). For a wait node checkNodeType declares the wait and emits
	// evWaiting, which is a checkpoint trigger; the completed predecessor's
	// ledger entry rides on THIS evMoved, so an evWaiting queued ahead of it
	// let the loop checkpoint a ledger one entry short — a restore from a
	// checkpoint taken at a wait right after a compensable activity then
	// aborted without ever compensating it. The emit is a channel send, not
	// a round trip, and the wait is not yet declared, so nothing here widens
	// the window in which a fired event could find no subscriber.
	t.instance.emit(trackEvent{
		kind:         evMoved,
		track:        t,
		node:         nextStep.node,
		compSnapshot: compSnap,
	})

	// The token continues on this track to nextStep's node. newTrack only
	// classified the track's initial node, so a mid-flow event node (e.g. a
	// ReceiveTask reached from an upstream node) must be classified here too —
	// otherwise it would execute without registering its event or parking the
	// track. checkNodeType is a no-op for non-event nodes.
	if err := t.checkNodeType(nextStep.node, false); err != nil {
		return err
	}

	// a wait-for-completion Compensation throw parked in checkNodeType above:
	// its deferred evCompensate goes out now, AFTER the evMoved that ledgers
	// the predecessor (SRD-059 FR-6).
	if t.pendingCompensate != nil {
		ev := *t.pendingCompensate
		t.pendingCompensate = nil
		t.instance.emit(ev)
	}

	// the remaining successors fork: build a fresh slice (don't mutate the
	// caller's) and hand it to the loop, which constructs the new tracks. The
	// track never mutates instance state itself.
	extras := make([]successor, 0, len(succs)-1)
	for i, s := range succs {
		if i != nextNode {
			extras = append(extras, s)
		}
	}

	if len(extras) != 0 {
		t.instance.emit(trackEvent{kind: evFork, track: t, succs: extras})
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
		// a Conditional definition was never hub-registered (SRD-048 FR-7 —
		// loop-owned); unregistering it would be a guaranteed miss.
		if eDef.Type() == flow.TriggerConditional {
			continue
		}

		// Symmetric with arming (SRD-090.B FR-2): when the ACTIVITY owns the
		// subscription, the withdrawal is due only once none of its
		// instances waits on the definition any more. Withdrawing on the
		// first instance to finish would take its siblings' wait with it —
		// the failure ADR-006 §2.9.5 names, and the reason the entry's
		// lifetime is stated as "while any instance awaits" rather than
		// per-pass.
		proc := eventproc.EventProcessor(t)

		if owner := t.activityOwner(); owner != nil {
			if !owner.stopped(eDef, t.execOrdinal()) {
				continue
			}

			proc = owner
		}

		if err := t.instance.UnregisterEvent(proc, eDef.ID()); err != nil {
			return errs.New(
				errs.M("failed to unregister event"),
				errs.C(errorClass, errs.OperationFailed),
				errs.D(observability.AttrTrackID, t.ID()),
				errs.D(observability.AttrEventDefinitionID, eDef.ID()),
				errs.D(observability.AttrEventDefinitionType, string(eDef.Type())),
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
	e *nodeExec,
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

	// THIS delivery's payload is captured by the receiving execution —
	// never by the shared node (ADR-006 v.5 §2.9.1, SRD-085 FR-1). The
	// node's ProcessEvent stays a notification seam.
	//
	// A CONCURRENT instance takes it on ITSELF. The track's slot is one
	// field shared by N instances of the same activity, each delivered to on
	// its own goroutine: two of them writing it is a data race, and one
	// taking the other's payload binds an approval to the wrong instance.
	if e != nil && e.concurrent {
		e.received = msgflow.CaptureItem(eDef)
	} else {
		t.receivedItem = msgflow.CaptureItem(eDef)
	}

	if err := ep.ProcessEvent(ctx, eDef); err != nil {
		return err
	}

	// A wait held by an engine-level holder rather than a hub waiter (SRD-071
	// FR-3) withdraws its holds here: the wait fired, so neither its deadline
	// nor its subscriptions may outlive it — a stale hold would wake a later
	// dehydration cycle spuriously. For an Event-Based Gateway this is also the
	// withdraw-the-losing-siblings step: the whole set goes at once.
	t.releaseHolds()

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

// setLoopCounter publishes the pass ordinal under the track mutex
// (SRD-082): the decorator's runner goroutine writes it while the loop
// reads it — the capture, the iteration mirror and the scope facts.
func (t *track) setLoopCounter(i int) {
	t.m.Lock()
	t.loopCounter = i
	t.m.Unlock()
}

// loopCounterSnap reads the ordinal under the mutex — the loop-side
// reader half of setLoopCounter.
func (t *track) loopCounterSnap() int {
	t.m.RLock()
	defer t.m.RUnlock()

	return t.loopCounter
}

// takeIterSeed hands the restored executor set to the activity starting
// now, and clears it (SRD-090.A FR-7).
//
// The clear is the point. A seed describes the instances of ONE activity —
// the one the track was restored on — and a restored track does not stop
// there: it finishes that activity and walks on through the graph. Left in
// place, the seed would still be sitting on the track when the token reached
// the NEXT iterated activity, whose decorator would read another activity's
// ordinals as its own and skip every instance recorded completed. Those
// instances would never run, and nothing would say so — the run would simply
// produce a shorter result.
//
// Only a decorator calls this, which is exactly the set that can be handed
// one: a seed reaches a track only when the capture wrote an IterationRecord
// for it, and only an iterated activity has one.
func (t *track) takeIterSeed() *checkpoint.IterationRecord {
	seed := t.iterSeed
	t.iterSeed = nil

	return seed
}
