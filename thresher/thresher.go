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

	"github.com/dr-dobermann/gobpm/internal/errs"
	"github.com/dr-dobermann/gobpm/model"
	"github.com/dr-dobermann/srvbus"
	"github.com/dr-dobermann/srvbus/es"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

const (
	thrStart = "thresher started"
	thrStop  = "thresher stopped"

	defaultTopic = "/thresher"
	thrNewEvt    = "NEW_THR_EVT"
	thrStartEvt  = "THR_START_EVT"
	thrStopEvt   = "THR_STOP_EVT"

	instNewEvt = "NEW_INSTANCE_EVT"
)

type Thresher struct {
	sync.Mutex

	id        model.Id
	instances []*Instance
	ctx       context.Context

	log *zap.SugaredLogger

	// external service and message servers
	sBus    *srvbus.ServiceBus
	esTopic string

	runned bool
}

func (thr *Thresher) IsRunned() bool {
	thr.Lock()
	defer thr.Unlock()

	return thr.runned
}

func (thr *Thresher) SrvBus() *srvbus.ServiceBus {
	return thr.sBus
}

func (thr *Thresher) EventsTopic() string {
	return thr.esTopic
}

// emits single event into the personal thresher topic
func (thr *Thresher) EmitEvent(name, descr string) {
	if thr.sBus == nil || !thr.sBus.IsRunned() {
		thr.log.Warn("ServiceBus is absent or not runned")
		return
	}

	eSrv, err := thr.sBus.GetEventServer()
	if err != nil {
		thr.log.Warnw("coudn't get an EventServer from the ServiceBus",
			zap.Error(err))
		return
	}

	// initialize default server topic if needed
	if thr.esTopic == "" {
		topic := defaultTopic + "/" + thr.id.String()

		if err := eSrv.AddTopicQueue(topic, "/"); err != nil {
			thr.log.Warnw("couldn't add thresher topic to Event Server",
				zap.String("topic", topic),
				zap.Error(err))

			return
		}

		thr.esTopic = topic
	}

	es.EmitEvt(eSrv, thr.esTopic, name, descr, uuid.UUID(thr.id))
}

// GetThresher creates a new Thresher and returns its pointer.
//
// Threser has its own s2.ServiceServer and msgsrv.MessageServer.
func New(sb *srvbus.ServiceBus, log *zap.SugaredLogger) (*Thresher, error) {
	if log == nil {
		return nil, errs.ErrNoLogger
	}

	if sb == nil {
		var err error
		sb, err = srvbus.New(uuid.Nil, log)
		if err != nil {
			return nil,
				fmt.Errorf(
					"couldn't create a ServiceBus for the thresher: %v", err)
		}
	}

	id := model.NewID()
	thresher := &Thresher{
		id:        id,
		instances: []*Instance{},
		log:       log.Named("THR [" + id.String() + "]"),
		sBus:      sb,
	}

	return thresher, nil
}

// create a new instance of the process and register it in the thresher.
func (thr *Thresher) NewInstance(
	p *model.Process) (*Instance, error) {

	if !thr.IsRunned() {
		return nil, errs.ErrNotRunned
	}

	sn, err := p.Copy()
	if err != nil {
		return nil,
			NewPEErr(nil, nil, "couldn't create a copy form process '%s'[%s]",
				p.Name(), p.ID().String())
	}

	iID := model.NewID()
	pi := &Instance{
		id:       iID,
		snapshot: sn,
		Thr:      thr,
		vs:       make(model.VarStore),
		tracks:   make(map[model.Id]*Track),
		log:      thr.log.Named("INST:" + iID.GetLast(4))}

	if sn.HasMessages() {
		pi.mQueue = fmt.Sprintf("MQ%v", pi.snapshot.OriginID)
	}

	thr.Lock()
	thr.instances = append(thr.instances, pi)
	thr.Unlock()

	thr.EmitEvent(instNewEvt,
		fmt.Sprintf(
			"{process_id: \"%v\", process_name: \"%s\", instance_id: \"%v\"}",
			p.ID(), p.Name(), iID))

	thr.log.Infow("new instance created",
		zap.String("process_name", p.Name()),
		zap.Stringer("process_id", p.ID()),
		zap.Stringer("instance_id", iID))

	if thr.IsRunned() {
		go pi.Run(thr.ctx)
	}

	return pi, nil
}

// runs a thresher.
func (thr *Thresher) Run(ctx context.Context) error {
	if thr.IsRunned() {
		return errs.ErrAlreadyRunned
	}

	if !thr.sBus.IsRunned() {
		if err := thr.sBus.Run(ctx); err != nil {
			return fmt.Errorf("couldn't start a service bus: %v", err)
		}
	}

	thr.Lock()
	thr.runned = true
	thr.ctx = ctx
	thr.Unlock()

	// run context listener to stop the thresher
	go func() {
		thr.log.Debug("thresher context listener started")

		<-ctx.Done()

		thr.Lock()
		thr.runned = false
		thr.Unlock()

		thr.log.Info("thresher stopped by context")

		thr.log.Debug("thresher context listener stopped")
	}()

	thr.log.Info(thrStart)
	thr.EmitEvent(thrStartEvt, "")

	for _, pi := range thr.instances {
		if pi.state == IsCreated {
			go pi.Run(thr.ctx)
		}
	}

	return nil
}
