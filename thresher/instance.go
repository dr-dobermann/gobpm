package thresher

import (
	"context"
	"fmt"
	"sync"

	"github.com/dr-dobermann/gobpm/internal/errs"
	"github.com/dr-dobermann/gobpm/model"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

const (
	instStartEvt = "INSTANCE_START_EVT"
	instEndEvt   = "INSTANCE_END_EVT"
)

type InstanceState uint8

const (
	IsCreated InstanceState = iota
	IsPrepared
	IsRunning
	IsStopping // awaits tracks endings
	IsEnded
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

	Thr *Thresher

	id    model.Id
	state InstanceState

	// the copy of the process model the instance is based on
	snapshot *model.Process
	vs       model.VarStore

	// track holds the state for every single token path
	tracks map[model.Id]*Track
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
}

func (pi *Instance) setState(newState InstanceState) {
	pi.Lock()
	pi.state = newState
	pi.Unlock()
}

func (pi *Instance) VarStore() *model.VarStore {
	return &pi.vs
}

func (pi *Instance) MsgQueue() string {
	return pi.mQueue
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
	pi.tracks = make(map[model.Id]*Track)

	for _, n := range nn {
		// find tasks and events that
		// don't have incoming flows
		if n.Type() != model.EtGateway && !n.HasIncoming() {
			// create tracks from them
			t, err := newTrack(n, pi, nil)
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

	pi.state = IsPrepared

	return nil
}

// adds single track to the Instance.
// used from track.run to create a fork
func (pi *Instance) addTrack(tr *Track) error {
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
	if pi.state == IsRunning {
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
	if pi.state != IsCreated {
		return pi.NewErr(nil,
			"wrong state to run instance '%s' (should be IsCreated)",
			pi.state)
	}

	if pi.state == IsCreated {
		if err := pi.prepare(); err != nil {
			return pi.NewErr(err,
				"couldn't prepare the instance for running")
		}
	}

	pi.log.Info("instance starting...")

	pi.Lock()
	pi.state = IsRunning
	pi.ctx = ctx

	// run prepared tracks
	for _, t := range pi.tracks {
		pi.wg.Add(1)

		go func(et *Track) {
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

	pi.Thr.EmitEvent(instStartEvt,
		fmt.Sprintf("{instance_id: \"%v\"}", pi.id))

	pi.log.Info("instance started")

	defer pi.Thr.EmitEvent(instEndEvt,
		fmt.Sprintf("{instance_id: \"%v\"}", pi.id))

	defer pi.log.Info("instance stopped")

	select {
	// wait for context closing
	case <-ctx.Done():
		pi.setState(IsStopping)

		pi.wg.Wait()

		pi.setState(IsEnded)

		return ctx.Err()

	// or all tracks finished
	case <-grCh:
		pi.setState(IsEnded)
	}

	return nil
}
