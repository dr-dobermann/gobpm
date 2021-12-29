package thresher

import (
	"context"
	"fmt"
	"sync"

	"github.com/dr-dobermann/gobpm/model"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type InstanceState uint8

const (
	IsCreated InstanceState = iota
	IsPrepared
	IsRunning
	IsEnded
)

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
	tracks map[model.Id]*track
	wg     sync.WaitGroup

	//monitor *ctr.Monitor
	//audit   *ctr.Audit

	log *zap.SugaredLogger
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

	pi.state = IsPrepared

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
				"couldn't prepare the instance for running : %v",
				err)
		}
	}

	pi.log.Info("instance starting...")

	pi.Lock()
	pi.state = IsRunning

	// run prepared tracks
	for _, t := range pi.tracks {
		pi.wg.Add(1)

		go func(et *track) {
			defer pi.wg.Done()

			et.run(ctx)
		}(t)
	}

	pi.Unlock()

	pi.log.Info("instance started")

	// wait for context closing
	<-ctx.Done()

	pi.log.Info("instance stopping: wait for tracks...")

	// wait all tracks stopping and exit
	pi.wg.Wait()

	pi.log.Info("instance stopped")

	return nil
}
