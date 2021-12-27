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
	"sync"

	"github.com/dr-dobermann/gobpm/model"
	"github.com/dr-dobermann/srvbus/msgsrv"
	"github.com/dr-dobermann/srvbus/s2"
)

type Thresher struct {
	id        model.Id
	instances []*ProcessInstance
	ctx       context.Context
	cancel    context.CancelFunc
	m         *sync.Mutex

	// external service and message servers
	SSrv *s2.ServiceServer
	MSrv *msgsrv.MessageServer
}

func (thr *Thresher) ChangeContext(ctx context.Context, cFunc context.CancelFunc) {
	thr.ctx = ctx
	thr.cancel = cFunc
}

var thresher *Thresher

// GetThresher creates a new Thresher and returns its pointer.
//
// Threser has its own s2.ServiceServer and msgsrv.MessageServer.
func GetThreshser() *Thresher {
	if thresher == nil {
		ctx, cancel := context.WithCancel(context.Background())
		id := model.NewID()
		thresher = &Thresher{
			id:        id,
			instances: []*ProcessInstance{},
			ctx:       ctx,
			cancel:    cancel,
			m:         new(sync.Mutex),
			SSrv:      s2.NewServiceServer(ctx, id.String()+" : SvcSrv"),
			MSrv:      msgsrv.NewMessageServer(id.String() + " : MsgSrv")}
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

func (thr *Thresher) TurnOn() {
	thr.m.Lock()
	defer thr.m.Unlock()

	for _, pi := range thr.instances {
		if pi.state == IsCreated {
			go pi.Run(thr.ctx)
		}
	}
}

func (thr *Thresher) TurnOff() {
	thr.cancel()
}

// ----------------------------------------------------------------------------
