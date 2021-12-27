package thresher

import (
	"context"
	"fmt"
	"sync"

	"github.com/dr-dobermann/gobpm/model"
)

type InstanceState uint8

const (
	IsCreated InstanceState = iota
	IsPrepared
	IsRunning
	IsEnded
)

// ProcessInstance represents a single run-time process instance
type ProcessInstance struct {
	Thr *Thresher

	id    model.Id
	state InstanceState
	// the copy of the process model the instance is based on
	snapshot *model.Process
	vs       model.VarStore
	tracks   []*track

	//monitor *ctr.Monitor
	//audit   *ctr.Audit
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
			t, err := newTrack(n, pi, nil)
			if err != nil {
				return NewProcExecError(
					nil,
					"couldn't prepare an Instance for starting",
					err)
			}

			pi.tracks = append(pi.tracks, t)
		}
	}

	pi.state = IsPrepared

	return nil
}

func (pi *ProcessInstance) Run(ctx context.Context) error {
	if pi.state == IsEnded || pi.state == IsRunning {
		return NewProcExecError(
			nil,
			fmt.Sprintf("Instance %s has wrong state %v", pi.id, pi.state),
			nil)
	}

	if pi.state == IsCreated {
		if err := pi.prepare(); err != nil {
			return NewProcExecError(
				nil,
				fmt.Sprintf("couldn't prepare the Instance %v for running : %v",
					pi.id.String(), err),
				nil)
		}
	}

	pi.state = IsRunning
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		default:
		}

		var wg sync.WaitGroup

		tc := 0
		for _, t := range pi.tracks {
			if t.state != TsReady {
				continue
			}

			tc++
			wg.Add(1)
			go func(et *track) {
				defer wg.Done()

				if err := et.tick(ctx); err != nil {
					et.state = TsError
					et.lastErr = err
				}
			}(t)
		}
		wg.Wait()

		if tc == 0 {
			pi.state = IsEnded
			break
		}
	}

	return nil
}
