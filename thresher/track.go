package thresher

import (
	"context"
	"fmt"
	"sync"

	"github.com/dr-dobermann/gobpm/model"
	"go.uber.org/zap"
)

// TrackState represent the state of the whole track
type TrackState uint8

const (
	TsReady TrackState = iota

	// Intermediate
	TsExecutingStep
	TsAwaitsStepResults

	// Final statuses
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
	sync.Mutex

	id       model.Id
	instance *Instance
	state    TrackState
	prev     []*track
	steps    []stepInfo
	lastErr  error

	log *zap.SugaredLogger
}

func (tr *track) currentStep() *stepInfo {
	return &tr.steps[len(tr.steps)-1]
}

func (tr *track) Instance() *Instance {
	return tr.instance
}

func (tr *track) checkStates(ts TrackState) bool {
	tr.Lock()
	defer tr.Unlock()

	return tr.state == ts
}

// newTrack creates a new track started from a Node n.
func newTrack(n model.Node, inst *Instance, prevTrack *track) (*track, error) {
	if n == nil {
		return nil,
			NewPEErr(nil, nil, "couldn't start track from nil Node")
	}

	if inst == nil {
		return nil,
			NewPEErr(nil,
				nil, "couldn't create a track for a nil instance")
	}

	trID := model.NewID()
	t := &track{
		id:       trID,
		instance: inst,
		steps:    []stepInfo{{node: n}},
		log:      inst.log.Named("TR:" + trID.GetLast(4))}

	if prevTrack != nil {
		t.prev = append(t.prev, prevTrack)
	}

	return t, nil
}

func (tr *track) run(ctx context.Context) {
	// check track status
	if !tr.checkStates(TsReady) {
		return
	}

	// while there is a step to take
	// take a step
	// execute it
	// check if there is other steps to take
	// if there is fork needed. create new track(s) and
	// add it(them) to the instance
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

		return NewPEErr(tr,
			nil, fmt.Sprintf("couldn't start node %s in state %v",
				tr.currentStep().node.Name(),
				tr.currentStep().state),
			nil)
	}

	n := tr.currentStep().node
	switch n.Type() {
	case model.EtActivity:
		t, ok := n.(model.TaskModel)
		if !ok {
			tr.state = TsError

			return NewPEErr(tr,
				nil, "couldn't convert node "+n.Name()+" to TaskModel",
				nil)
		}

		te, err := GetTaskExecutor(t)
		if err != nil {
			tr.state = TsError

			return NewPEErr(tr,
				nil, "couldn't get the TaskExecutor", err)
		}

		tr.state = TsExecutingStep
		tr.currentStep().state = SsStarted
		ns, next, err := te.Exec(ctx, tr)
		if err != nil {
			tr.state = TsError

			return NewPEErr(tr, err, "nil, error executing task "+t.Name())
		}

		if err = tr.updateState(ns, next); err != nil {
			tr.state = TsError

			return NewPEErr(
				nil, tr,
				"couldn't update track state to "+ns.String(),
				err)
		}

	case model.EtGateway:

	case model.EtEvent:

	default:
		tr.state = TsError

		return NewPEErr(tr,
			nil, fmt.Sprintf(
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
		return NewPEErr(
			tnil, r, "couldn't update state on finalized track", nil)
	}

	if ns <= tr.currentStep().state {
		tr.state = TsError
		return NewPEErr(
			tnil, r,
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
			t, ok := tr.currentStep().node.(model.TaskModel)
			if !ok {
				tr.state = TsError
				return NewPEErr(
					nil, tr,
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
				return NewPEErr(
					nil, tr,
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
		return NewPEErr(tr, nil, "nil, invalid step state "+ns.String())
	}

	return nil
}
