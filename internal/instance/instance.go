// Instance creates a run-time environment for the process.
package instance

import (
	"context"
	"fmt"
	"sync"

	"github.com/dr-dobermann/gobpm/internal/emitter"
	"github.com/dr-dobermann/gobpm/internal/errs"
	"github.com/dr-dobermann/gobpm/model"
	"github.com/dr-dobermann/gobpm/pkg/executor"
	"github.com/dr-dobermann/srvbus"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

const (
	InstStartEvt = "INSTANCE_START_EVT"
	InstEndEvt   = "INSTANCE_END_EVT"
)

type InstanceState uint8

const (
	Created InstanceState = iota
	Prepared
	Running
	Stopping // awaits tracks endings
	Ended
)

func (is InstanceState) String() string {
	return []string{
		"Created",
		"Prepared",
		"Running",
		"Stopping",
		"Ended",
	}[is]
}

// Instance represents a single run-time process instance
type Instance struct {
	sync.Mutex

	sBus *srvbus.ServiceBus

	id    model.Id
	state InstanceState

	// the copy of the process model the instance is based on
	snapshot *model.Process
	vs       *model.VarStore

	// track holds the state for every single token path
	tracks map[model.Id]*track
	wg     sync.WaitGroup

	//monitor *ctr.Monitor
	//audit   *ctr.Audit

	ctx context.Context
	log *zap.SugaredLogger

	// if the original process has a non-empty messages list
	// mQueue consists a non-empty message queue name on
	// Thresher.sBus.MessageServer
	// Queue name constructs as "MQ" + process_ID
	mQueue string

	Emitter emitter.EventEmitter

	gates map[model.Id]executor.GatewayExecutor
}

func (pi *Instance) ID() model.Id {
	return pi.id
}

func (pi *Instance) State() InstanceState {
	pi.Lock()
	defer pi.Unlock()

	return pi.state
}

func (pi *Instance) setState(newState InstanceState) {
	pi.Lock()
	pi.state = newState
	pi.Unlock()
}

func New(
	p *model.Process,
	sb *srvbus.ServiceBus,
	log *zap.SugaredLogger,
	ee emitter.EventEmitter) (*Instance, error) {

	sn, err := p.Copy()
	if err != nil {
		return nil,
			fmt.Errorf("couldn't create a copy form process '%s'[%s]: %v",
				p.Name(), p.ID().String(), err)
	}

	iID := model.NewID()
	pi := Instance{
		sBus:     sb,
		id:       iID,
		snapshot: sn,
		vs:       model.NewVarStore(),
		tracks:   make(map[model.Id]*track),
		wg:       sync.WaitGroup{},
		log:      log.Named("INS:" + iID.GetLast(4)),
		mQueue:   fmt.Sprintf("MQ%v", p.ID()),
		Emitter:  ee,
		gates:    make(map[model.Id]executor.GatewayExecutor)}

	return &pi, nil
}

// creates a new ProcessExecutingError
func (pi *Instance) NewErr(err error, format string, params ...interface{}) error {
	return ProcessExecutingError{
		pID:        pi.id,
		instanceID: pi.snapshot.ID(),
		trackID:    model.Id(uuid.Nil),
		Err:        err,
		msg:        fmt.Sprintf(format, params...),
	}
}

// prepare prepares the Instance object for start.
// prepare looks for nodes that don't have incoming flows and
// creates a list of tracks to work with
func (pi *Instance) prepare() error {

	// get through all nodes except the gateways
	nn, err := pi.snapshot.GetNodes(model.EtUnspecified)
	if err != nil {
		return pi.NewErr(err, "couldn't get nodes list from snapshot")
	}

	// clear tracks list
	pi.tracks = make(map[model.Id]*track)

	for _, n := range nn {
		// find tasks and events that
		// don't have incoming flows
		if n.Type() != model.EtGateway && !n.HasIncoming() {
			// create tracks from them
			t, err := newTrack(n, pi, nil, nil)
			if err != nil {
				return pi.NewErr(err,
					"couldn't prepare an Instance for starting")
			}

			// and add them to the tracks list
			pi.tracks[t.id] = t
		}
	}

	if len(pi.tracks) == 0 {
		return errs.ErrNoTracks
	}

	pi.state = Prepared

	return nil
}

// adds single track to the Instance.
// used from track.run to create a fork
func (pi *Instance) addTrack(tr *track) error {
	if tr == nil {
		return errs.ErrInvalidTrack
	}

	pi.Lock()
	defer pi.Unlock()

	// add track into instance pool
	pi.tracks[tr.id] = tr

	pi.log.Debugw("new track added",
		zap.Stringer("id", tr.id),
		zap.Stringer("node_type", tr.currentStep().node.Type()),
		zap.String("node_name", tr.currentStep().node.Name()))

	// if instance running, run the track too
	if pi.state == Running {
		pi.wg.Add(1)

		go func() {
			defer pi.wg.Done()

			tr.run(pi.ctx)
		}()
	}

	return nil
}

// runs an instance and all its tracks
func (pi *Instance) Run(ctx context.Context) error {
	if pi.state != Created {
		return pi.NewErr(nil,
			"wrong state to run instance '%s' (should be IsCreated)",
			pi.state)
	}

	if err := pi.prepare(); err != nil {
		return pi.NewErr(err,
			"couldn't prepare the instance for running")
	}

	pi.log.Info("instance starting...")

	pi.Lock()
	pi.state = Running
	pi.ctx = ctx

	// run prepared tracks
	for _, t := range pi.tracks {
		pi.wg.Add(1)

		go func(et *track) {
			defer pi.wg.Done()

			et.run(ctx)
		}(t)
	}

	pi.Unlock()

	// tracks ending watcher
	grCh := make(chan struct{})
	go func() {
		// wait for all tracks
		pi.wg.Wait()

		close(grCh)
	}()

	pi.Emitter.EmitEvent(InstStartEvt,
		fmt.Sprintf("{instance_id: \"%v\"}", pi.id))

	pi.log.Info("instance started")

	defer pi.Emitter.EmitEvent(InstEndEvt,
		fmt.Sprintf("{instance_id: \"%v\"}", pi.id))

	defer pi.log.Info("instance stopped")

	select {
	// wait for context closing
	case <-ctx.Done():
		pi.setState(Stopping)

		pi.wg.Wait()

		pi.setState(Ended)

		return ctx.Err()

	// or all tracks finished
	case <-grCh:
		pi.setState(Ended)
	}

	return nil
}

// returns an instance of GatewayExecutor if it was previously
// saved. If there is no Gateway with Id from ge, the new instance
// will be saved into the storage and returned.
func (pi *Instance) getGExInstance(
	ge executor.GatewayExecutor) executor.GatewayExecutor {

	pi.Lock()
	defer pi.Unlock()

	g, ok := pi.gates[ge.ID()]
	if !ok {
		pi.gates[ge.ID()] = ge
		g = ge
	}

	return g
}
