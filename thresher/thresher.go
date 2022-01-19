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
	mid "github.com/dr-dobermann/gobpm/internal/identity"
	"github.com/dr-dobermann/gobpm/internal/instance"
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
	ThrNewEvt    = "NEW_THR_EVT"
	ThrStartEvt  = "THR_START_EVT"
	ThrStopEvt   = "THR_STOP_EVT"

	InstNewEvt = "NEW_INSTANCE_EVT"
)

type Thresher struct {
	sync.Mutex

	id        mid.Id
	instances map[mid.Id]*instance.Instance
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

	id := mid.NewID()
	thresher := &Thresher{
		id:        id,
		instances: make(map[mid.Id]*instance.Instance),
		log:       log.Named("THR [" + id.String() + "]"),
		sBus:      sb,
	}

	return thresher, nil
}

// create a new instance of the process and register it in the thresher.
func (thr *Thresher) NewInstance(
	p *model.Process) (mid.Id, error) {

	if !thr.IsRunned() {
		return mid.EmptyID(), errs.ErrNotRunned
	}

	pi, err := instance.New(p, thr.sBus, thr.log, thr)
	if err != nil {
		return mid.EmptyID(),
			fmt.Errorf("couldn't create instance for process '%s'[%v]: %v",
				p.Name(), p.ID(), err)
	}

	thr.Lock()
	thr.instances[pi.ID()] = pi
	thr.Unlock()

	thr.EmitEvent(InstNewEvt,
		fmt.Sprintf(
			"{process_id: \"%v\", process_name: \"%s\", instance_id: \"%v\"}",
			p.ID(), p.Name(), pi.ID()))

	thr.log.Infow("new instance created",
		zap.String("process_name", p.Name()),
		zap.Stringer("process_id", p.ID()),
		zap.Stringer("instance_id", pi.ID()))

	if thr.IsRunned() {
		go pi.Run(thr.ctx)
	}

	return pi.ID(), nil
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

		thr.EmitEvent(ThrStopEvt, "")

	}()

	thr.log.Info(thrStart)
	thr.EmitEvent(ThrStartEvt, "")

	for _, pi := range thr.instances {
		if pi.State() == instance.Created {
			go pi.Run(thr.ctx)
		}
	}

	return nil
}
