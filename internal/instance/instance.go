package instance

import (
	"context"
	"reflect"
	"sync"

	"github.com/dr-dobermann/gobpm/internal/exec"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

const errorClass = "INSTANCE_ERROR"

type State uint8

const (
	Created State = iota
	Ready
	StartingTracks
	Runned
	Paused
	FinishingTracks
	Finished
	Canceled
)

const (
	rootScope exec.DataPath = "/"
)

func (s State) String() string {
	return []string{
		"Created",
		"Ready",
		"StartingTracks",
		"Runned",
		"Paused",
		"FinishingTracks",
		"FInished",
		"Canceled",
	}[s]
}

type Instance struct {
	foundation.ID

	m sync.Mutex

	// wg is used to hold track's go-routines tracing.
	wg sync.WaitGroup

	state State
	s     *exec.Snapshot

	// initEvents holds instance's initial event definitions
	initEvents []flow.EventDefinition

	// Scopes holds accessible in the moment Data.
	// first map indexed by data path, the second map indexed by Data name.
	scopes map[exec.DataPath]map[string]data.Data

	// root event producer for the instance. usually it will be thresher
	// created the instance.
	// root event producer for the instance. usually it will be thresher
	// created the instance.
	eProd exec.EventProducer

	// traks indexed by track Ids
	tracks map[string]*track

	// events keeps list of tracks that awaits for evnent.
	// events are indexed by event definition id
	events map[string]*track
}

// New creates a new Instance from the Snapshot s and sets state to Ready.
func New(
	s *exec.Snapshot,
	eDef ...flow.EventDefinition,
) (*Instance, error) {
	inst := Instance{
		ID:         *foundation.NewID(),
		state:      Ready,
		s:          s,
		initEvents: eDef,
		scopes:     map[exec.DataPath]map[string]data.Data{},
		tracks:     map[string]*track{},
		events:     map[string]*track{},
	}

	// adds all processes properties into defalut scope
	dd := []data.Data{}
	for _, p := range s.Properties {
		dd = append(dd, p)
	}

	if err := inst.addData(rootScope, dd...); err != nil {
		return nil, err
	}

	if err := inst.createTracks(); err != nil {
		return nil, err
	}

	return &inst, nil
}

// -------------------- exec.EventProcessor interface --------------------------

func (inst *Instance) ProcessEvent(eDef flow.EventDefinition) error {
	if eDef == nil {
		return errs.New(
			errs.M("empty event definition"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	inst.m.Lock()
	t, ok := inst.events[eDef.Id()]
	inst.m.Unlock()

	if !ok {
		return errs.New(
			errs.M("event definition %q isn't registered for instance %q of process %q(%s)",
				eDef.Id(), inst.Id(), inst.s.ProcessName, inst.s.ProcessId),
			errs.C(errorClass, errs.InvalidParameter))
	}

	if err := t.ProcessEvent(eDef); err != nil {
		return err
	}

	inst.m.Lock()
	delete(inst.events, eDef.Id())
	inst.m.Unlock()

	return nil
}

// -------------------- exec.EventProducer interface ---------------------------

// RegisterEvents register tracks awaited for the event.
// Once event is fired, then track's EventProcessor called.
func (inst *Instance) RegisterEvents(
	proc exec.EventProcessor,
	eDefs ...flow.EventDefinition,
) error {
	is := inst.State()
	if is != Runned {
		return errs.New(
			errs.M(
				"instance should be Runned to register events(current state: %s)",
				is),
			errs.C(errorClass, errs.InvalidState),
			errs.D("requester_id", proc.Id()))
	}

	if proc == nil {
		return errs.New(
			errs.M("empyt track"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	for _, ed := range eDefs {
		if ed == nil {
			continue
		}

		t, ok := proc.(*track)
		if !ok {
			return errs.New(
				errs.M("not track (%q)", reflect.TypeOf(proc).String()),
				errs.C(errorClass, errs.TypeCastingError))
		}

		inst.m.Lock()
		inst.events[ed.Id()] = t
		inst.m.Unlock()
	}

	return nil
}

// -------------------- exec.Scope interface -----------------------------------

// Name returns the Instance root Scope name.
func (inst *Instance) Name() string {
	return string(rootScope)
}

// GetData returns data value name from scope path.
func (inst *Instance) GetData(
	path exec.DataPath,
	name string,
) (data.Value, error) {
	inst.m.Lock()
	defer inst.m.Unlock()

	s, ok := inst.scopes[path]
	if !ok {
		return nil,
			errs.New(
				errs.M("couldn't find scope %q", path),
				errs.C(errorClass, errs.ObjectNotFound))
	}

	d, ok := s[name]
	if !ok {
		return nil,
			errs.New(
				errs.M("data %q isn't found on scope %q", name, path),
				errs.C(errorClass, errs.ObjectNotFound))
	}

	return d.Value(), nil
}

// ------------------ exec.RuntimeEnvironment interface ------------------------

// InstanceId retruns id of the Instance.
func (inst *Instance) InstanceId() string {
	return inst.Id()
}

// -----------------------------------------------------------------------------

// State returns current state of the Instance.
func (inst *Instance) State() State {
	inst.m.Lock()
	defer inst.m.Unlock()

	return inst.state
}

// updateState sets new state for the Instance.
func (inst *Instance) updateState(newState State) {
	inst.m.Lock()
	defer inst.m.Unlock()

	inst.state = newState
}

// Run starts the process instance execution. Execution could be stopped by
// cancel function of the context.
func (inst *Instance) Run(
	ctx context.Context,
	cancel context.CancelFunc,
	ep exec.EventProducer,
) error {
	inst.m.Lock()
	defer inst.m.Unlock()

	if inst.state != Ready {
		return errs.New(
			errs.M("invalid instance state to run (want: Ready, has: %s)",
				inst.state),
			errs.C(errorClass, errs.InvalidState))
	}

	inst.eProd = ep

	if err := inst.runInitialEvents(); err != nil {
		return err
	}

	if err := inst.runTracks(ctx); err != nil {
		return err
	}

	// run track ended watcher
	grChan := make(chan struct{})
	go func() {
		inst.wg.Wait()

		close(grChan)
	}()

	go func() {
		select {
		// wait for context cancelation
		case <-ctx.Done():
			inst.updateState(FinishingTracks)

			inst.wg.Done()

			inst.updateState(Canceled)

		// or all tracks finishing
		case <-grChan:
			inst.updateState(Finished)
		}

		// run cancel on the end to free resources.
		cancel()
	}()

	return nil
}

// runTracks runs all tracks of the instance.
func (inst *Instance) runTracks(ctx context.Context) error {
	inst.m.Lock()
	defer inst.m.Unlock()

	if inst.state != Ready {
		return errs.New(
			errs.M("invalid instance state to run (want: Ready, has: %s)",
				inst.state),
			errs.C(errorClass, errs.InvalidState))
	}

	for _, t := range inst.tracks {
		inst.wg.Add(1)

		go func(t *track) {
			defer inst.wg.Done()

			t.run(ctx)
		}(t)
	}

	return nil
}

// addTrack adds a new track into the track pool.
// If instance is running, added track also runs.
func (inst *Instance) addTrack(ctx context.Context, nt *track) error {
	if nt == nil {
		return errs.New(
			errs.M("couldn't add empty track to instance %q", inst.Id()),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	inst.m.Lock()
	defer inst.m.Unlock()

	if _, ok := inst.tracks[nt.Id()]; ok {
		return errs.New(
			errs.M("track from node %q(%s) already registered in instance %q",
				inst.tracks[nt.Id()].steps[0].node.Name(),
				inst.tracks[nt.Id()].steps[0].node.Id(),
				inst.Id()),
			errs.C(errorClass, errs.DuplicateObject))
	}

	inst.tracks[nt.Id()] = nt

	if inst.state == Runned {
		inst.wg.Add(1)

		go func() {
			defer inst.wg.Done()
			nt.run(ctx)
		}()
	}

	return nil
}

// runIntialEvents feeds event's definitions on Instance creating into tracks
// await the events to continue.
func (inst *Instance) runInitialEvents() error {
	for _, d := range inst.initEvents {
		t, ok := inst.events[d.Id()]
		if !ok {
			return errs.New(
				errs.M("event definition %q isn't registered in instance %q of process %q(%s)",
					d.Id(), inst.Id(), inst.s.ProcessName, inst.s.ProcessId),
				errs.C(errorClass, errs.InvalidObject))
		}

		if err := t.ProcessEvent(d); err != nil {
			return err
		}

		inst.m.Lock()
		delete(inst.events, d.Id())
		inst.m.Unlock()
	}

	return nil
}

// createTrack creates all initial tracks of the Instance.
func (inst *Instance) createTracks() error {
	for _, n := range inst.s.Nodes {
		_, boundaryEvent := n.(flow.BoudaryEvent)
		if len(n.Incoming()) != 0 ||
			n.NodeType() == flow.GatewayNodeType ||
			boundaryEvent {
			continue
		}

		t, err := newTrack(n, inst, newToken(inst))
		if err != nil {
			return err
		}

		inst.tracks[t.Id()] = t
	}

	return nil
}

// addData adds data to scope named path
func (inst *Instance) addData(path exec.DataPath, dd ...data.Data) error {
	if err := path.Validate(); err != nil {
		return err
	}

	vv, ok := inst.scopes[path]
	if !ok {
		vv = map[string]data.Data{}
	}

	for _, d := range dd {
		if d == nil {
			return errs.New(
				errs.M("data is empty"),
				errs.C(errorClass, errs.EmptyNotAllowed))
		}

		vv[d.Name()] = d
	}

	inst.scopes[path] = vv

	return nil
}
