package instance

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"

	"github.com/dr-dobermann/gobpm/internal/exec"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/helpers"
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

// =============================================================================
type Instance struct {
	foundation.ID

	m sync.Mutex

	// wg is used to hold track's go-routines tracing.
	wg sync.WaitGroup

	// state of the Instance.
	state State

	// the Snapshot, the Instance is based on.
	s *exec.Snapshot

	// Instance's runtime context.
	ctx context.Context

	// Scopes holds accessible in the moment Data.
	// first map indexed by data path, the second map indexed by Data name.
	scopes map[exec.DataPath]map[string]data.Data

	// rootScope holds the root dataPath of the scope
	rootScope exec.DataPath

	// parentScope hold reference on the parent scope which set up on Instance
	// creation.
	parentScope exec.Scope

	// parentEventProducer is used to register the Instance in events producers
	// chain.
	parentEventProducer exec.EventProducer

	// root event producer for the instance. usually it will be thresher
	// created the instance.
	// root event producer for the instance. usually it will be thresher
	// created the instance.
	eProd exec.EventProducer

	// tracks indexed by track Ids
	tracks map[string]*track

	tokens []*token

	// events keeps list of tracks that awaits for evnent.
	// events are indexed by event definition id.
	// inner map indexed by track id.
	events map[string]map[string]*track
}

// New creates a new Instance from the Snapshot s and sets state to Ready.
func New(
	s *exec.Snapshot,
	parentScope exec.Scope,
	ep exec.EventProducer,
) (*Instance, error) {
	var err error

	inst := Instance{
		ID:                  *foundation.NewID(),
		state:               Ready,
		s:                   s,
		scopes:              map[exec.DataPath]map[string]data.Data{},
		tracks:              map[string]*track{},
		tokens:              []*token{},
		events:              map[string]map[string]*track{},
		parentScope:         parentScope,
		parentEventProducer: ep,
	}

	// adds all processes properties into defalut scope
	dd := []data.Data{}
	for _, p := range s.Properties {
		dd = append(dd, p)
	}

	inst.rootScope = exec.RootDataPath

	if parentScope != nil {
		inst.rootScope, err = parentScope.Root().Append(s.ProcessName)
		if err != nil {
			return nil,
				errs.New(
					errs.M("couldn't create Instance Scope data path"),
					errs.E(err))
		}
	}

	inst.scopes[inst.rootScope] = map[string]data.Data{}
	if err := inst.addData(inst.rootScope, dd...); err != nil {
		return nil, err
	}

	if err := inst.createTracks(); err != nil {
		return nil, err
	}

	return &inst, nil
}

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
	if ctx == nil {
		return errs.New(
			errs.M("empty context for instance"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	inst.m.Lock()
	defer inst.m.Unlock()

	if inst.state != Ready {
		return errs.New(
			errs.M("invalid instance state to run (want: Ready, has: %s)",
				inst.state),
			errs.C(errorClass, errs.InvalidState))
	}

	inst.eProd = ep
	inst.ctx = ctx

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
		if cancel != nil {
			cancel()
		}
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

// createTrack creates all initial tracks of the Instance.
func (inst *Instance) createTracks() error {
	for _, n := range inst.s.Nodes {
		_, boundaryEvent := n.(flow.BoudaryEvent)
		if len(n.Incoming()) != 0 ||
			n.NodeType() == flow.GatewayNodeType ||
			boundaryEvent {
			continue
		}

		t, err := newTrack(n, inst, nil)
		if err != nil {
			return err
		}

		inst.tracks[t.Id()] = t
	}

	return nil
}

// addData adds data to scope named path
func (inst *Instance) addData(path exec.DataPath, dd ...data.Data) error {
	vv, ok := inst.scopes[path]
	if !ok {
		return errs.New(
			errs.M("couldn't find scope %q to add data", path.String()))
	}

	for _, d := range dd {
		if d == nil {
			return errs.New(
				errs.M("data is empty"))
		}

		dn := strings.TrimSpace(d.Name())
		if dn == "" {
			return errs.New(
				errs.M("couldn't add data with no name"))
		}

		vv[dn] = d
	}

	inst.scopes[path] = vv

	return nil
}

// createToken creates a new token and registers it in the Instance.
// if there is failure occurs, error returned.
func (inst *Instance) createToken() (*token, error) {
	if inst.State() != Runned {
		return nil, errs.New(
			errs.M("couldn't create token on non-runned instance"),
			errs.C(errorClass, errs.InvalidState))
	}

	t := newToken(inst)

	inst.m.Lock()
	defer inst.m.Unlock()

	inst.tokens = append(inst.tokens, t)

	return t, nil
}

// -------------------- exec.EventProcessor interface --------------------------

// ProcessEvent processes single event definition, it registered in called
// EventProducer (usually it would be Thresher).
// If the caller doesn't provide the context (ctx == nil), then internal
// Instance context would be used.
func (inst *Instance) ProcessEvent(
	ctx context.Context,
	eDef flow.EventDefinition,
) error {
	if eDef == nil {
		return errs.New(
			errs.M("empty event definition"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if ctx == nil {
		ctx = inst.ctx
	}

	inst.m.Lock()
	tt, ok := inst.events[eDef.Id()]
	inst.m.Unlock()

	if !ok {
		return errs.New(
			errs.M("event definition %q isn't registered for instance %q of process %q(%s)",
				eDef.Id(), inst.Id(), inst.s.ProcessName, inst.s.ProcessId),
			errs.C(errorClass, errs.InvalidParameter))
	}

	ee := []error{}

	for _, t := range tt {
		if err := t.ProcessEvent(ctx, eDef); err != nil {
			ee = append(ee, err)
		}

		inst.m.Lock()
		delete(inst.events[eDef.Id()], t.Id())
		if len(inst.events[eDef.Id()]) == 0 {
			delete(inst.events, eDef.Id())
		}
		inst.m.Unlock()
	}

	if len(ee) != 0 {
		return errors.Join(ee...)
	}

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
			errs.M("instance should be Runned to register events (current state: %s)",
				is),
			errs.C(errorClass, errs.InvalidState),
			errs.D("requester_id", proc.Id()))
	}

	if proc == nil {
		return errs.New(
			errs.M("empty EventProcessor"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	for _, ed := range eDefs {
		if ed == nil {
			continue
		}

		t, ok := proc.(*track)
		if !ok {
			return errs.New(
				errs.M("not a track (%q)", reflect.TypeOf(proc).String()),
				errs.C(errorClass, errs.TypeCastingError))
		}

		if inst.parentEventProducer != nil {
			if err := inst.parentEventProducer.RegisterEvents(
				inst, ed); err != nil {
				return errs.New(
					errs.M(
						"couldn't register event in Thresher"),
					errs.C(errorClass, errs.OperationFailed))
			}
		}

		inst.m.Lock()
		if _, ok := inst.events[ed.Id()]; !ok {
			inst.events[ed.Id()] = make(map[string]*track)
		}

		inst.events[ed.Id()][t.Id()] = t
		inst.m.Unlock()
	}

	return nil
}

// UnregisterEvents removes event definition to EventProcessor link from
// EventProducer.
func (inst *Instance) UnregisterEvents(
	ep exec.EventProcessor,
	eDefs ...flow.EventDefinition,
) error {
	inst.m.Lock()
	defer inst.m.Unlock()

	for _, ed := range eDefs {
		if _, ok := inst.events[ed.Id()]; ok {
			delete(inst.events, ed.Id())
		}
	}

	if inst.eProd != nil {
		if err := inst.eProd.UnregisterEvents(ep, eDefs...); err != nil {
			return errs.New(
				errs.M("couldn't unregister an event from instance's event producer"),
				errs.C(errorClass, errs.OperationFailed),
				errs.E(err))
		}
	}

	return nil
}

// EmitEvents gets a list of eventDefinitions and sends them to all
// EventProcessors registered for this type of EventDefinition.
func (inst *Instance) EmitEvents(events ...flow.EventDefinition) error {
	if inst.eProd == nil {
		return errs.New(
			errs.M("event producer isn't presented for Instance %q[%s]",
				inst.s.ProcessName, inst.Id()),
			errs.C(errorClass, errs.ObjectNotFound))
	}

	if err := inst.eProd.EmitEvents(events...); err != nil {
		return errs.New(
			errs.M("event emiting failed for Instance %q[%s]",
				inst.s.ProcessName, inst.Id()),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err))
	}

	return nil
}

// -------------------- exec.Scope interface -----------------------------------

// Root returns the root dataPath of the Scope.
func (inst *Instance) Root() exec.DataPath {
	return inst.rootScope
}

// Scopes returns list of scopes controlled by Scope.
func (inst *Instance) Scopes() []exec.DataPath {
	inst.m.Lock()
	defer inst.m.Unlock()

	return helpers.MapKeys(inst.scopes)
}

// AddData adds data.Data to the NodeDataLoader scope or to rootScope
// if NodeDataLoader is nil.
func (inst *Instance) AddData(
	ndl exec.NodeDataLoader,
	values ...data.Data,
) error {
	var (
		dp  = inst.rootScope
		err error
	)

	if ndl != nil {
		dp, err = dp.Append(ndl.Name())
		if err != nil {
			return errs.New(
				errs.M("couldn't form data path for node %q", ndl.Name()),
				errs.E(err))
		}
	}

	inst.m.Lock()
	defer inst.m.Unlock()

	return inst.addData(dp, values...)
}

// GetData tries to return value of data.Data object with name Name.
// dataPath selects the initial scope to look for the name.
// If current Scope doesn't find the name, then it looks in upper
// Scope until find or failed to find.
func (inst *Instance) GetData(
	path exec.DataPath,
	name string,
) (data.Value, error) {
	inst.m.Lock()
	defer inst.m.Unlock()

	if err := path.Validate(); err != nil {
		return nil, err
	}

	var err error

	for p := path; ; p, err = p.DropTail() {
		if err != nil {
			return nil,
				errs.New(
					errs.M("couldn't get upper level for Scope %s:", p.String()),
					errs.E(err))
		}

		s, ok := inst.scopes[p]
		if !ok {
			continue
		}

		if d, ok := s[name]; ok {
			return d.Value(), nil
		}

		if p == exec.RootDataPath {
			break
		}
	}

	if inst.parentScope != nil {
		return inst.parentScope.GetData(
			inst.parentScope.Root(),
			name)
	}

	return nil,
		errs.New(
			errs.M("data %q isn't found on scope %q", name, path),
			errs.C(errorClass, errs.ObjectNotFound))
}

// LoadData loads a data data.Data into the Scope into
// the dataPath.
func (inst *Instance) LoadData(
	ndl exec.NodeDataLoader,
	values ...data.Data,
) error {
	inst.m.Lock()
	defer inst.m.Unlock()

	dp, err := inst.rootScope.Append(ndl.Name())
	if err != nil {
		return errs.New(
			errs.M("couldn't get data path for node %q", ndl.Name()),
			errs.E(err))
	}

	if _, ok := inst.scopes[dp]; !ok {
		return errs.New(
			errs.M("couldn't find scope for node %q (run ExtendScope first)",
				ndl.Name()))
	}

	return inst.addData(dp, values...)
}

// ExtendScope adds a new child Scope to the Scope and returns
// its full path.
func (inst *Instance) ExtendScope(
	ndl exec.NodeDataLoader,
) error {
	inst.m.Lock()
	defer inst.m.Unlock()

	dp, err := inst.rootScope.Append(ndl.Name())
	if err != nil {
		return errs.New(
			errs.M("couldn't add scope for %q"),
			errs.E(err))
	}

	if _, ok := inst.scopes[dp]; ok {
		return errs.New(
			errs.M("scope %q already existed", dp.String()))
	}

	inst.scopes[dp] = make(map[string]data.Data)

	if err := ndl.RegisterData(dp, inst); err != nil {
		return errs.New(
			errs.M("data loading for noed %q failed"),
			errs.E(err))
	}

	return nil
}

// LeaveScope calls the Scope to clear all data saved by NodeDataLoader.
func (inst *Instance) LeaveScope(ndl exec.NodeDataLoader) error {
	inst.m.Lock()
	defer inst.m.Unlock()

	if ndl == nil {
		return errs.New(
			errs.M("no NodeDataLoader"))
	}

	// get scope name for the NodeDataLoader
	dp, err := inst.rootScope.Append(ndl.Name())
	if err != nil {
		return errs.New(
			errs.M("couldn't compose data path for Node %q", ndl.Name()),
			errs.E(err))
	}

	vv, ok := inst.scopes[dp]
	if !ok {
		return nil
	}

	vnn := helpers.MapKeys(vv)
	for _, v := range vnn {
		delete(inst.scopes[dp], v)
	}

	return nil
}

// ------------------ exec.RuntimeEnvironment interface ------------------------

// InstanceId retruns id of the Instance.
func (inst *Instance) InstanceId() string {
	return inst.Id()
}

// -----------------------------------------------------------------------------

// =============================================================================
// Interfaces check
var (
	_ exec.EventProducer      = (*Instance)(nil)
	_ exec.EventProcessor     = (*Instance)(nil)
	_ exec.RuntimeEnvironment = (*Instance)(nil)
)
