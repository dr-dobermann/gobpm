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
	ctx      context.Context
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
	hist   atomic.Pointer[[]stepUpdate]
	steps  []*stepInfo
	m      sync.RWMutex
	state  trackState
	stopIt atomic.Bool
}

// record appends a track-state transition to the history, copy-on-write, and
// publishes it atomically. It is called from the track's run goroutine and,
// via ProcessEvent -> updateState, from a waiter goroutine, so the read of
// t.steps is guarded by t.m (the same lock checkFlows takes to append a step).
func (t *track) record(state trackState) {
	t.m.RLock()
	node := t.steps[len(t.steps)-1].node
	t.m.RUnlock()

	old := t.hist.Load()

	var base []stepUpdate
	if old != nil {
		base = *old
	}

	next := make([]stepUpdate, len(base), len(base)+1)
	copy(next, base)
	next = append(next, stepUpdate{node: node, state: state, at: t.instance.now()})

	t.hist.Store(&next)
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
// CorrelationKeys returns the conversation key values the track's instance has
// established (SRD-017 §4.3). The message waiter reads it structurally (the
// "declared filter") to subscribe this in-instance receiver keyed to its
// conversation; an instance with no keys yields none, leaving a wildcard
// subscription. It is the subscriber declaring its own filter — the waiter
// never references the instance directly.
func (t *track) CorrelationKeys() []string {
	return t.instance.conversationKeyValues()
}

func (t *track) checkNodeType(node flow.Node) error {
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

	// Declare the wait BEFORE registering: a waiter may deliver an event
	// synchronously on registration (a MessageWaiter draining a message the
	// broker already buffered fires at once), and ProcessEvent only accepts an
	// event while the track is in TrackWaitForEvent. Setting the state first
	// removes that race; timers, which fire later, are unaffected.
	t.updateState(TrackWaitForEvent)

	for _, d := range defs {
		if err := t.instance.RegisterEvent(t, d); err != nil {
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
		step := t.currentStep()

		select {
		case <-ctx.Done():
			t.updateState(TrackCanceled)
			t.lastErr = ctx.Err()

			return

		default:
			if t.stopIt.Load() {
				t.updateState(TrackCanceled)

				return
			}

			if t.inState(TrackWaitForEvent) {
				continue
			}
		}

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
			t.lastErr = err
			t.updateState(TrackFailed)

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
	sj, ok := step.node.(exec.SynchronizingJoin)
	if !ok || len(step.node.Incoming()) <= 1 {
		return true
	}

	var inFlowID string
	if step.inFlow != nil {
		inFlowID = step.inFlow.ID()
	}

	complete, merged := sj.Arrive(inFlowID, t.ID())
	if !complete {
		// not the completing arrival: become AwaitingMerge and stop. The run
		// goroutine returns; the loop is told via evAwaiting (emitted by the
		// spawn wrapper, which sees the final state).
		t.updateState(TrackAwaitingMerge)

		return false
	}

	// declare the merge: the loop flips the absorbed tracks to Merged. prev is
	// not touched — see the doc comment on why convergence is not a parent edge.
	t.instance.emit(trackEvent{kind: evMerged, track: t, mergedIDs: merged})

	return true
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

	f, err := scope.NewFrame(
		t.ID(), step.node.ID(),
		t.instance.dataPlane.Root(), t.instance.dataPlane)
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
			errs.M("node %q[%s] doesn't implement flow.EventNode interface"))
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

// ProcessEvent delivers a fired event to the waiting node, unregisters the
// node's event definitions, and returns the track to the Ready state so run()
// resumes it. Implements eventproc.EventProcessor.
func (t *track) ProcessEvent(
	ctx context.Context,
	eDef flow.EventDefinition,
) error {
	if !t.inState(TrackWaitForEvent) {
		return errs.New(
			errs.M("track #%s of instance #%s doesn't expect any event",
				t.ID(), t.instance.ID()),
			errs.C(errorClass, errs.InvalidState),
			errs.D("event_trigger", string(eDef.Type())),
			errs.D("event_definition_id", eDef.ID()))
	}

	if ctx == nil {
		ctx = t.ctx
	}

	// ProcessEvent runs on a waiter goroutine; guard the t.steps read against
	// the run goroutine's checkFlows append (same t.m).
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

	// Conversation-token rules BEFORE the node processes (SRD-017 §4.5): a
	// correlation mismatch (a held key whose value differs) means the message
	// isn't for this conversation — reject it so the receiver keeps waiting,
	// without advancing the token. Otherwise any new key is associated here.
	if t.instance.validateAndAssociate(ctx, eDef) {
		return eventproc.ErrRejected
	}

	if err := ep.ProcessEvent(ctx, eDef); err != nil {
		return err
	}

	if err := t.unregisterEvent(n); err != nil {
		return errs.New(
			errs.M("node %q[%s] unregister events failed", n.Name(), n.ID()),
			errs.E(err))
	}

	t.updateState(TrackReady)

	return nil
}

// -----------------------------------------------------------------------------
