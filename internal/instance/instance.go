/*
Package instance provides process instance management for BPMN execution.

This package is part of GoBPM - Business Process Management Engine for Go.
See LICENSE file for license information.

Author: dr-dobermann (rgabitov@gmail.com)
Repository: https://github.com/dr-dobermann/gobpm
*/
package instance

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/exp/maps"

	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/interactor"
	"github.com/dr-dobermann/gobpm/internal/renv"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

const (
	errorClass = "INSTANCE_ERROR"

	runtimeVars = "RUNTIME"

	// StartedAt represents the started time variable name.
	StartedAt = "STARTED_AT"
	// CurrState represents the current state variable name.
	CurrState = "STATE"
	// TracksCount represents the tracks count variable name.
	TracksCount = "TRACKS_CNT"
)

// State represents the process instance state.
// uint32-backed so it can live in an atomic.Uint32 without a narrowing
// conversion (the instance's run state is read lock-free via State()).
type State uint32

const (
	// Created represents a created instance state.
	Created State = iota
	// Ready represents a ready instance state.
	Ready
	// StartingTracks represents a starting tracks state.
	StartingTracks
	// Runned represents a running instance state.
	Runned
	// Stopping represents a stopping instance state.
	Stopping
	// Paused represents a paused instance state.
	Paused
	// FinishingTracks represents a finishing tracks state.
	FinishingTracks
	// Finished represents a finished instance state.
	Finished
	// Canceled represents a canceled instance state.
	Canceled
)

func (s State) String() string {
	return []string{
		"Created",
		"Ready",
		"StartingTracks",
		"Runned",
		"Stopping",
		"Paused",
		"FinishingTracks",
		"FInished",
		"Canceled",
	}[s]
}

// dataFinder is used to find Data in Scope by Name or by Id.
type dataFinder func(data.Data) bool

// Instance represents a process instance for execution.
type Instance struct {
	startTime           time.Time
	ctx                 context.Context
	rr                  interactor.Registrator
	parentEventProducer eventproc.EventProducer
	parentScope         scope.Scope
	s                   *snapshot.Snapshot
	now                 func() time.Time
	scopes              map[scope.DataPath]map[string]data.Data
	tracks              map[string]*track // owned by loop(); not guarded by m
	events              chan trackEvent   // tracks -> loop()
	loopDone            chan struct{}     // closed when loop() exits
	rootScope           scope.DataPath
	runtimeScope        scope.DataPath
	foundation.BaseElement
	tokens     []*token // guarded by m (removed in M5)
	trackCount atomic.Int64
	state      atomic.Uint32 // State; written only by loop(), read via State()
	m          sync.RWMutex  // guards scopes + tokens (NOT lifecycle state/tracks)
}

// New creates a new Instance from the Snapshot s and sets state to Ready.
func New(
	s *snapshot.Snapshot,
	parentScope scope.Scope,
	ep eventproc.EventProducer,
	rr interactor.Registrator,
) (*Instance, error) {
	if s == nil {
		return nil,
			errs.New(
				errs.M("no snapshot is given"),
				errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if ep == nil {
		return nil,
			errs.New(
				errs.M("empty parent event producer"),
				errs.C(errorClass, errs.EmptyNotAllowed))
	}

	be, err := foundation.NewBaseElement()
	if err != nil {
		return nil, fmt.Errorf("failed to create base element: %w", err)
	}

	inst := Instance{
		BaseElement:         *be,
		s:                   s,
		now:                 time.Now,
		scopes:              map[scope.DataPath]map[string]data.Data{},
		tracks:              map[string]*track{},
		events:              make(chan trackEvent),
		loopDone:            make(chan struct{}),
		tokens:              []*token{},
		parentScope:         parentScope,
		parentEventProducer: ep,
		rr:                  rr,
	}
	inst.state.Store(uint32(Ready))

	if err := inst.loadProperties(parentScope); err != nil {
		return nil, errs.New(
			errs.M("couldn't load process'es properties into Instance scope"),
			errs.E(err),
			errs.C(errorClass, errs.BulidingFailed),
			errs.D("process_name", s.ProcessName),
			errs.D("process_id", s.ProcessID))
	}

	if err := inst.createTracks(); err != nil {
		return nil, err
	}

	// TracksCount reflects all tracks created (initial + forks); seed it with
	// the initial tracks. The loop adds forks; ended tracks are retained.
	inst.trackCount.Store(int64(len(inst.tracks)))

	return &inst, nil
}

// loadProperties sets the Instance rootScope name and load process'es
// properties into the instance's root Scope.
func (inst *Instance) loadProperties(parentScope scope.Scope) error {
	dd := make([]data.Data, 0, len(inst.s.Properties))
	for _, p := range inst.s.Properties {
		dd = append(dd, p)
	}

	inst.rootScope = scope.RootDataPath
	if parentScope != nil {
		inst.rootScope = parentScope.Root()
	}

	var err error

	inst.rootScope, err = inst.rootScope.Append(inst.s.ProcessName)
	if err != nil {
		return fmt.Errorf("couldn't create instance's scope data path: %w", err)
	}

	inst.runtimeScope, err = inst.rootScope.Append(runtimeVars)
	if err != nil {
		return fmt.Errorf("couldn't create instance's scope runtime variables data path: %w", err)
	}

	inst.scopes[inst.rootScope] = map[string]data.Data{}
	if err := inst.addData(inst.rootScope, dd...); err != nil {
		return err
	}

	return nil
}

// State returns current state of the Instance.
func (inst *Instance) State() State {
	return State(inst.state.Load())
}

// setState sets a new instance state. Written only from loop() (the single
// owner of lifecycle state) and from Run(); State() readers see it via the
// atomic, so no lock is needed.
func (inst *Instance) setState(newState State) {
	inst.state.Store(uint32(newState))
}

// Run starts the process instance execution. Execution could be stopped by
// cancel function of the context.
func (inst *Instance) Run(
	ctx context.Context,
) error {
	if ctx == nil {
		return errs.New(
			errs.M("empty context for instance"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if inst.State() != Ready {
		return errs.New(
			errs.M("invalid instance state to run"),
			errs.C(errorClass, errs.InvalidState),
			errs.D("current_state", inst.State()))
	}

	inst.ctx = ctx
	inst.startTime = inst.now()
	inst.setState(Runned)

	// initial tracks were built by createTracks() during New; hand them to the
	// loop, which becomes the sole owner of lifecycle state from here on.
	initial := maps.Values(inst.tracks)

	go inst.loop(ctx, initial)

	return nil
}

// trackEvent is a message from a track to the Instance event loop. M2 carries
// the coarse lifecycle set; M4 refines the fork into an active-flows event.
type trackEvent struct {
	track *track
	kind  trackEventKind
}

type trackEventKind uint8

const (
	evSpawn trackEventKind = iota // register + run a new track (from a fork)
	evEnded                       // a track's run() returned
	evStop                        // request to stop the instance (no live token)
)

// emit delivers a track event to the loop. It never blocks forever: if the
// loop has exited or the context is canceled it drops the event.
func (inst *Instance) emit(ev trackEvent) {
	select {
	case inst.events <- ev:
	case <-inst.loopDone:
	case <-inst.ctx.Done():
	}
}

// loop is the single owner of the Instance's lifecycle state (the tracks
// registry and the run state). Tracks never mutate that state directly — they
// emit events here, applied in order in this one goroutine, so no lock guards
// lifecycle state. The instance finishes when all tracks have ended.
func (inst *Instance) loop(ctx context.Context, initial []*track) {
	defer close(inst.loopDone)

	active := 0
	stopping := false

	spawn := func(t *track) {
		inst.tracks[t.ID()] = t
		active++

		go func(t *track) {
			t.run(ctx)
			inst.emit(trackEvent{kind: evEnded, track: t})
		}(t)
	}

	stopAll := func() {
		if stopping {
			return
		}
		stopping = true
		inst.setState(Stopping)
		for _, t := range inst.tracks {
			t.stop()
		}
	}

	for _, t := range initial {
		spawn(t)
	}

	if active == 0 {
		inst.setState(Finished)
		return
	}

	done := ctx.Done()
	for active > 0 {
		select {
		case <-done:
			done = nil
			stopAll()

		case ev := <-inst.events:
			switch ev.kind {
			case evSpawn:
				inst.trackCount.Add(1)
				spawn(ev.track)
				if stopping {
					ev.track.stop()
				}

			case evEnded:
				active--

			case evStop:
				stopAll()
			}
		}
	}

	inst.setState(Finished)
}

// createTrack creates all initial tracks of the Instance.
func (inst *Instance) createTracks() error {
	for _, n := range inst.s.Nodes {
		_, boundaryEvent := n.(flow.BoundaryEvent)
		if len(n.Incoming()) != 0 ||
			n.NodeType() == flow.GatewayNodeType ||
			boundaryEvent {
			continue
		}

		t, err := newTrack(n, inst, nil)
		if err != nil {
			return err
		}

		inst.tracks[t.ID()] = t
	}

	return nil
}

// addData adds data to scope named path
func (inst *Instance) addData(path scope.DataPath, dd ...data.Data) error {
	inst.m.RLock()
	vv, ok := inst.scopes[path]
	inst.m.RUnlock()

	if !ok {
		vv = make(map[string]data.Data)
	}

	for _, d := range dd {
		if d == nil {
			return fmt.Errorf("data is empty")
		}

		dn := strings.TrimSpace(d.Name())
		if dn == "" {
			return fmt.Errorf("couldn't add data with no name")
		}

		vv[dn] = d
	}

	inst.m.Lock()
	inst.scopes[path] = vv
	inst.m.Unlock()

	return nil
}

// getData is looking for data.Data in exec.Scope (Instance).
// if there is no data in path, then getData looks upper path
// until it checks instance root path.
func (inst *Instance) getData(
	path scope.DataPath,
	finder dataFinder,
) (data.Data, error) {
	if err := path.Validate(); err != nil {
		return nil, err
	}

	var err error

	for {
		s, ok := inst.scopes[path]
		if ok {
			for _, d := range s {
				if finder(d) {
					return d, nil
				}
			}
		}

		if path == scope.RootDataPath {
			break
		}

		path, err = path.DropTail()
		if err != nil {
			return nil,
				fmt.Errorf("couldn't get upper level for Scope %q: %w",
					path.String(), err)
		}
	}

	return nil,
		fmt.Errorf("data not found")
}

// getRuntimeVar tries to find the Instance's runtime variable by its name.
func (inst *Instance) getRuntimeVar(name string) (data.Data, error) {
	var d data.Value

	switch name {
	case StartedAt:
		d = values.NewVariable(inst.startTime)

	case CurrState:
		d = values.NewVariable(inst.State())

	case TracksCount:
		tc := int(inst.trackCount.Load())
		d = values.NewVariable(tc)

	default:
		return nil,
			fmt.Errorf("invalid runtime variable name %q", name)
	}

	id, err := data.NewItemDefinition(d)
	if err != nil {
		return nil,
			fmt.Errorf(
				"couldn't create an ItemDefinition for runtime variable %q: %w",
				name, err)
	}

	iae, err := data.NewItemAwareElement(id, data.ReadyDataState)
	if err != nil {
		return nil,
			fmt.Errorf(
				"couldn't create an ItemAwareElement for runtime variable %q: %w",
				name, err)
	}

	p, err := data.NewParameter(name, iae)
	if err != nil {
		return nil,
			fmt.Errorf(
				"couldn't create an ItemDefinition for runtime variable %q: %w",
				name, err)
	}

	return p, nil
}

// addToken adds a new token into Instance.
func (inst *Instance) addToken(t *token) {
	if st := inst.State(); st != Runned {
		return
	}

	inst.m.Lock()
	inst.tokens = append(inst.tokens, t)
	inst.m.Unlock()
}

// tokenConsumed checks all tokens of the Instance and if there is
// no alive token, all runned tracks stopped, and Instance execution finished.
func (inst *Instance) tokenConsumed(_ *token) {
	if inst.State() != Runned {
		return
	}

	// check if there is any alive token (tokens are guarded by m until M5).
	inst.m.Lock()
	anyAlive := slices.ContainsFunc(
		inst.tokens,
		func(t *token) bool {
			return t.state == TokenAlive
		},
	)
	inst.m.Unlock()

	if anyAlive {
		return
	}

	// no live token — ask the loop (the sole state owner) to stop the instance.
	inst.emit(trackEvent{kind: evStop})
}

// -------------------- exec.EventProducer interface ---------------------------

// RegisterEvent register tracks awaited for the event.
// Once event is fired, then track's EventProcessor called.
func (inst *Instance) RegisterEvent(
	proc eventproc.EventProcessor,
	eDef flow.EventDefinition,
) error {
	is := inst.State()
	if is != Runned {
		return errs.New(
			errs.M("instance isn't runned (current state: %s)",
				is),
			errs.C(errorClass, errs.InvalidState),
			errs.D("requester_id", proc.ID()))
	}

	if proc == nil {
		return errs.New(
			errs.M("empty EventProcessor"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if eDef == nil {
		return errs.New(
			errs.M("empty EventDefinition"),
			errs.C(errorClass, errs.EmptyNotAllowed, errs.InvalidParameter))
	}

	if inst.parentEventProducer == nil {
		return errs.New(
			errs.M("no registered EventProducer"),
			errs.C(errorClass, errs.InvalidObject))
	}

	return inst.parentEventProducer.RegisterEvent(
		proc, eDef)
}

// UnregisterEvent removes event definition to EventProcessor link from
// EventProducer.
func (inst *Instance) UnregisterEvent(
	_ eventproc.EventProcessor,
	_ string,
) error {
	return nil
}

// PropagateEvent sends a fired throw event's eventDefinition
// up to chain of EventProducers
func (inst *Instance) PropagateEvent(
	ctx context.Context,
	eDef flow.EventDefinition,
) error {
	st := inst.State()
	if st != Runned {
		return errs.New(
			errs.M("instance isn't runned"),
			errs.C(errorClass, errs.InvalidState),
			errs.D("current_state", st),
			errs.D("instance_id", inst.ID()))
	}

	if err := inst.parentEventProducer.PropagateEvent(ctx, eDef); err != nil {
		return errs.New(
			errs.M("event propagation failed"),
			errs.C(errorClass, errs.OperationFailed),
			errs.D("event_definition_id", eDef.ID()),
			errs.D("event_definition_type", eDef.Type()),
			errs.E(err))
	}

	return nil
}

// -------------------- exec.Scope interface -----------------------------------

// Root returns the root dataPath of the Scope.
func (inst *Instance) Root() scope.DataPath {
	return inst.rootScope
}

// Scopes returns list of scopes controlled by Scope.
func (inst *Instance) Scopes() []scope.DataPath {
	inst.m.Lock()
	defer inst.m.Unlock()

	return maps.Keys(inst.scopes)
}

// AddData adds data.Data to the NodeDataLoader scope or to rootScope
// if NodeDataLoader is nil.
func (inst *Instance) AddData(
	ndl scope.NodeDataLoader,
	vv ...data.Data,
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

	return inst.addData(dp, vv...)
}

// GetData tries to return value of data.Data object with name Name.
// dataPath selects the initial scope to look for the name.
// If current Scope doesn't find the name, then it looks in upper
// Scope until find or failed to find.
//
// To get the Instance's runtime variables only GetData could be used,
// since runtime variables doesn't have Id.
func (inst *Instance) GetData(
	path scope.DataPath,
	name string,
) (data.Data, error) {
	if path == inst.runtimeScope {
		return inst.getRuntimeVar(name)
	}

	byName := func(d data.Data) bool {
		return d.Name() == name
	}

	d, err := inst.getData(path, byName)
	if err != nil {
		return nil,
			errs.New(
				errs.M("couldn't get Data %q from scoppe %s",
					name, inst.ID()),
				errs.E(err))
	}

	return d, nil
}

// GetDataByID tries to find data.Data in the Scope by its ItemDefinition
// ID. It starts looking for the data from dataPath and continues to locate
// it until Scope root.
func (inst *Instance) GetDataByID(
	path scope.DataPath,
	id string,
) (data.Data, error) {
	byID := func(d data.Data) bool {
		return d.ItemDefinition().ID() == id
	}

	d, err := inst.getData(path, byID)
	if err != nil {
		return nil,
			errs.New(
				errs.M("couldn't get Data #%s from scope %s",
					id, inst.ID()),
				errs.E(err))
	}

	return d, nil
}

// LoadData loads a data data.Data into the Scope into
// the dataPath.
func (inst *Instance) LoadData(
	ndl scope.NodeDataLoader,
	vv ...data.Data,
) error {
	dp, err := inst.rootScope.Append(ndl.Name())
	if err != nil {
		return errs.New(
			errs.M("couldn't create data path for node %q", ndl.Name()),
			errs.E(err))
	}

	return inst.addData(dp, vv...)
}

// ExtendScope adds a new child Scope to the Scope and returns
// its full path.
func (inst *Instance) ExtendScope(
	ndl scope.NodeDataLoader,
) error {
	dp, err := inst.rootScope.Append(ndl.Name())
	if err != nil {
		return errs.New(
			errs.M("couldn't add scope for %q"),
			errs.E(err))
	}

	inst.m.RLock()
	_, found := inst.scopes[dp]
	inst.m.RUnlock()

	if found {
		return errs.New(
			errs.M("scope %q already existed", dp.String()))
	}

	inst.m.Lock()
	inst.scopes[dp] = make(map[string]data.Data)
	inst.m.Unlock()

	if err := ndl.RegisterData(dp, inst); err != nil {
		return errs.New(
			errs.M("data loading for noed %q failed"),
			errs.E(err))
	}

	return nil
}

// LeaveScope calls the Scope to clear all data saved by NodeDataLoader.
func (inst *Instance) LeaveScope(ndl scope.NodeDataLoader) error {
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

	inst.m.Lock()
	defer inst.m.Unlock()
	vv, ok := inst.scopes[dp]
	if !ok {
		return nil
	}

	vnn := maps.Keys(vv)
	for _, v := range vnn {
		delete(inst.scopes[dp], v)
	}

	delete(inst.scopes, dp)

	return nil
}

// ------------------ exec.RuntimeEnvironment interface ------------------------

// InstanceID returns ID of the Instance.
func (inst *Instance) InstanceID() string {
	return inst.ID()
}

// EventProducer returns the EventProducer of the runtime.
func (inst *Instance) EventProducer() eventproc.EventProducer {
	return inst
}

// RenderRegistrator returns the render registrator for the instance.
func (inst *Instance) RenderRegistrator() interactor.Registrator {
	return inst.rr
}

// =============================================================================
// Interfaces check
var (
	_ eventproc.EventProducer = (*Instance)(nil)
	_ renv.RuntimeEnvironment = (*Instance)(nil)
	_ scope.Scope             = (*Instance)(nil)
)
