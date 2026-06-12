package scope

import (
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

// frameState tracks the frame lifecycle: open → committed | discarded.
type frameState uint8

const (
	frameOpen frameState = iota
	frameCommitted
	frameDiscarded
)

// Frame is the working set of one node execution by one track (ADR-010
// §2.3, SRD-007 FR-2): per-execution parameter and property instances,
// frame-first lookup with container walk-up, and an all-or-nothing commit.
//
// A frame is owned by exactly one execution — it is NOT safe for concurrent
// use; cross-track serialization happens in the Plane, never in frames.
type Frame struct {
	plane   *Plane
	inputs  map[string]*data.Parameter
	outputs map[string]*data.Parameter
	props   map[string]data.Data
	puts    map[string]data.Data
	at      DataPath
	trackID string
	nodeID  string
	state   frameState
}

// NewFrame creates the execution frame of node nodeID executed by track
// trackID, attached to the open container scope at.
func NewFrame(
	trackID, nodeID string,
	at DataPath,
	p *Plane,
) (*Frame, error) {
	trackID = strings.TrimSpace(trackID)
	nodeID = strings.TrimSpace(nodeID)

	if trackID == "" || nodeID == "" {
		return nil,
			errs.New(
				errs.M("NewFrame: empty track id or node id isn't allowed"),
				errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if p == nil {
		return nil,
			errs.New(
				errs.M("NewFrame: a nil Plane isn't allowed"),
				errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if err := p.checkContained("NewFrame", at); err != nil {
		return nil, err
	}

	if !p.opened(at) {
		return nil,
			errs.New(
				errs.M("NewFrame: container scope %q isn't open", at),
				errs.C(errorClass, errs.ObjectNotFound))
	}

	return &Frame{
		plane:   p,
		inputs:  map[string]*data.Parameter{},
		outputs: map[string]*data.Parameter{},
		props:   map[string]data.Data{},
		puts:    map[string]data.Data{},
		at:      at,
		trackID: trackID,
		nodeID:  nodeID,
	}, nil
}

// TrackID returns the id of the track owning the frame.
func (f *Frame) TrackID() string {
	return f.trackID
}

// NodeID returns the id of the node the frame belongs to.
func (f *Frame) NodeID() string {
	return f.nodeID
}

// InstantiateInputs builds the frame's input parameter instances from the
// node's immutable input definitions. The definitions stay untouched — the
// instance shares the definition's identity (ids) but carries its own value.
func (f *Frame) InstantiateInputs(defs []*data.Parameter) error {
	return f.instantiateParams("InstantiateInputs", defs, f.inputs)
}

// InstantiateOutputs builds the frame's output parameter instances from the
// node's immutable output definitions.
func (f *Frame) InstantiateOutputs(defs []*data.Parameter) error {
	return f.instantiateParams("InstantiateOutputs", defs, f.outputs)
}

// LoadProperties builds the frame's property instances from the node's
// property definitions. Properties are execution-local (BPMN §10.4
// visibility): they are readable through the frame and die with it — they
// are never committed to the container scope.
func (f *Frame) LoadProperties(defs []*data.Property) error {
	if err := f.checkOpen("LoadProperties"); err != nil {
		return err
	}

	for i, def := range defs {
		if def == nil {
			return errs.New(
				errs.M("LoadProperties: a nil property isn't allowed "+
					"(index %d)", i),
				errs.C(errorClass, errs.EmptyNotAllowed))
		}

		inst, err := instantiate(def.Name(), &def.ItemAwareElement)
		if err != nil {
			return errs.New(
				errs.M("LoadProperties: couldn't instantiate property %q",
					def.Name()),
				errs.C(errorClass, errs.OperationFailed),
				errs.E(err))
		}

		f.props[def.Name()] = inst
	}

	return nil
}

// Inputs returns the frame's input parameter instances.
func (f *Frame) Inputs() []*data.Parameter {
	return paramList(f.inputs)
}

// Outputs returns the frame's output parameter instances.
func (f *Frame) Outputs() []*data.Parameter {
	return paramList(f.outputs)
}

// Put stores node-produced values in the frame. Puts are committed to the
// container scope together with the outputs. A repeated name overwrites the
// previous value (last write wins within one execution).
func (f *Frame) Put(dd ...data.Data) error {
	if err := f.checkOpen("Put"); err != nil {
		return err
	}

	names, err := batchNames("Put", dd)
	if err != nil {
		return err
	}

	for i, d := range dd {
		f.puts[names[i]] = d
	}

	return nil
}

// GetData resolves name frame-first — inputs, outputs, properties, puts —
// and then walks the container scopes from the frame's attachment point up
// to the plane's root.
func (f *Frame) GetData(name string) (data.Data, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil,
			errs.New(
				errs.M("GetData: an empty data name isn't allowed"),
				errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if d, ok := f.lookup(func(d data.Data) bool {
		return d.Name() == name
	}); ok {
		return d, nil
	}

	return f.plane.GetData(f.at, name)
}

// GetDataByID resolves an ItemDefinition id frame-first and then through
// the container scopes from the frame's attachment point.
func (f *Frame) GetDataByID(id string) (data.Data, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil,
			errs.New(
				errs.M("GetDataByID: an empty ItemDefinition id isn't allowed"),
				errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if d, ok := f.lookup(func(d data.Data) bool {
		idef := d.ItemDefinition()

		return idef != nil && idef.ID() == id
	}); ok {
		return d, nil
	}

	return f.plane.GetDataByID(f.at, id)
}

// Commit flushes the frame's outputs and puts into the container scope as
// one atomic Plane batch and seals the frame. A frame commits at most once
// and never after Discard (ADR-010 §2.3).
func (f *Frame) Commit() error {
	if err := f.checkOpen("Commit"); err != nil {
		return err
	}

	batch := make([]data.Data, 0, len(f.outputs)+len(f.puts))

	for _, o := range f.outputs {
		batch = append(batch, o)
	}

	for _, p := range f.puts {
		batch = append(batch, p)
	}

	if err := f.plane.Commit(f.at, batch...); err != nil {
		return errs.New(
			errs.M("Commit: frame of node %q on track %q failed to commit",
				f.nodeID, f.trackID),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err))
	}

	f.state = frameCommitted

	return nil
}

// Discard seals the frame leaving the container scope untouched — the
// failure-path contract: a failed execution commits nothing (ADR-010 §2.3).
// Discard is idempotent and is a no-op on an already-sealed frame, so it is
// safe to defer unconditionally.
func (f *Frame) Discard() {
	if f.state == frameOpen {
		f.state = frameDiscarded
	}
}

// lookup searches the frame's own groups in resolution order.
func (f *Frame) lookup(finder dataFinder) (data.Data, bool) {
	for _, in := range f.inputs {
		if finder(in) {
			return in, true
		}
	}

	for _, out := range f.outputs {
		if finder(out) {
			return out, true
		}
	}

	for _, pr := range f.props {
		if finder(pr) {
			return pr, true
		}
	}

	for _, pt := range f.puts {
		if finder(pt) {
			return pt, true
		}
	}

	return nil, false
}

// checkOpen guards mutating operations on a sealed frame.
func (f *Frame) checkOpen(op string) error {
	if f.state != frameOpen {
		return errs.New(
			errs.M("%s: the frame of node %q on track %q is sealed",
				op, f.nodeID, f.trackID),
			errs.C(errorClass, errs.InvalidState))
	}

	return nil
}

// instantiateParams builds per-frame parameter instances from definitions
// into dst, rejecting nils and duplicate names.
func (f *Frame) instantiateParams(
	op string,
	defs []*data.Parameter,
	dst map[string]*data.Parameter,
) error {
	if err := f.checkOpen(op); err != nil {
		return err
	}

	for i, def := range defs {
		if def == nil {
			return errs.New(
				errs.M("%s: a nil parameter isn't allowed (index %d)", op, i),
				errs.C(errorClass, errs.EmptyNotAllowed))
		}

		if _, ok := dst[def.Name()]; ok {
			return errs.New(
				errs.M("%s: duplicate parameter name %q", op, def.Name()),
				errs.C(errorClass, errs.DuplicateObject))
		}

		inst, err := instantiate(def.Name(), &def.ItemAwareElement)
		if err != nil {
			return errs.New(
				errs.M("%s: couldn't instantiate parameter %q",
					op, def.Name()),
				errs.C(errorClass, errs.OperationFailed),
				errs.E(err))
		}

		dst[def.Name()] = inst
	}

	return nil
}

// instantiate builds a fresh per-frame instance of a definition: same
// identity (element and ItemDefinition ids), independent value (the clobber
// surface of shared in-place mutation ends here).
func instantiate(
	name string,
	src *data.ItemAwareElement,
) (*data.Parameter, error) {
	iae, err := src.Clone()
	if err != nil {
		return nil, err
	}

	return data.NewParameter(name, iae)
}

// paramList snapshots a parameter map into a slice.
func paramList(m map[string]*data.Parameter) []*data.Parameter {
	pp := make([]*data.Parameter, 0, len(m))

	for _, p := range m {
		pp = append(pp, p)
	}

	return pp
}
