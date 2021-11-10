// GoBPM is BPMN v.2 compliant business process engine
//
// (c) 2021, Ruslan Gabitov a.k.a. dr-dobermann.
// Use of this source is governed by LGPL license that
// can be found in the LICENSE file.

/*
Package thresher as a part of gobpm package provides a runtime
environment to run, monitor and control business processes
loaded or created in model package.
*/
package thresher

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/ctr"
	"github.com/dr-dobermann/gobpm/model"
)

// TaskExecutor defines the run-time functionatlity of the Task objects
type TaskExecutor interface {
	model.TaskDefinition
	Exec(ctx context.Context, tr *track) (StepState, []*model.SequenceFlow, error)
}

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

// track keeps information about one single business process execution path.
// track consists information about currently executed Node and the history
// of past executed Nodes.
// Every task with no incoming flow or intermediate event starts a new track.
// if track splits, the new track will be started
type track struct {
	id       model.Id
	instance *ProcessInstance
	state    TrackState
	prev     []*track
	steps    []stepInfo
}

func (tr *track) currentStep() *stepInfo {
	return &tr.steps[len(tr.steps)-1]
}

func (tr *track) Instance() *ProcessInstance {
	return tr.instance
}

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

// newTrack creates a new track started from a Node n.
func newTrack(n model.Node, inst *ProcessInstance) (*track, error) {
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
			return NewProcExecError(tr,
				"couldn't convert node "+n.Name()+" to TaskDefinition",
				nil)
		}

		te, err := GetTaskExecutor(t)
		if err != nil {
			return NewProcExecError(tr,
				"couldn't get the TaskExecutor", err)
		}

		// TODO: check incoming variables demands for the Task

		tr.state = TsExecutingStep
		tr.currentStep().state = SsStarted
		ns, next, err := te.Exec(ctx, tr)
		if err != nil {
			tr.state = TsError
			return NewProcExecError(tr, "error executing task "+t.Name(), err)
		}

		// TODO: check resulting variable demands of the Task

		if err = tr.updateState(ns, next); err != nil {
			return NewProcExecError(tr, "couldn't update track state to "+ns.String(), err)
		}

	case model.EtGateway:

	case model.EtEvent:

	default:
		return NewProcExecError(tr,
			fmt.Sprintf("invalid node type %v of %s. Should be Activity, Gateway or Event",
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

	return nil
}

type InstanceState uint8

const (
	IsCreated InstanceState = iota
	IsPrepared
	IsRunning
	IsEnded
)

// ProcessInstance represents a single run-time process instance
type ProcessInstance struct {
	id    model.Id
	state InstanceState
	// the copy of the process model the instance is based on
	snapshot *model.Process
	vs       model.VarStore
	tracks   []*track

	monitor *ctr.Monitor
	audit   *ctr.Audit
}

// prepare prepares the ProcessInstance object for start.
// prepare looks for nodes that don't have incoming flows and
// creates a list of tracks to work with
func (pi *ProcessInstance) prepare() error {
	if pi.state != IsCreated {
		return NewProcExecError(nil,
			fmt.Sprintf("couldn't prepare Instance in %d state", pi.state), nil)
	}

	// through all nodes except the gateways find ones
	// don't have an incoming flow
	// and create tracks with them
	for _, n := range pi.snapshot.GetNodes(model.EtUnspecified) {
		if n.Type() != model.EtGateway && !n.HasIncoming() {
			t, err := newTrack(n, pi)
			if err != nil {
				return NewProcExecError(nil, "couldn't prepare an Instance for starting", err)
			}

			pi.tracks = append(pi.tracks, t)
		}
	}

	pi.state = IsPrepared

	return nil
}

func (pi *ProcessInstance) Start(ctx context.Context, trh *Thresher) {

}

type Thresher struct {
	id        model.Id
	instances []*ProcessInstance
}

var thresher *Thresher

func GetThreshser() *Thresher {
	if thresher == nil {
		thresher = &Thresher{
			id:        model.NewID(),
			instances: []*ProcessInstance{}}
	}

	return thresher
}

func (thr *Thresher) NewProcessInstance(p *model.Process) (*ProcessInstance, error) {
	sn := p.Copy()
	if sn == nil {
		return nil,
			NewProcExecError(nil,
				fmt.Sprintf("couldn't create a copy form process %s[%s]",
					p.Name(), p.ID().String()),
				nil)
	}

	pi := &ProcessInstance{
		id:       model.NewID(),
		snapshot: sn,
		vs:       make(model.VarStore),
		tracks:   []*track{}}
	thr.instances = append(thr.instances, pi)

	return pi, nil
}

func (trh *Thresher) Run(ctx context.Context) {

}
