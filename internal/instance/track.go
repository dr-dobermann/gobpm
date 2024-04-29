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
//     give to the other child tracks.
//
// if there is no outgouing flows, then track ends and token died.

package instance

import (
	"context"
	"sync"

	"github.com/dr-dobermann/gobpm/internal/exec"
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

	m sync.Mutex

	ctx context.Context

	state   trackState
	prev    []*track
	steps   []*stepInfo
	lastErr error

	instance *Instance
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
	tk *token,
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
				errs.M("empty instance"),
				errs.C(errorClass, errs.EmptyNotAllowed))
	}

	t := track{
		ID:   *foundation.NewID(),
		prev: []*track{},
		steps: []*stepInfo{
			{
				node:  start,
				state: StepCreated,
				tk:    tk,
			},
		},
		instance: inst,
		lastErr:  nil,
	}

	if prevTrack != nil {
		t.prev = append(t.prev, append(prevTrack.prev, prevTrack)...)
	}

	t.updateState(TrackReady)

	// check if Node is event and it awaits for events
	if e, ok := start.(flow.EventNode); ok {
		for _, d := range e.Definitions() {
			if err := t.instance.RegisterEvents(&t, d); err != nil {
				return nil,
					errs.New(
						errs.M("couldn't register event definitions for event %s[%s]",
							start.Name(), start.Id()),
						errs.C(errorClass, errs.BulidingFailed))
			}
		}

		if len(e.Definitions()) != 0 {
			t.updateState(TrackWaitForEvent)
		}
	}

	return &t, nil
}

// inState checks if track state is equal to any track state from the ss.
func (t *track) inState(ss ...trackState) bool {
	t.m.Lock()
	defer t.m.Unlock()

	for _, s := range ss {
		if t.state == s {
			return true
		}
	}

	return false
}

// updateState sets new state for the track.
// If track has a token, its state will be updated accordingly.
func (t *track) updateState(newState trackState) {
	t.m.Lock()
	defer t.m.Unlock()

	ts := TokenInvalid

	switch {
	case newState == TrackReady ||
		newState == TrackExecutingStep:
		ts = TokenAlive

	case newState == TrackWaitForEvent:
		ts = TokenWaitForEvent

	case newState == TrackFailed ||
		newState == TrackEnded ||
		newState == TrackCanceled:
		ts = TokenConsumed
	}

	if t.currentStep().tk != nil {
		t.currentStep().tk.updateState(ts)
	}

	t.state = newState
}

// currentStep returns current step of the track.
func (t *track) currentStep() *stepInfo {
	t.m.Lock()
	defer t.m.Unlock()

	return t.steps[len(t.steps)-1]
}

// run start execution loop of the track which ends by ctx's cancel or
// when there is no outgoing flows from the processing nodes.
func (t *track) run(
	ctx context.Context,
) {
	if !t.inState(TrackReady, TrackWaitForEvent) {
		return
	}

	if ctx == nil {
		errs.Panic("empty context for track #" + t.Id() +
			" of Instance #" + t.instance.Id())
		return
	}
	t.ctx = ctx

	for {
		select {
		case <-ctx.Done():
			t.updateState(TrackCanceled)
			t.lastErr = ctx.Err()

			return

		default:
			if t.inState(TrackWaitForEvent) {
				continue
			}
		}

		// while there is a step to take
		step := t.currentStep()
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

	if step.tk == nil {
		var err error

		step.tk, err = t.instance.createToken()
		if err != nil {
			return nil,
				errs.New(
					errs.M("couldn't get token for Node %s[%s]",
						step.node.Id(), step.node.Name()),
					errs.C(errorClass, errs.BulidingFailed),
					errs.E(err))
		}
	}

	t.updateState(TrackExecutingStep)

	step.state = StepStarted

	ndl, ok := step.node.(exec.NodeDataLoader)
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

	if err := t.runNodeEpilogue(ctx, step.node); err != nil {
		return nil, err
	}

	step.state = StepEnded

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
	nextNode := -1
	for i, f := range flows {
		if f.Target().Id() == t.currentStep().node.Id() {
			nextNode = i
			break
		}
	}

	if nextNode == -1 {
		nextNode = 0
	}

	tokens := t.currentStep().tk.split(len(flows))

	t.steps = append(t.steps, &stepInfo{
		node:  flows[nextNode].Target().Node(),
		state: StepCreated,
		tk:    tokens[0],
	})

	for i, f := range append(flows[:nextNode], flows[nextNode+1:]...) {
		nt, err := newTrack(f.Target().Node(), t.instance, t, tokens[i+1])
		if err != nil {
			return errs.New(
				errs.M("couldn't creaete a new track for flow %q", f.Id()),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(err))
		}

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
		return nil
	}

	t.currentStep().state = StepPrologued

	if err := np.Prologue(ctx, t.instance); err != nil {
		return err
	}

	return nil
}

// runNodeEpilogue runs node Epilogue if node supports it.
func (t *track) runNodeEpilogue(ctx context.Context, n flow.Node) error {
	ne, ok := n.(exec.NodeEpliogue)
	if !ok {
		return nil
	}

	t.currentStep().state = StepEpilogued

	if err := ne.Epilogue(ctx, t.instance); err != nil {
		return err
	}

	return nil
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

	ep, ok := n.(exec.EventProcessor)
	if !ok {
		return errs.New(
			errs.M("node %q(%s) doesn't support event processing",
				n.Name(), n.Id()),
			errs.C(errorClass, errs.TypeCastingError))
	}

	if err := ep.ProcessEvent(ctx, eDef); err != nil {
		return err
	}

	t.updateState(TrackReady)

	return nil
}

// -----------------------------------------------------------------------------
