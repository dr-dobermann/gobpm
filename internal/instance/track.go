package instance

import (
	"context"
	"errors"
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
	StepAwaitsResults
	StepEnded
	StepFailed
)

func (ss stepState) String() string {
	return []string{
		"Created",
		"Started",
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

	state   trackState
	prev    []*track
	steps   []*stepInfo
	lastErr error

	instance *Instance
}

// newTrack creates the new track from the start flow.Node and sets it
// in TrackReady state.
// newTrack retruns created track's pointer on success or error on failure.
func newTrack(
	start flow.Node,
	inst *Instance,
	tk *token,
) (*track, error) {
	_, ok := start.(exec.NodeExecutor)
	if !ok {
		return nil, errs.New(
			errs.M("%q node hasn't NodeExecutor interface", start.Name()),
			errs.C(errorClass, errs.TypeCastingError))
	}

	t := track{
		ID:   *foundation.NewID(),
		prev: []*track{},
		steps: []*stepInfo{
			{
				node:  start,
				state: StepCreated,
				tk:    tk,
			}},
		instance: inst,
		lastErr:  nil,
	}

	t.updateState(TrackReady)

	// check if Node is event and it awaits for events
	if e, ok := start.(flow.EventNode); ok {
		for _, d := range e.Definitions() {
			if err := t.instance.RegisterEvents(&t, d); err != nil {
				return nil, err
			}
		}

		if len(e.Definitions()) != 0 {
			t.updateState(TrackWaitForEvent)
		}

	}

	return &t, nil
}

// --------------------- exec.EventProcessor interface -------------------------

func (t *track) ProcessEvent(
	eDef flow.EventDefinition,
) error {
	if !t.inState(TrackWaitForEvent) {
		return errs.New(
			errs.M("track doesn't expect any event"),
			errs.C(errorClass, errs.InvalidState),
			errs.D("event_trigger", string(eDef.Type())),
			errs.D("eDef_id", eDef.Id()))
	}

	n := t.steps[len(t.steps)-1].node

	ep, ok := n.(exec.EventProcessor)
	if !ok {
		return errs.New(
			errs.M("node %q(%s) doesn't support event processing",
				n.Name(), n.Id()),
			errs.C(errorClass, errs.TypeCastingError))
	}

	if err := ep.ProcessEvent(eDef); err != nil {
		return err
	}

	t.updateState(TrackReady)

	return nil
}

// -----------------------------------------------------------------------------

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
func (t *track) updateState(newState trackState) {
	t.m.Lock()
	defer t.m.Unlock()

	switch {
	case newState == TrackReady && t.state == TrackWaitForEvent:
		t.currentStep().tk.updateState(TokenAlive)

	case newState == TrackWaitForEvent:
		t.currentStep().tk.updateState(TokenWaitForEvent)

	case newState == TrackFailed:
		t.currentStep().tk.updateState(TokenDead)
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
//
// eDef keeps an event definition which caused the instance start.
func (t *track) run(
	ctx context.Context,
) {
	if !t.inState(TrackReady, TrackWaitForEvent) {
		return
	}

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

			step.tk.updateState(TokenDead)

			return
		}

		nextFlows, err := t.executeNode(step.node)
		if err != nil {
			t.lastErr = err
			t.updateState(TrackFailed)

			return
		}

		step.state = StepEnded

		if len(nextFlows) == 0 {
			t.updateState(TrackEnded)

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

// executeNode tries to execute flow.Node n.
// On succes it returns a list (probably empty) of outgoing sequence flows.
// On failure it returns error.
func (t *track) executeNode(n flow.Node) ([]*flow.SequenceFlow, error) {

	return nil, errors.New("not implemented")
}

func (t *track) checkFlows(flows []*flow.SequenceFlow) error {
	return errors.New("not implemented")
}
