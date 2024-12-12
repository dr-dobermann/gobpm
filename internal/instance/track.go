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
//   - Node execution goes in three steps:
//
//     1. It starts from Prologue, if Node supports this interface.
//
//     2. If Prologue doesn't return any error, then started node Execute.
//     If node Execution finished successfully, it returns a list of
//     outgoing flows.
//
//     3. If node supports Epilogue, then Epilogue started.
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
	"sync"

	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/internal/exec"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// trackState represent the state of the whole track
type trackState uint8

const (
	TrackCreated trackState = iota
	TrackReady

	// Intermediate
	TrackExecutingStep
	TrackProcessStepResults
	TrackWaitForEvent

	// Final statuses
	TrackMerged
	TrackEnded
	TrackCanceled
	TrackFailed
)

func (t trackState) String() string {
	return []string{
		"TrackCreated",
		"TrackReady",
		"TrackExecutingStep",
		"TrackProcessStepResults",
		"TrackWaitForEvent",
		"TrackWaitForInteraction",
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
	StepCreated stepState = iota
	StepStarted
	StepPrologued
	StepExecuting
	StepEpilogued
	StepAwaitsResults
	StepEnded
	StepFailed
)

func (ss stepState) String() string {
	return []string{
		"Created",
		"Started",
		"StepPrologued",
		"StepExecuting",
		"StepEpilogued",
		"AwaitsResults",
		"Ended",
		"Failed",
	}[ss]
}

// stepInfo keeps information about single track step
type stepInfo struct {
	node  flow.Node
	state stepState
	tk    *token
}

// track processed single line of the process from start noed or
// from fork of sequence flow.
type track struct {
	foundation.ID

	m sync.RWMutex

	ctx context.Context

	state   trackState
	prev    []*track
	steps   []*stepInfo
	lastErr error

	instance *Instance

	stopIt bool
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

	t := track{
		ID:   *foundation.NewID(),
		prev: []*track{},
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
		t.prev = append(t.prev, append(prevTrack.prev, prevTrack)...)
	}

	if err := t.checkNodeType(start); err != nil {
		return nil, err
	}

	return &t, nil
}

// checkNodeType determines if node awaits for event or human interaction
// and updates track state on positive comparison.
func (t *track) checkNodeType(node flow.Node) error {
	if en, ok := node.(flow.EventNode); ok {
		edCnt := 0

		for _, d := range en.Definitions() {
			if err := t.instance.RegisterEvent(t, d); err != nil {
				return errs.New(
					errs.M("couldn't register event definitions"),
					errs.C(errorClass, errs.BulidingFailed),
					errs.D("node_id", en.Id()),
					errs.D("node_name", en.Name()),
					errs.D("event_definition_id", d.Id()),
					errs.E(err))
			}

			edCnt++
		}

		if edCnt != 0 {
			t.updateState(TrackWaitForEvent)
		}
	}

	return nil
}

// inState checks if track state is equal to any track state from the ss.
func (t *track) inState(ss ...trackState) bool {
	t.m.RLock()
	state := t.state
	t.m.RUnlock()

	for _, s := range ss {
		if state == s {
			return true
		}
	}

	return false
}

// updateState sets new state for the track if its not in final state.
// If track has a token, its state will be updated accordingly.
func (t *track) updateState(newState trackState) {
	t.m.RLock()
	state := t.state
	step := t.steps[len(t.steps)-1]
	t.m.RUnlock()

	if state == newState {
		return
	}

	ts := TokenInvalid

	switch newState {
	case TrackReady, TrackExecutingStep:
		ts = TokenAlive

	case TrackWaitForEvent:
		ts = TokenWaitForEvent

	case TrackFailed, TrackEnded, TrackCanceled:
		ts = TokenConsumed
	}

	if step.tk != nil {
		if err := step.tk.updateState(ts); err != nil {
			errs.Panic(err)

			return
		}
	}

	errMsg := "<no error>"
	if t.lastErr != nil {
		errMsg = t.lastErr.Error()
	}

	t.instance.show("TRACK.UPDATE", "state updated",
		map[string]any{
			"track_id":   t.Id(),
			"old_state":  state.String(),
			"new_state":  newState.String(),
			"step_state": step.state.String(),
			"node":       step.node.Name(),
			"error":      errMsg,
		})

	t.m.Lock()
	t.state = newState
	t.m.Unlock()
}

// currentStep returns current step of the track.
func (t *track) currentStep() *stepInfo {
	t.m.RLock()
	defer t.m.RUnlock()

	return t.steps[len(t.steps)-1]
}

// stop terminates track execution.
func (t *track) stop() {
	t.m.Lock()
	defer t.m.Unlock()

	t.stopIt = true
}

// run start execution loop of the track which ends by ctx's cancel or
// when there is no outgoing flows from the processing nodes.
//
//nolint:funlen
func (t *track) run(
	ctx context.Context,
) {
	if t.stopIt || !t.inState(TrackReady, TrackWaitForEvent) {
		t.instance.show("TRACK.FAILED", "not in runnable state",
			map[string]any{
				"track_id":    t.Id(),
				"stop_flag":   t.stopIt,
				"track_state": t.state.String(),
			})

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
			if t.stopIt {
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

		nextFlows, err := t.executeNode(ctx, step)
		if err != nil {
			t.lastErr = err
			t.updateState(TrackFailed)

			return
		}

		err = t.checkFlows(ctx, nextFlows)
		if err != nil {
			t.lastErr = err
			t.updateState(TrackFailed)

			return
		}
	}
}

// executeNode tries to execute flow.Node n.
// On succes it returns a list (probably empty) of outgoing sequence flows.
// On failure it returns error.
//
//nolint:funlen
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

	// create a new token if track doesn't have yet
	if step.tk == nil {
		step.tk = newToken(t.instance, t)
	}

	t.updateState(TrackExecutingStep)

	step.state = StepStarted

	if err := t.loadIncomingData(ctx, step.node); err != nil {
		return nil, err
	}

	ndl, ok := step.node.(scope.NodeDataLoader)
	if ok {
		err := t.instance.ExtendScope(ndl)
		if err != nil {
			return nil, err
		}

		defer func() {
			_ = t.instance.LeaveScope(ndl)
		}()
	}

	if err := t.runNodePrologue(ctx, step.node); err != nil {
		return nil, err
	}

	step.state = StepExecuting

	nexts, err := ne.Exec(ctx, t.instance)
	if err != nil {
		return nil, err
	}

	t.instance.show("TRACK.RUN", "node executed successfully",
		map[string]any{
			"track_id":       t.Id(),
			"node":           step.node.Name(),
			"outgoing_flows": len(nexts),
		})

	if err := t.runNodeEpilogue(ctx, step.node); err != nil {
		return nil, err
	}

	step.state = StepEnded

	if err := t.uploadOutgoingData(ctx, step.node); err != nil {
		return nil, err
	}

	return nexts, nil
}

// checkFlows processes node outgoing flows.
// If number of flows is greater than 1 then new tracks with splited token
// created.
func (t *track) checkFlows(ctx context.Context, flows []*flow.SequenceFlow) error {
	if len(flows) == 0 {
		t.updateState(TrackEnded)
		return nil
	}

	// if outgoing flows has any cyclic on current node then first of them
	// should be next step of the track
	nextNode := 0
	for i, f := range flows {
		if f.Target().Id() == t.currentStep().node.Id() {
			nextNode = i
			break
		}
	}

	tokens := t.currentStep().tk.split(len(flows))

	// create a new step for main track and put token from current step
	// into it.
	nextStep := stepInfo{
		node:  flows[nextNode].Target().Node(),
		state: StepCreated,
		tk:    tokens[0],
	}

	if err := nextStep.tk.updateState(TokenAlive); err != nil {
		return errs.New(
			errs.M("couldn't update token state of the main flow"),
			errs.D("node_name", nextStep.node.Name()),
			errs.D("node_id", nextStep.node.Id()),
			errs.D("instance_id", t.instance.Id()),
			errs.E(err))
	}

	t.steps = append(t.steps, &nextStep)

	// for every new flow create a new track with new tokens.
	for i, f := range append(flows[:nextNode], flows[nextNode+1:]...) {
		nt, err := newTrack(f.Target().Node(), t.instance, t)
		if err != nil {
			return errs.New(
				errs.M("couldn't creaete a new track for flow %q", f.Id()),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(err))
		}

		nt.steps[0].tk = tokens[i+1]

		if err := t.instance.addTrack(ctx, nt); err != nil {
			return errs.New(
				errs.M("couldn't add new track for flow %q", f.Id()),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(err))
		}
	}

	return nil
}

// runNodePrologue runs node Prologue if it supported.
func (t *track) runNodePrologue(ctx context.Context, n flow.Node) error {
	np, ok := n.(exec.NodePrologue)
	if !ok {
		t.instance.show("TRACK.RUN", "no prologue",
			map[string]any{
				"track_id":  t.Id(),
				"node_name": n.Name(),
			})

		return nil
	}

	t.currentStep().state = StepPrologued

	if err := np.Prologue(ctx, t.instance); err != nil {
		t.instance.show("TRACK.RUN", "prologue failed",
			map[string]any{
				"track_id":  t.Id(),
				"node_name": n.Name(),
				"error":     err.Error(),
			})

		return err
	}

	t.instance.show("TRACK.RUN", "prologue finished",
		map[string]any{"track_id": t.Id()})

	return nil
}

// runNodeEpilogue runs node Epilogue if node supports it.
func (t *track) runNodeEpilogue(ctx context.Context, n flow.Node) error {
	ne, ok := n.(exec.NodeEpliogue)
	if !ok {
		t.instance.show("TRACK.RUN", "no epilogue",
			map[string]any{
				"track_id":  t.Id(),
				"node_name": n.Name(),
			})

		return nil
	}

	t.currentStep().state = StepEpilogued

	if err := ne.Epilogue(ctx, t.instance); err != nil {
		t.instance.show("TRACK.RUN", "epilogue failed",
			map[string]any{
				"track_id":  t.Id(),
				"node_name": n.Name(),
				"error":     err.Error(),
			})

		return err
	}

	t.instance.show("TRACK.RUN", "epilogue finished",
		map[string]any{
			"track_id":  t.Id(),
			"node_name": n.Name(),
		})

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
		if err := t.instance.UnregisterEvent(t, eDef.Id()); err != nil {
			return errs.New(
				errs.M("failed to unregister event"),
				errs.C(errorClass, errs.OperationFailed),
				errs.D("track_id", t.Id()),
				errs.D("event_definition_id", eDef.Id()),
				errs.D("event_definition_type", eDef.Type()),
				errs.E(err))
		}
	}

	return nil
}

// loadIncomingData checks if the flow.Node n implements flow.NodeDataConsumer
// and if so, calls the LoadData of the Node from input DataObjects.
func (t *track) loadIncomingData(ctx context.Context, n flow.Node) error {
	dc, ok := n.(scope.NodeDataConsumer)
	if !ok {
		return nil
	}

	return dc.LoadData(ctx)
}

// uploadOutgoingData checks if the flow.Node n impmements flow.NoadDataProducer
// and if so, calls the UploadData of the Node.
func (t *track) uploadOutgoingData(ctx context.Context, n flow.Node) error {
	dp, ok := n.(scope.NodeDataProducer)
	if !ok {
		return nil
	}

	return dp.UploadData(ctx, t.instance)
}

// --------------------- exec.EventProcessor interface -------------------------

func (t *track) ProcessEvent(
	ctx context.Context,
	eDef flow.EventDefinition,
) error {
	if !t.inState(TrackWaitForEvent) {
		return errs.New(
			errs.M("track #%s of instance #%s doesn't expect any event",
				t.Id(), t.instance.Id()),
			errs.C(errorClass, errs.InvalidState),
			errs.D("event_trigger", string(eDef.Type())),
			errs.D("event_definition_id", eDef.Id()))
	}

	if ctx == nil {
		ctx = t.ctx
	}

	n := t.steps[len(t.steps)-1].node

	ep, ok := n.(eventproc.EventProcessor)
	if !ok {
		return errs.New(
			errs.M("node %q(%s) doesn't support event processing",
				n.Name(), n.Id()),
			errs.C(errorClass, errs.TypeCastingError))
	}

	if err := ep.ProcessEvent(ctx, eDef); err != nil {
		return err
	}

	if err := t.unregisterEvent(n); err != nil {
		return errs.New(
			errs.M("node %q[%s] unregister events failed", n.Name(), n.Id()),
			errs.E(err))
	}

	t.updateState(TrackReady)

	return nil
}

// -----------------------------------------------------------------------------
