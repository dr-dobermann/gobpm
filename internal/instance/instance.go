package instance

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

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
	"golang.org/x/exp/maps"

	"github.com/dr-dobermann/gobpm/pkg/monitor"
)

const (
	errorClass = "INSTANCE_ERROR"

	runtimeVars = "RUNTIME"

	// Runtime variables names
	StartedAt   = "STARTED_AT"
	CurrState   = "STATE"
	TracksCount = "TRACKS_CNT"
)

type State uint8

const (
	Created State = iota
	Ready
	StartingTracks
	Runned
	Stopping
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
		"Stopping",
		"Paused",
		"FinishingTracks",
		"FInished",
		"Canceled",
	}[s]
}

// dataFinder is used to find Data in Scope by Name or by Id.
type dataFinder func(data.Data) bool

// =============================================================================
type Instance struct {
	foundation.ID

	m sync.RWMutex

	// wg is used to hold track's go-routines tracing.
	wg sync.WaitGroup

	// state of the Instance.
	state State

	// the Snapshot, the Instance is based on.
	s *snapshot.Snapshot

	// Instance's runtime context.
	ctx context.Context

	// monId keeps last monitoring event id.
	monId atomic.Int64

	// Scopes holds accessible in the moment Data.
	// first map indexed by data path, the second map indexed by Data name.
	scopes map[scope.DataPath]map[string]data.Data

	// rootScope holds the root dataPath of the scope
	rootScope scope.DataPath

	// instance runtime variables scope
	runtimeScope scope.DataPath

	// parentScope hold reference on the parent scope which set up on Instance
	// creation.
	parentScope scope.Scope

	// parentEventProducer is used to register the Instance in events producers
	// chain.
	parentEventProducer eventproc.EventProducer

	// root event producer for the instance. usually it will be thresher
	// created the instance.
	// root event producer for the instance. usually it will be thresher
	// created the instance.
	eProd eventproc.EventProducer

	// render registrator registers nodes with renderers of human interaction.
	rr interactor.Registrator

	// tracks indexed by track Ids
	tracks map[string]*track

	tokens []*token

	// events keeps list of tracks that awaits for evnent.
	// events are indexed by event definition id.
	// inner map indexed by track id (EventProcessor's Id).
	events map[string]map[string]*track

	// Instance's run starting time.
	startTime time.Time

	monitors []monitor.Writer
}

// New creates a new Instance from the Snapshot s and sets state to Ready.
func New(
	s *snapshot.Snapshot,
	parentScope scope.Scope,
	ep eventproc.EventProducer,
	rr interactor.Registrator,
	mon monitor.Writer,
) (*Instance, error) {
	if s == nil {
		return nil,
			errs.New(
				errs.M("nil snapshot"),
				errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if ep == nil {
		return nil,
			errs.New(
				errs.M("empty parent event producer"),
				errs.C(errorClass, errs.EmptyNotAllowed))
	}

	inst := Instance{
		ID:                  *foundation.NewID(),
		state:               Ready,
		s:                   s,
		scopes:              map[scope.DataPath]map[string]data.Data{},
		tracks:              map[string]*track{},
		tokens:              []*token{},
		events:              map[string]map[string]*track{},
		parentScope:         parentScope,
		parentEventProducer: ep,
		rr:                  rr,
		monitors:            []monitor.Writer{},
	}

	if mon != nil {
		inst.RegisterWriter(mon)
	}

	if err := inst.loadProperties(parentScope); err != nil {
		return nil, errs.New(
			errs.M("couldn't load process'es properties into Instance scope"),
			errs.E(err),
			errs.C(errorClass, errs.BulidingFailed),
			errs.D("process_name", s.ProcessName),
			errs.D("process_id", s.ProcessId))
	}

	if err := inst.createTracks(); err != nil {
		return nil, err
	}

	return &inst, nil
}

// loadProperties sets the Instance rootScope name and load process'es
// properties into the instance's root Scope.
func (inst *Instance) loadProperties(parentScope scope.Scope) error {
	dd := []data.Data{}
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

	inst.show("INSTANCE.INIT", "instance.loadProperties",
		map[string]any{
			"properties_count": len(dd),
		})

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
	inst.show("INSTANCE.STATE", "state updated",
		map[string]any{
			"old_state": inst.state,
			"new_state": newState,
		})

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

	inst.show("INSTANCE.RUN", "running tracks",
		map[string]any{
			"number_of_tracks": len(inst.tracks),
		})

	if err := inst.runTracks(ctx); err != nil {
		return err
	}

	// run tracks ending watcher
	grChan := make(chan struct{})
	go func() {
		inst.wg.Wait()

		close(grChan)

		inst.unregisterEvents()

		inst.updateState(Finished)
	}()

	return nil
}

// runTracks runs all tracks of the instance.
func (inst *Instance) runTracks(ctx context.Context) error {
	inst.state = Runned

	// run only registered tracks, not created by runned track's forks.
	tracks := append([]*track{}, maps.Values(inst.tracks)...)
	for _, t := range tracks {
		inst.show(
			"INSTANCE.RUN",
			"track runned",
			map[string]any{
				"start_node": t.currentStep().node.Name(),
			})

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
		return fmt.Errorf("couldn't add empty track to instance %q", inst.Id())
	}

	inst.m.Lock()
	defer inst.m.Unlock()

	if _, ok := inst.tracks[nt.Id()]; ok {
		return fmt.Errorf("track from node %q(%s) already registered in instance %q",
			inst.tracks[nt.Id()].steps[0].node.Name(),
			inst.tracks[nt.Id()].steps[0].node.Id(),
			inst.Id())
	}

	inst.tracks[nt.Id()] = nt

	if inst.state == Runned {
		inst.show(
			"INSTANCE.RUN",
			"track runned",
			map[string]any{
				"start_node": nt.currentStep().node.Name(),
			})

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

	inst.show("INSTANCE.TOKEN", "token registered",
		map[string]any{
			"track_id":    t.trk.Id(),
			"instance_id": inst.Id(),
			"token_state": t.state,
		})
}

// tokenConsumed checks all tokens of the Instance and if there is
// no alive token, all runned tracks stopped, and Instance execution finished.
func (inst *Instance) tokenConsumed(t *token) {
	inst.m.Lock()
	defer inst.m.Unlock()

	if inst.state != Runned {
		return
	}

	inst.show("INSTANCE.TOKEN", "token consumed",
		map[string]any{
			"instance_id": inst.Id(),
			"track_id":    t.trk.Id(),
			"token_id":    t.Id(),
		})

	// check if there is any alive token. If any, then return.
	if slices.ContainsFunc(
		inst.tokens,
		func(t *token) bool {
			return t.state == TokenAlive
		},
	) {
		return
	}

	inst.show("INSTANCE.STOP", "all tokens consumed. stopping tracks...",
		map[string]any{
			"instance_id": inst.Id(),
		})

	// if there is no alive token, stop the instance.
	inst.state = Stopping

	go func() {
		for _, t := range inst.tracks {
			t.stop()
		}
	}()
}

// show sends an Event ev to all registered monitors.
func (inst *Instance) show(
	src, typ string,
	details map[string]any,
) {
	ev := monitor.Event{
		Source:  src,
		Type:    typ,
		At:      time.Now(),
		Details: details,
	}

	if ev.Details == nil {
		ev.Details = make(map[string]any)
	}

	ev.Details["instance_id"] = inst.Id()
	ev.Details["counter"] = inst.monId.Load()

	inst.monId.Add(1)

	for _, m := range inst.monitors {
		go func(w monitor.Writer) {
			w.Write(&ev)
		}(m)
	}
}

// TODO: fill the func or remove it's call.
func (inst *Instance) unregisterEvents() {
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
			errs.M("event definition not registered for instance"),
			errs.D("event_def_id", eDef.Id()),
			errs.D("instance_id", inst.Id()),
			errs.D("process_name", inst.s.ProcessName),
			errs.D("process_id", inst.s.ProcessId),
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
			errs.D("requester_id", proc.Id()))
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

	t, ok := proc.(*track)
	if !ok {
		return errs.New(
			errs.M("not a track (%q)", reflect.TypeOf(proc).String()),
			errs.C(errorClass, errs.TypeCastingError))
	}

	if inst.parentEventProducer != nil {
		if err := inst.parentEventProducer.RegisterEvent(
			inst, eDef); err != nil {
			return errs.New(
				errs.M(
					"couldn't register event in parent EventProducer"),
				errs.C(errorClass, errs.OperationFailed))
		}
	}

	inst.m.Lock()
	if _, ok := inst.events[eDef.Id()]; !ok {
		inst.events[eDef.Id()] = make(map[string]*track)
	}

	inst.events[eDef.Id()][t.Id()] = t
	inst.m.Unlock()

	return nil
}

// UnregisterEvent removes event definition to EventProcessor link from
// EventProducer.
func (inst *Instance) UnregisterEvent(
	ep eventproc.EventProcessor,
	eDefId string,
) error {
	inst.m.Lock()
	defer inst.m.Unlock()

	if _, ok := inst.events[eDefId]; !ok {
		return errs.New(
			errs.M("event definition isn't registered"),
			errs.C(errorClass, errs.InvalidParameter),
			errs.D("event_definition_id", eDefId))
	}

	if inst.eProd != nil {
		if err := inst.eProd.UnregisterEvent(ep, eDefId); err != nil {
			return errs.New(
				errs.M("event unregistration failed"),
				errs.C(errorClass, errs.OperationFailed),
				errs.E(err))
		}
	}

	delete(inst.events[eDefId], ep.Id())

	if len(inst.events[eDefId]) == 0 {
		delete(inst.events, eDefId)
	}

	return nil
}

// PropagateEvent gets a eventDefinition and sends it to all
// EventProcessors registered for this id of EventDefinition.
func (inst *Instance) PropagateEvent(
	ctx context.Context,
	eDefId flow.EventDefinition,
) error {
	if inst.eProd == nil {
		return errs.New(
			errs.M("event producer isn't presented for Instance %q[%s]",
				inst.s.ProcessName, inst.Id()),
			errs.C(errorClass, errs.ObjectNotFound))
	}

	if err := inst.eProd.PropagateEvent(ctx, eDefId); err != nil {
		return errs.New(
			errs.M("event emitting failed for Instance %q[%s]",
				inst.s.ProcessName, inst.Id()),
			errs.C(errorClass, errs.OperationFailed),
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
					name, inst.Id()),
				errs.E(err))
	}

	return d, nil
}

// GetDataById tries to find data.Data in the Scope by its ItemDefinition
// id.
// It starts looking for the data from dataPath and continues to locate
// it until Scope root.
func (inst *Instance) GetDataById(
	path scope.DataPath,
	id string,
) (data.Data, error) {
	byId := func(d data.Data) bool {
		return d.ItemDefinition().Id() == id
	}

	d, err := inst.getData(path, byId)
	if err != nil {
		return nil,
			errs.New(
				errs.M("couldn't get Data #%s from scope %s",
					id, inst.Id()),
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

// InstanceId retruns id of the Instance.
func (inst *Instance) InstanceId() string {
	return inst.Id()
}

// EventProducer returns the EventProducer of the runtime.
func (inst *Instance) EventProducer() eventproc.EventProducer {
	return inst
}

func (inst *Instance) RenderRegistrator() interactor.Registrator {
	return inst.rr
}

// ------------------- monitor.WriterRegistrator -------------------------------

// RegisterWriter registers single non-nil unique monitoring event writer on the
// Instance.
func (inst *Instance) RegisterWriter(m monitor.Writer) {
	if m == nil {
		return
	}

	inst.m.Lock()
	defer inst.m.Unlock()

	if idx := slices.Index(inst.monitors, m); idx == -1 {
		inst.monitors = append(inst.monitors, m)
	}
}

// =============================================================================
// Interfaces check
var (
	_ eventproc.EventProducer   = (*Instance)(nil)
	_ eventproc.EventProcessor  = (*Instance)(nil)
	_ renv.RuntimeEnvironment   = (*Instance)(nil)
	_ scope.Scope               = (*Instance)(nil)
	_ monitor.WriterRegistrator = (*Instance)(nil)
)
