package instance

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/internal/taskexecs"
	"github.com/dr-dobermann/gobpm/pkg/identity"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/dr-dobermann/gobpm/pkg/thresher/executor"
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

type stepState uint8

const (
	SsCreated stepState = iota
	SsStarted
	SsAwaitsResults
	SsEnded
	SsFailed
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
	node  common.Node
	state stepState
	tk    *Token
}

// track consists information about currently executed Node and the history
// of past executed Nodes.
// Every task with no incoming flow or intermediate event starts a new track.
// if track splits, the new track(s) will be started
type track struct {
	id       identity.Id
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
func newTrack(
	n common.Node,
	inst *Instance,
	prevTrack *track,
	tk *Token) (*track, error) {

	if n == nil {
		return nil,
			NewPEErr(nil, nil, "couldn't start track from nil Node")
	}

	if inst == nil {
		return nil,
			NewPEErr(nil,
				nil, "couldn't create a track for a nil instance")
	}

	if tk == nil {
		tk = newToken(identity.EmptyID(), inst)
	}

	trID := identity.NewID()
	t := &track{
		id:       trID,
		instance: inst,
		steps: []*stepInfo{
			{
				node: n,
				tk:   tk}},
		log: inst.log.Named("TR:" + trID.GetLast(4))}

	if prevTrack != nil {
		t.prev = append(t.prev, prevTrack)
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

	var err error
	tr.log.Debug("track execution started")
	defer tr.log.Debugw("track execution finished",
		zap.Error(err))

	for {
		// check context cancellation
		select {
		case <-ctx.Done():
			err = ctx.Err()
			tr.state = TsError
			tr.lastErr = err
			return

		default:
		}

		// while there is a step to take
		step := tr.currentStep()
		if step.state != SsCreated {
			// if the last step is finished
			// stop track running, inactivate token and return
			tr.state = TsEnded

			step.tk.updateState(Inactive)

			return
		}

		// take a step
		ne, err := GetNodeExecutor(step.node)
		if err != nil {
			err = NewPEErr(tr, err, "couldn't get node executor for node '%s'",
				step.node.Name())
			tr.updateState(TsError, SsFailed, err)

			return
		}

		tr.updateState(TsExecutingStep, SsStarted, nil)

		tr.log.Debugw("node execution started",
			zap.Stringer("type", step.node.Type()),
			zap.String("name", step.node.Name()))

		nextFlows, nextTokens, err := tr.execNode(ctx, ne)
		if err != nil {
			err = NewPEErr(tr, err, "node '%s' execution failed",
				step.node.Name())
			tr.updateState(TsError, SsFailed, err)

			return
		}

		step.state = SsEnded
		tr.log.Debugw("node execution successful",
			zap.String("node_name", step.node.Name()),
			zap.Int("out_flows_num", len(nextFlows)))

		err = tr.updateFlows(nextFlows, nextTokens)
		if err != nil {
			tr.state = TsError
			tr.lastErr = err

			return
		}
	}
}

// after successful execution of the node in step,
// updateFlows creates a next step on the track and forked track if
// there are more than one output flows
func (tr *track) updateFlows(
	nextFlows []*common.SequenceFlow,
	nextTokens []*Token) error {

	for i, sf := range nextFlows {
		if i == 0 {
			// check if there is other steps to take
			// add it to the same track as next one
			tr.steps = append(tr.steps, &stepInfo{
				node:  sf.GetTarget(),
				state: SsCreated,
				tk:    nextTokens[i]})

			tr.log.Debugw("new step added",
				zap.String("node_name", sf.GetTarget().Name()),
				zap.Stringer("node_id", sf.GetTarget().ID()))

			continue
		}

		// if there is fork appears. create new track(s) and
		// add it(them) to the instance
		ntr, err := newTrack(sf.GetTarget(), tr.instance, tr, nextTokens[i])
		if err != nil {
			return NewPEErr(tr, err, "couldn't create a fork "+
				"for '%s' on flow %v",
				sf.GetTarget().Name(), sf.ID())
		}

		if err := tr.instance.addTrack(ntr); err != nil {
			return fmt.Errorf("couldn't add forked track to "+
				"instance for %s on flow %v: %v",
				sf.GetTarget().Name(), sf.ID(), err)
		}

		tr.log.Debugw("new forked track added",
			zap.Stringer("forked_track_id", ntr.id),
			zap.String("forked_node_name", ntr.currentStep().node.Name()))
	}

	return nil
}

// updates track's and current step's state
func (tr *track) updateState(ts trackState, ss stepState, err error) {
	tr.currentStep().state = ss

	tr.state = ts

	if err != nil {
		tr.lastErr = err
	}
}

// executing one node with prologue and epilogue if node implements such
// interfaces.
//
// execNodes also controls tokens passing to the node and getting it(them)
// back from the node
func (tr *track) execNode(
	ctx context.Context,
	ne executor.NodeExecutor) ([]*common.SequenceFlow, []*Token, error) {

	step := tr.currentStep()

	// pass token to the node if it accept them
	th, hasTokenHdlr := step.node.(TokenHandler)
	if hasTokenHdlr {
		err := th.TakeToken(step.tk)
		if err != nil {
			return nil, nil, NewPEErr(tr, err, "node '%s' didn't take a token",
				step.node.Name())
		}
	}

	// dl, ok := ne.(executor.DataLinker)
	// if ok {
	// 	if err := dl.CheckInData(); err != nil {
	// 		return nil, nil, NewPEErr(tr, err, "node linked data check error")
	// 	}
	// }

	// check node Prologue
	if err := tr.runNodePrologue(ctx, ne); err != nil {
		return nil, nil, NewPEErr(tr, err, "node prologue failed")
	}

	// execute it
	nexts, err := ne.Exec(ctx, tr)
	if err != nil {
		return nil, nil, NewPEErr(tr, err, "node execution failed")
	}

	err = tr.runNodeEpilogue(ctx, ne)
	if err != nil {
		return nil, nil, NewPEErr(tr, err, "node epilogue failed")
	}

	// if dl != nil {
	// 	if err := dl.CheckOutData(); err != nil {
	// 		return nil, nil,
	// 			NewPEErr(tr, err, "couldn't update data linked to node")
	// 	}
	// }

	// update outgoing tokens
	var nextTokens []*Token

	if hasTokenHdlr {
		nextTokens, err = th.ReturnTokens()
		if err != nil {
			return nil, nil, NewPEErr(tr, err,
				"couldn't get tokens from node '%s'",
				step.node.Name())
		}

		if len(nextTokens) != len(nexts) {
			return nil, nil, NewPEErr(tr, err,
				"number of flows(%d) isn't equal to number of tokens(%d)",
				len(nexts), len(nextTokens))
		}
	} else {
		nextTokens = step.tk.split(len(nexts), Alive)
	}

	return nexts, nextTokens, nil
}

// checks if node implements NodePrologue interface and runs node prologue.
func (tr *track) runNodePrologue(
	ctx context.Context,
	ne executor.NodeExecutor) error {

	np, ok := ne.(executor.NodePrologue)
	// if node doesn't implement NodePrologue, then prologue check is ok
	if !ok {
		return nil
	}
	tr.log.Debug("prologue started...",
		zap.String("node_name", tr.currentStep().node.Name()))

	return np.Prologue(ctx, tr)
}

// checks if node implements NodeEpilogue interface and runs node epilogue
func (tr *track) runNodeEpilogue(
	ctx context.Context,
	ne executor.NodeExecutor) error {

	nEp, ok := ne.(executor.NodeEpliogue)

	if ok {
		tr.log.Debug("epilogue started...",
			zap.String("node_name", tr.currentStep().node.Name()))

		return nEp.Epilogue(ctx, tr)
	}

	return nil
}

func GetNodeExecutor(n common.Node) (executor.NodeExecutor, error) {
	switch cn := n.(type) {
	case activities.TaskModel:
		return taskexecs.GetTaskExecutor(cn)

	// case model.GatewayModel:
	// case model.EtEvent:

	default:
		return nil, fmt.Errorf("invalid node type: %T", cn)
	}
}
