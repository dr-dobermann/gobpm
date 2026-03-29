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
type State uint8

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
	scopes              map[scope.DataPath]map[string]data.Data
	tracks              map[string]*track
	rootScope           scope.DataPath
	runtimeScope        scope.DataPath
	foundation.BaseElement
	tokens []*token
	wg     sync.WaitGroup
	m      sync.RWMutex
	state  State
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
		state:               Ready,
		s:                   s,
		scopes:              map[scope.DataPath]map[string]data.Data{},
		tracks:              map[string]*track{},
		tokens:              []*token{},
		parentScope:         parentScope,
		parentEventProducer: ep,
		rr:                  rr,
	}

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
	inst.m.RLock()
	defer inst.m.RUnlock()

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
) error {
	if ctx == nil {
		return errs.New(
			errs.M("empty context for instance"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if inst.state != Ready {
		return errs.New(
			errs.M("invalid instance state to run",
				inst.state),
			errs.C(errorClass, errs.InvalidState),
			errs.D("current_state", inst.state))
	}

	inst.m.Lock()
	inst.ctx = ctx
	inst.startTime = time.Now()
	inst.m.Unlock()

	if err := inst.runTracks(ctx); err != nil {
		return err
	}

	// run tracks ending watcher
	grChan := make(chan struct{})
	go func() {
		inst.wg.Wait()

		close(grChan)

		inst.updateState(Finished)
	}()

	return nil
}

// runTracks runs all tracks of the instance.
func (inst *Instance) runTracks(ctx context.Context) error {
	inst.state = Runned

	// run only registered tracks, not the ones created by runned track's forks.
	tracks := append([]*track{}, maps.Values(inst.tracks)...)
	for _, t := range tracks {
		tt := t
		inst.runSingleTrack(ctx, tt)
	}

	return nil
}

// addTrack adds a new track into the instance's track pool.
// If instance is running, added track also runs.
func (inst *Instance) addTrack(ctx context.Context, nt *track) error {
	if nt == nil {
		return fmt.Errorf("couldn't add empty track to instance %q", inst.ID())
	}

	inst.m.Lock()
	defer inst.m.Unlock()

	if _, ok := inst.tracks[nt.ID()]; ok {
		return fmt.Errorf("track from node %q(%s) already registered in instance %q",
			inst.tracks[nt.ID()].steps[0].node.Name(),
			inst.tracks[nt.ID()].steps[0].node.ID(),
			inst.ID())
	}

	inst.tracks[nt.ID()] = nt

	if inst.state == Runned {
		inst.runSingleTrack(ctx, nt)
	}

	return nil
}

// runSingleTrack starts a new track and add it to instance's WaitGroup.
func (inst *Instance) runSingleTrack(ctx context.Context, t *track) {
	inst.wg.Add(1)

	go func(t *track) {
		defer inst.wg.Done()

		t.run(ctx)
	}(t)
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
		d = values.NewVariable(inst.state)

	case TracksCount:
		tc := len(inst.tracks)
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
	inst.m.Lock()
	defer inst.m.Unlock()

	if inst.state != Runned {
		return
	}

	// check if there is any alive token. If any, then return.
	if slices.ContainsFunc(
		inst.tokens,
		func(t *token) bool {
			return t.state == TokenAlive
		},
	) {
		return
	}

	// if there is no alive token, stop the instance.
	inst.state = Stopping

	go func() {
		for _, t := range inst.tracks {
			t.stop()
		}
	}()
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
