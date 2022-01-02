package thresher

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/model"
	"go.uber.org/zap"
)

const (
	trackNewEvt   = "NEW_TRACK_EVT"
	trackStartEvt = "TRACK_START_EVT"
	trackEndEvt   = "TRACK_END_EVT"

	newTrackMsg = "new track created"
	startTrack  = "track started"
	endTrack    = "track finished"
)

// TrackState represent the state of the whole track
type TrackState uint8

const (
	TsReady TrackState = iota

	// Intermediate
	TsExecutingStep
	TsProcessStepResults

	// Final statuses
	TsMerged
	TsEnded
	TsError
)

func (ts TrackState) String() string {
	return []string{
		"Created",
		"Executing Step",
		"Process Step Results",
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
	instance *Instance
	state    TrackState
	prev     []*track
	steps    []*stepInfo
	lastErr  error

	log *zap.SugaredLogger
}

func (tr *track) currentStep() *stepInfo {
	return tr.steps[len(tr.steps)-1]
}

func (tr *track) Instance() *Instance {
	return tr.instance
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
		steps:    []*stepInfo{{node: n}},
		log:      inst.log.Named("TR:" + trID.GetLast(4))}

	if prevTrack != nil {
		t.prev = append(t.prev, prevTrack)
	}

	t.log.Info(newTrackMsg)

	inst.Thr.EmitEvent(trackNewEvt,
		fmt.Sprintf(
			"{instance_id: \"%v\", track_id: \"%v\", "+
				"node_name: \"%s\", node_type: \"%s\"}",
			t.id, inst.id, n.Name(), n.Type().String()))

	return t, nil
}

// runs single track from its first step
//
// after executing step there could appeared new steps
// sequenced to the current one.
// if there are more than one outcoming flows from the step,
// the new track(s) will be created and registered on the instance.
//
// if there is no more steps on the track or the exeution of the current
// one ends with an error, the track running ends.
func (tr *track) run(ctx context.Context) {
	if tr.state != TsReady {
		return
	}

	for {
		// check context cancellation
		select {
		case <-ctx.Done():
			tr.state = TsError
			tr.lastErr = ctx.Err()
			return

		default:
		}

		// while there is a step to take
		step := tr.currentStep()
		if step.state != SsCreated {
			// if the last step is finished
			// stop track running and return
			tr.log.Debug("track execution finished")

			tr.state = TsEnded

			return
		}

		// take a step
		ne, err := GetNodeExecutor(step.node)
		if err != nil {
			step.state = SsFailed
			tr.state = TsError
			tr.lastErr = err

			tr.log.Debugw("track ends with errors",
				zap.Error(NewPEErr(tr, err, "couldn't get node executor")))

			return
		}

		// execute it
		step.state = SsStarted
		tr.state = TsExecutingStep
		tr.log.Debugw("executing step",
			zap.Stringer("type", step.node.Type()),
			zap.String("name", step.node.Name()))

		nexts, err := ne.Exec(ctx, tr)
		if err != nil {
			step.state = SsFailed
			tr.state = TsError
			tr.lastErr = err

			tr.log.Debugw("track ends with errors",
				zap.Error(err))

			return
		}

		step.state = SsEnded
		tr.log.Debugw("step executed",
			zap.String("node_name", step.node.Name()),
			zap.Int("out_flows_num", len(nexts)))

		for i, sf := range nexts {
			// check if there is other steps to take
			if i == 0 {
				tr.steps = append(tr.steps, &stepInfo{
					node:  sf.GetTarget(),
					state: SsCreated,
				})

				tr.log.Debugw("new step added",
					zap.String("node_name", sf.GetTarget().Name()),
					zap.Stringer("node_id", sf.GetTarget().ID()))

				continue
			}

			// if there is fork appears (nexts > 1). create new track(s) and
			ntr, err := newTrack(sf.GetTarget(), tr.instance, tr)
			if err != nil {
				tr.state = TsError
				tr.lastErr = fmt.Errorf("couldn't create a fork "+
					"for %s on flow %v: %v",
					sf.GetTarget().Name(), sf.ID(), err)
				return
			}

			// add it(them) to the instance
			if err := tr.instance.addTrack(ntr); err != nil {
				tr.state = TsError
				tr.lastErr = fmt.Errorf("couldn't add forked track to "+
					"instance for %s on flow %v: %v",
					sf.GetTarget().Name(), sf.ID(), err)
				return
			}

			tr.log.Debugw("new forked track added",
				zap.Stringer("forked_track_id", ntr.id),
				zap.String("forked_node_name", ntr.currentStep().node.Name()))
		}
	}
}
