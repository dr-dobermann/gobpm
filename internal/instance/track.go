package instance

import (
	"context"

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

type stepInfo struct {
	node  flow.Node
	state stepState
	tk    *token
}

type track struct {
	foundation.ID

	state   trackState
	prev    []*track
	steps   []*stepInfo
	lastErr error

	inst *Instance
}

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
		ID:    *foundation.NewID(),
		state: TrackReady,
		prev:  []*track{},
		steps: []*stepInfo{
			{
				node:  start,
				state: StepCreated,
				tk:    tk,
			}},
		inst:    inst,
		lastErr: nil,
	}

	return &t, nil
}

// --------------------- exec.EventProcessor interface -------------------------

func (t *track) ProcessEvent(
	ctx context.Context,
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

	return ep.ProcessEvent(ctx, eDef)
}

// -----------------------------------------------------------------------------

// inState checks if track state is equal to any track state from the ss.
func (t *track) inState(ss ...trackState) bool {
	for _, s := range ss {
		if t.state == s {
			return true
		}
	}

	return false
}

// updateState sets new state for the track.
func (t *track) updateState(newState trackState) {
	t.state = newState
}

// run start execution loop of the track which ends by ctx's cancel or
// when there is no outgoing flows from the processing nodes.
//
// eDef keeps an event definition which caused the instance start.
func (t *track) run(
	ctx context.Context,
	eDef flow.EventDefinition,
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
		}

	}
}
