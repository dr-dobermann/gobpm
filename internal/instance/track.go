package instance

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/model"
	"github.com/dr-dobermann/gobpm/pkg/executor"
	"go.uber.org/zap"
)

const (
	TrackNewEvt = "NEW_TRACK_EVT"

	newtrackMsg = "new track created"
	starttrack  = "track started"
	endtrack    = "track finished"
)

// trackState represent the state of the whole track
type trackState uint8

const (
	TsReady trackState = iota

	// Intermediate
	TsExecutingStep
	TsProcessStepResults

	// Final statuses
	TsMerged
	TsEnded
	TsError
)

func (ts trackState) String() string {
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
	state    trackState
	prev     []*track
	steps    []*stepInfo
	lastErr  error

	log *zap.SugaredLogger
}

func (tr *track) currentStep() *stepInfo {
	return tr.steps[len(tr.steps)-1]
}

// newtrack creates a new track started from a Node n.
func newTrack(n model.Node, inst *Instance, prevtrack *track) (*track, error) {
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

	if prevtrack != nil {
		t.prev = append(t.prev, prevtrack)
	}

	t.log.Info(newtrackMsg)

	inst.Emitter.EmitEvent(TrackNewEvt,
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
		ne, err := executor.GetNodeExecutor(step.node)
		if err != nil {
			step.state = SsFailed
			tr.state = TsError
			tr.lastErr = err

			tr.log.Debugw("track ends with errors",
				zap.Error(NewPEErr(tr, err, "couldn't get node executor")))

			return
		}

		// check node Prologue
		if err := tr.checkPrologue(ctx, ne); err != nil {
			step.state = SsFailed
			tr.state = TsError
			tr.lastErr = err

			tr.log.Debugw("step prologue failed",
				zap.String("node_name", step.node.Name()),
				zap.Error(err))

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

func (tr *track) checkPrologue(
	ctx context.Context,
	ne executor.NodeExecutor) error {

	np, ok := ne.(executor.NodePrologue)
	// if node doesn't implement NodePrologue, then prologue check is ok
	if !ok {
		return nil
	}

	return np.Prologue(ctx, tr)
}
