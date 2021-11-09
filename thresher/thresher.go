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

type TrackState uint8

const (
	TsCreated TrackState = iota
	TsStarted
	// Intermediate
	TsAwaitsService
	TsAwaitsMessage
	// Final status
	TsMerged
	TsEnded
	TsError
)

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
	node     model.Node
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
func newTrack(n model.Node, inst *ProcessInstance) *track {
	if n == nil {
		panic("couldn't start track from nil Node")
	}

	t := &track{
		id:       model.NewID(),
		instance: inst,
		node:     n}

	return t
}

// tick make a single step on track if current node isn't started
// yet.
// Executed node returns its new status. if status is from Final ones
// the Node returns a list of valid outcomes flows.
func (tr *track) tick(ctx context.Context) error {

	if tr.state != TsCreated {
		return nil
	}

	switch tr.node.Type() {
	case model.EtActivity:
		t, ok := tr.node.(Task)
		if !ok {
			panic("couldn't convert node " + tr.node.Name() + " to Task")
		}

		next, err := t.Exec(ctx, tr)
		if err != nil {
			panic(err.Error())
		}

		fmt.Println(next)

	case model.EtGateway:

	case model.EtEvent:

	default:
		panic(fmt.Sprintf("invalid node type %v. Should be Activity, Gateway or Event", tr.node.Type().String()))
	}

	return nil
}

// updateState updates the current track state onto ns.
// if it's a Final staus and ff isn't empty the next Node selected and
// new tracks might be created.
// if it's a Finel status and there are no more flows, the track is ended.
func (tr *track) updateState(ns TrackState, ff []*model.SequenceFlow) error {
	if tr.state == TsEnded || tr.state == TsError || tr.state == TsMerged {
		return NewProcExecError(
			tr, "couldn't update state on finalized track", nil)
	}

	switch ns {
	case TsCreated:
		return NewProcExecError(
			tr, "couldn't set a Created status on executing Node", nil)

	case TsStarted:

	// Intermediate statuses
	case TsAwaitsMessage, TsAwaitsService:

	// Final statuses
	case TsEnded:

	case TsError:
	}

	return nil
}

// ProcessInstance represents a single run-time process instance
type ProcessInstance struct {
	id model.Id
	// the copy of the process model the instance is based on
	snapshot *model.Process
	vs       model.VarStore
	tracks   []*track

	monitor *ctr.Monitor
	audit   *ctr.Audit
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

func (trh *Thresher) Run(ctx context.Context) {

}
