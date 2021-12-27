package thresher

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/model"
)

type ProcessExecutingError struct {
	piID    model.Id
	pID     model.Id
	trackId model.Id
	msg     string
	Err     error
}

func (pee ProcessExecutingError) Error() string {
	return fmt.Sprintf("%s[%s]:%s: %s : %v",
		pee.piID, pee.pID, pee.trackId, pee.msg, pee.Err)
}

func NewProcExecError(trk *track, msg string, err error) ProcessExecutingError {
	pee := ProcessExecutingError{msg: msg, Err: err}

	if trk != nil {
		pee.trackId = trk.id
		pee.piID = trk.instance.id
		pee.pID = trk.instance.snapshot.ID()
	}

	return pee
}

// ----------------------------------------------------------------------------

// TaskExecutor defines the run-time functionatlity of the Task objects
type TaskExecutor interface {
	model.TaskDefinition

	RegisterOnTrack(tr *track) error

	Exec(ctx context.Context, tr *track) (StepState, []*model.SequenceFlow, error)
}

// ----------------------------------------------------------------------------

// TrackState represent the state of the whole track
type TrackState uint8

const (
	TsReady TrackState = iota
	TsExecutingStep
	// Intermediate
	TsAwaitsStepResults
	// Final status
	TsMerged
	TsEnded
	TsError
)

func (ts TrackState) String() string {
	return []string{
		"Created",
		"Executing Step",
		"Awaits Step Completion",
		"Merged",
		"Ended",
		"Error",
	}[ts]
}

type StepState uint8

const (
	SsCreated StepState = iota
	SsStarted
	SsAwaitsResults
	SsEnded
	SsFailed
)

func (ss StepState) String() string {
	return []string{
		"Created",
		"Started",
		"AwaitsResults",
		"Ended",
		"Failed",
	}[ss]
}

type stepInfo struct {
	node  model.Node
	state StepState
}

// track consists information about currently executed Node and the history
// of past executed Nodes.
// Every task with no incoming flow or intermediate event starts a new track.
// if track splits, the new track(s) will be started
type track struct {
	id       model.Id
	instance *ProcessInstance
	state    TrackState
	prev     []*track
	steps    []stepInfo
	lastErr  error
}

func (tr *track) currentStep() *stepInfo {
	return &tr.steps[len(tr.steps)-1]
}

func (tr *track) Instance() *ProcessInstance {
	return tr.instance
}

// newTrack creates a new track started from a Node n.
func newTrack(n model.Node, inst *ProcessInstance, prevTrack *track) (*track, error) {
	if n == nil {
		return nil,
			NewProcExecError(nil, "couldn't start track from nil Node", nil)
	}

	if inst == nil {
		return nil,
			NewProcExecError(nil,
				"couldn't create a track for a nil instance",
				nil)
	}

	t := &track{
		id:       model.NewID(),
		instance: inst,
		steps:    []stepInfo{{node: n}}}

	if prevTrack != nil {
		t.prev = append(t.prev, prevTrack)
	}

	return t, nil
}

// tick make a single step on track if current node isn't started
// yet.
// Executed node returns its new status. if status is from Final ones
// the Node returns a list of valid outcomes flows.
func (tr *track) tick(ctx context.Context) error {

	if tr.state != TsReady {
		return nil
	}

	if tr.currentStep().state != SsCreated {
		tr.state = TsError

		return NewProcExecError(tr,
			fmt.Sprintf("couldn't start node %s in state %v",
				tr.currentStep().node.Name(),
				tr.currentStep().state),
			nil)
	}

	n := tr.currentStep().node
	switch n.Type() {
	case model.EtActivity:
		t, ok := n.(model.TaskDefinition)
		if !ok {
			tr.state = TsError

			return NewProcExecError(tr,
				"couldn't convert node "+n.Name()+" to TaskDefinition",
				nil)
		}

		te, err := GetTaskExecutor(t)
		if err != nil {
			tr.state = TsError

			return NewProcExecError(tr,
				"couldn't get the TaskExecutor", err)
		}

		tr.state = TsExecutingStep
		tr.currentStep().state = SsStarted
		ns, next, err := te.Exec(ctx, tr)
		if err != nil {
			tr.state = TsError

			return NewProcExecError(tr, "error executing task "+t.Name(), err)
		}

		if err = tr.updateState(ns, next); err != nil {
			tr.state = TsError

			return NewProcExecError(
				tr,
				"couldn't update track state to "+ns.String(),
				err)
		}

	case model.EtGateway:

	case model.EtEvent:

	default:
		tr.state = TsError

		return NewProcExecError(tr,
			fmt.Sprintf(
				"invalid node type %v of %s. Should be Activity, Gateway or Event",
				n.Type().String(), n.Name()),
			nil)
	}

	return nil
}

// updateState updates the current track state according to Step state.
// if it's a Final staus and ff isn't empty the next Node selected and
// new tracks might be created if there are splitting flows.
// if it's a Finel status and there are no more flows, the track is ended.
func (tr *track) updateState(ns StepState, ff []*model.SequenceFlow) error {
	if tr.state == TsEnded || tr.state == TsError || tr.state == TsMerged {
		return NewProcExecError(
			tr, "couldn't update state on finalized track", nil)
	}

	if ns <= tr.currentStep().state {
		tr.state = TsError
		return NewProcExecError(
			tr,
			fmt.Sprintf("Invalid step state for node %s. current: %s, new: %s",
				tr.currentStep().node.Name(), tr.currentStep().state, ns),
			nil)
	}
	tr.currentStep().state = ns

	switch ns {
	case SsAwaitsResults:
		tr.state = TsAwaitsStepResults

	case SsEnded:
		if len(ff) == 0 {
			tr.state = TsEnded
			return nil
		}

		// continues track on default flow for activity if it's presented
		// or on the first item in the flows list.
		// after taking one flow from the list of outcoming flows,
		// remove it from the list for further processing
		if tr.currentStep().node.Type() == model.EtActivity {
			t, ok := tr.currentStep().node.(model.TaskDefinition)
			if !ok {
				tr.state = TsError
				return NewProcExecError(
					tr,
					fmt.Sprintf("couldn't convert node %s into Task",
						tr.currentStep().node.Name()),
					nil)
			}

			// find default flow in the outcoming flows
			p := -1
			for i, f := range ff {
				if f.ID() == t.DefaultFlowId() {
					p = i
					break
				}
			}

			// if there is no default flow, just take the first one
			if p == -1 {
				p = 0
			}

			tr.steps = append(tr.steps, stepInfo{node: ff[p].GetTarget()})

			// remove processed flow from the list
			ff = append(ff[:p], ff[p+1:]...)

		} else {
			tr.steps = append(tr.steps, stepInfo{node: ff[0].GetTarget()})
			ff = ff[1:]
		}

		for _, f := range ff {
			nt, err := newTrack(f.GetTarget(), tr.instance, tr)
			if err != nil {
				return NewProcExecError(
					tr,
					"couldn't create splitted track from node "+
						f.GetTarget().Name(),
					err)
			}
			tr.instance.tracks = append(tr.instance.tracks, nt)
		}

		tr.state = TsReady

	case SsFailed:
		tr.state = TsError

	default:
		return NewProcExecError(tr, "invalid step state "+ns.String(), nil)
	}

	return nil
}
