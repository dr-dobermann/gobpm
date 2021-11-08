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
	TsAwaitsService
	TsAwaitsMessage
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

// newTrack creates a new track from single Node
func newTrack(n model.Node, inst *ProcessInstance) *track {

	t := &track{
		id:       model.NewID(),
		instance: inst,
		node:     n}

	return t
}

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

// ProcessInstance represents a single run-time process instance
type ProcessInstance struct {
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
