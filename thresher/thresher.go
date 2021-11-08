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
	"github.com/dr-dobermann/gobpm/ctr"
	"github.com/dr-dobermann/gobpm/model"
)

type TrackState uint8

const (
	TsCreated TrackState = iota
	TsStarted
	TsAwaitsService
	TsAwaitsMessage
	TsEnded
)

// track keeps information about one single business process execution path.
// track consists information about currently executed Node and the history
// of past executed Nodes.
// Every task with no incoming flow or intermediate event starts a new track.
// if track splits, the new track will be started
type track struct {
	instance *ProcessInstance
	state    TrackState
	prev     *track
	pos      model.Node
}

// ProcessInstance represents a single run-time process instance
type ProcessInstance struct {
	// the copy of the process model the instance is based on
	snapshot *model.Process
	vs       model.VarStore
	tracks   []track

	monitor *ctr.Monitor
	audit   *ctr.Audit
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
