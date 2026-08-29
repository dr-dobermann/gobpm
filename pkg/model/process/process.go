// Package process provides an implementation of BPMN Process elements and their execution.
package process

import (
	"errors"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/artifacts"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	dataobjects "github.com/dr-dobermann/gobpm/pkg/model/data_objects"
	datastores "github.com/dr-dobermann/gobpm/pkg/model/data_stores"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	hi "github.com/dr-dobermann/gobpm/pkg/model/hinteraction"
	"github.com/dr-dobermann/gobpm/pkg/model/lanes"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

const errorClass = "PROCESS_ERRORS"

// A Process describes a sequence or flow of Activities in an organization with
// the objective of carrying out work. In BPMN a Process is depicted as a graph
// of Flow Elements, which are a set of Activities, Events, Gateways, and
// Sequence Flows that define finite execution semantics. Processes can be
// defined at any level from enterprise-wide Processes to Processes performed
// by a single person. Low-level Processes can be grouped together to achieve a
// common business goal.
type Process struct {
	properties    map[string]*data.Property
	roles         map[string]*hi.ResourceRole
	nodes         map[string]flow.Node
	flows         map[string]*flow.SequenceFlow
	dataObjects   map[string]*dataobjects.DataObject
	dataStoreRefs map[string]*datastores.DataStoreReference
	laneSets      []*lanes.LaneSet
	// artifacts is ordered, not keyed: artifacts have no name, and
	// declaration order is what a round-trip reproduces (ADR-039).
	artifacts []artifacts.Artifact
	// ioSpec is the process's declared I/O contract (ADR-040 §2.1): one
	// input set, one output set, nil when the process declares none — the
	// permissive, contract-less process (§2.5).
	ioSpec *data.InputOutputSpecification
	name   string
	foundation.BaseElement
	CorrelationSubscriptions []*bpmncommon.CorrelationSubscription
}

// New creates a new Process and returns its pointer.
// Available options:
//
//	activities.WithRoles
//	data.WithProperties
//	data.WithInputs / data.WithOutputs
//	lanes.WithLaneSets
//	foundation.WithID
//	foundation.WithDoc
func New(
	name string,
	procOpts ...options.Option,
) (*Process, error) {
	pc := processConfig{
		name:     strings.Trim(name, " "),
		props:    map[string]*data.Property{},
		roles:    map[string]*hi.ResourceRole{},
		baseOpts: []options.Option{},
	}

	ee := []error{}

	addErr := func(err error) {
		if err != nil {
			ee = append(ee, err)
		}
	}

	for _, po := range procOpts {
		switch opt := po.(type) {
		case activities.RoleOption: // *processConfig implements RoleConfigurator
			addErr(opt(&pc))

		case data.PropertyOption: // *processConfig implements data.PropertyAdder
			addErr(opt(&pc))

		case lanes.LaneSetOption: // *processConfig implements lanes.LaneSetAdder
			addErr(opt(&pc))

		case data.IOSpecOption: // *processConfig implements data.IOSpecAdder
			addErr(opt(&pc))

		case foundation.BaseOption:
			pc.baseOpts = append(pc.baseOpts, opt)

		default:
			ee = append(ee,
				errs.New(
					errs.M("invalid option type for Process"),
					errs.C(errorClass, errs.BulidingFailed,
						errs.TypeCastingError),
					errs.D("option_type", reflect.TypeOf(opt).String())))
		}
	}

	if len(ee) > 0 {
		return nil, errors.Join(ee...)
	}

	return pc.newProcess()
}

// Name returns the process name.
func (p *Process) Name() string {
	return p.name
}

// Roles returns the resource roles declared on the Process itself
// (BPMN Table 10.1 — the resources responsible for the Process, as distinct
// from an Activity's own). They are carried and validated, but contribute to no
// task's eligibility (ADR-020 v.3 §2.5.4).
func (p *Process) Roles() []*hi.ResourceRole {
	return slices.Collect(maps.Values(p.roles))
}

// LaneSets returns a copy of the Process's lane sets, in declaration order.
// Lanes are carried and never executed (SRD-076).
func (p *Process) LaneSets() []*lanes.LaneSet {
	return slices.Clone(p.laneSets)
}

// IOSpec returns the process's declared I/O contract (ADR-040), or nil when
// the process declares none — the permissive, contract-less process that
// accepts whatever its caller delivers (ADR-040 §2.5).
func (p *Process) IOSpec() *data.InputOutputSpecification {
	return p.ioSpec
}

// AddArtifacts attaches artifacts to the process. Artifacts are model-only
// carriers (ADR-039): held for BPMN loading, never executed, never cloned
// into an instance. A nil artifact and a duplicate id are refused.
func (p *Process) AddArtifacts(arts ...artifacts.Artifact) error {
	aa, err := artifacts.Append(p.artifacts, arts...)
	if err != nil {
		return errs.New(
			errs.M("Process %q: adding artifacts failed", p.name),
			errs.C(errorClass, errs.BulidingFailed),
			errs.E(err))
	}

	p.artifacts = aa

	return nil
}

// Artifacts returns a copy of the process's artifact collection, in the
// order the artifacts were added.
func (p *Process) Artifacts() []artifacts.Artifact {
	return slices.Clone(p.artifacts)
}

// Properties returns the Process properties.
func (p *Process) Properties() []*data.Property {
	return slices.Collect(maps.Values(p.properties))
}

// DataObjects returns the Process-level Data Objects (SRD-063 FR-1).
func (p *Process) DataObjects() []*dataobjects.DataObject {
	return slices.Collect(maps.Values(p.dataObjects))
}

// addDataObject registers a Data Object on the Process (SRD-063 FR-1). A
// DataObject is a scope-resident named container (ADR-030 §2.1), so its name
// must be unique among the Process's Data Objects and must not collide with a
// Property — they share one scope name-space.
func (p *Process) addDataObject(do *dataobjects.DataObject) error {
	name := do.Name()
	if _, ok := p.dataObjects[name]; ok {
		return errs.New(
			errs.M("data object %q(%s) already registered in process",
				name, do.ID()),
			errs.C(errorClass, errs.DuplicateObject))
	}

	if _, ok := p.properties[name]; ok {
		return errs.New(
			errs.M("data object name %q collides with a process property",
				name),
			errs.C(errorClass, errs.DuplicateObject))
	}

	p.dataObjects[name] = do

	return do.BindTo(p)
}

// DataStoreReferences returns the Process-level Data Store References
// (SRD-068 FR-3).
func (p *Process) DataStoreReferences() []*datastores.DataStoreReference {
	return slices.Collect(maps.Values(p.dataStoreRefs))
}

// addDataStoreRef registers a Data Store Reference on the Process (SRD-068
// FR-3). A reference is a flow-scope handle to an engine-global store — it is
// NOT seeded into scope (its data lives in the engine registry), so its name
// need only be unique among the Process's references.
func (p *Process) addDataStoreRef(r *datastores.DataStoreReference) error {
	name := r.Name()
	if _, ok := p.dataStoreRefs[name]; ok {
		return errs.New(
			errs.M("data store reference %q(%s) already registered in process",
				name, r.ID()),
			errs.C(errorClass, errs.DuplicateObject))
	}

	p.dataStoreRefs[name] = r

	return r.BindTo(p)
}

// addNode adds non-empty unique BaseNode n to the process p.
func (p *Process) addNode(n flow.Node) error {
	if _, ok := p.nodes[n.ID()]; ok {
		return errs.New(
			errs.M("node %q(%s) already registered in process.",
				n.Name(), n.ID()),
			errs.C(errorClass, errs.DuplicateObject))
	}

	p.nodes[n.ID()] = n

	return n.BindTo(p)
}

// Nodes returns a slice of Process flow.BaseNodes of one of types.
// if types aren't specified then all nodes returned.
func (p *Process) Nodes(types ...flow.NodeType) []flow.Node {
	if err := flow.ValidateNodeTypes(types...); err != nil {
		return []flow.Node{}
	}

	fnn := make([]flow.Node, 0, len(p.nodes))
	for _, n := range p.nodes {
		if len(types) == 0 || has(types, n.NodeType()) {
			fnn = append(fnn, n)
		}
	}

	return fnn
}

// has checks if slice has item.
func has[T comparable](slice []T, item T) bool {
	for _, i := range slice {
		if i == item {
			return true
		}
	}

	return false
}

// addFlow add non-empty unique SequenceFlow into the Process.
func (p *Process) addFlow(f *flow.SequenceFlow) error {
	if _, ok := p.flows[f.ID()]; ok {
		return errs.New(
			errs.M("flow %q already exists in the process %q",
				f.ID(), p.ID()),
			errs.C(errorClass, errs.DuplicateObject))
	}

	p.flows[f.ID()] = f

	return f.BindTo(p)
}

// Flows returns all processes flows.
func (p *Process) Flows() []*flow.SequenceFlow {
	ff := make([]*flow.SequenceFlow, 0, len(p.flows))

	for _, f := range p.flows {
		ff = append(ff, f)
	}

	return ff
}

// ---------------------- flow.Container interface -----------------------------

// Add adds new Element into the Process p.
// On failure it returns an error.
func (p *Process) Add(e flow.Element) error {
	if e == nil {
		return errs.New(
			errs.M("flow element couldn't be empty"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	switch e.EType() {
	case flow.NodeElement:
		n, ok := e.(flow.Node)
		if !ok {
			return errs.New(
				errs.M("element %q reports NodeElement type but is not a flow.Node",
					e.ID()),
				errs.C(errorClass, errs.TypeCastingError))
		}

		return p.addNode(n)

	case flow.SequenceBaseElement:
		f, ok := e.(*flow.SequenceFlow)
		if !ok {
			return errs.New(
				errs.M("element %q reports SequenceBaseElement type but is not a *flow.SequenceFlow",
					e.ID()),
				errs.C(errorClass, errs.TypeCastingError))
		}

		return p.addFlow(f)

	case flow.DataObjectElement:
		do, ok := e.(*dataobjects.DataObject)
		if !ok {
			return errs.New(
				errs.M("element %q reports DataObjectElement type but is not a *dataobjects.DataObject",
					e.ID()),
				errs.C(errorClass, errs.TypeCastingError))
		}

		return p.addDataObject(do)

	case flow.DataStoreReferenceElement:
		r, ok := e.(*datastores.DataStoreReference)
		if !ok {
			return errs.New(
				errs.M("element %q reports DataStoreReferenceElement type but is not a *datastores.DataStoreReference",
					e.ID()),
				errs.C(errorClass, errs.TypeCastingError))
		}

		return p.addDataStoreRef(r)
	}

	return errs.New(
		errs.M("invalid flow element type: %s", reflect.TypeOf(e).String()),
		errs.C(errorClass, errs.InvalidParameter))
}

// Validate checks that the Process is a well-formed flow graph: every sequence
// flow connects a source and a target node that belong to the Process. It is
// run at registration (snapshot.New) so a malformed process fails with a clear
// error before a snapshot is built, rather than producing a broken snapshot.
//
// Validate does not re-check BPMN element completeness (the "an EndEvent
// requires a StartEvent" rule the snapshot already enforces). Flow endpoints
// are guaranteed non-nil by flow.Link, so only their membership in the Process
// is checked here.
func (p *Process) Validate() error {
	ee := []error{}

	for id, f := range p.flows {
		if _, ok := p.nodes[f.Source().ID()]; !ok {
			ee = append(ee,
				errs.New(
					errs.M("source %q of sequence flow %q is not in the process",
						f.Source().ID(), id),
					errs.C(errorClass, errs.ObjectNotFound)))
		}

		if _, ok := p.nodes[f.Target().ID()]; !ok {
			ee = append(ee,
				errs.New(
					errs.M("target %q of sequence flow %q is not in the process",
						f.Target().ID(), id),
					errs.C(errorClass, errs.ObjectNotFound)))
		}
	}

	// Per-node self-validation (once the flows are linked): a node that needs to
	// check its configuration against its incoming/outgoing flows — e.g. a Complex
	// gateway's activation threshold against its incoming-flow count (ADR-005 v.3
	// §2.11) — implements Validate() and is checked here at registration.
	for _, n := range p.nodes {
		v, ok := n.(interface{ Validate() error })
		if !ok {
			continue
		}

		if err := v.Validate(); err != nil {
			ee = append(ee, err)
		}
	}

	p.validateTopLevelStarts(&ee)

	// A Cancel End Event is invalid at the top level (BPMN §10.7 — "MAY NOT be
	// used in any other type of Sub-Process or Process"; ADR-028 §2.6): a Process
	// is never a Transaction, so it passes isTransaction=false.
	if err := events.ValidateCancelEndPlacement(p.Nodes(), false); err != nil {
		ee = append(ee, err)
	}

	// Link pairing (ADR-006 v.4 §2.8, SRD-057 §3.3): every Link name in this
	// top-level container must have exactly one target catch and ≥1 source
	// throw. A nested Sub-Process validates its own Link namespace via its own
	// Validate hook (above), so this pass is single-level by construction.
	if err := events.ValidateLinkPairing(p.Nodes()); err != nil {
		ee = append(ee, err)
	}

	// A compensation handler lives outside the normal flow (SRD-059 FR-2).
	if err := activities.ValidateCompensationPlacement(p.Nodes()); err != nil {
		ee = append(ee, err)
	}

	// An authorizing role cannot name its people through a directory the engine
	// doesn't have (ADR-020 v.3 §2.5.4, SRD-075 FR-5). The Process passes its
	// own roles too — it is not one of its own nodes.
	if err := activities.ValidateResourceRoles(
		p.Nodes(), p.Roles()); err != nil {
		ee = append(ee, err)
	}

	// A lane may only place nodes of its own container (SRD-076 FR-8). Lanes
	// attach no behavior, so this is the only place the engine reads one.
	if err := lanes.ValidateLaneSets(p.laneSets, p.Nodes()); err != nil {
		ee = append(ee, err)
	}

	// The declared I/O contract shares the root scope's one namespace with
	// the properties and data objects (ADR-040 §2.6), and the two directions
	// share it with each other.
	if err := p.validateIONames(); err != nil {
		ee = append(ee, err)
	}

	if len(ee) > 0 {
		return errors.Join(ee...)
	}

	return nil
}

// validateIONames refuses a declared parameter whose name collides with a
// property, a data object, or a parameter of the other direction (ADR-040
// §2.6 — one namespace), after the specification's own per-direction check.
func (p *Process) validateIONames() error {
	if p.ioSpec == nil {
		return nil
	}

	if err := p.ioSpec.Validate(); err != nil {
		return err
	}

	ee := []error{}
	seen := map[string]data.Direction{}

	for _, dir := range []data.Direction{data.Input, data.Output} {
		// Parameters can't fail here: the direction is one of the two the
		// specification holds, said in the form the coverage gate reads.
		params, err := p.ioSpec.Parameters(dir)
		if err != nil {
			return errs.Invariant("parameters of %q: %w", dir, err)
		}

		for _, param := range params {
			name := param.Name()

			switch {
			case p.properties[name] != nil:
				ee = append(ee, ioClash(name, dir, "a process property"))

			case p.dataObjects[name] != nil:
				ee = append(ee, ioClash(name, dir, "a data object"))

			case seen[name] != "":
				ee = append(ee, ioClash(name, dir,
					"the "+string(seen[name])+" parameter of the same name"))

			default:
				seen[name] = dir
			}
		}
	}

	return errors.Join(ee...)
}

// ioClash words the one-namespace refusal.
func ioClash(name string, dir data.Direction, with string) error {
	return errs.New(
		errs.M("%s parameter %q collides with %s: the root scope has one "+
			"namespace (ADR-040 §2.6)", dir, name, with),
		errs.C(errorClass, errs.DuplicateObject))
}

// validateTopLevelStarts rejects a Conditional trigger on a top-level Start Event
// (ADR-006 v.3 §2.7): a Process is the TOP-LEVEL container, and BPMN Table 10.84
// forbids the condition to reference process data — the engine exposes no legal
// static-attribute surface. The StartEvent itself stays constructible with it
// (context-free), because the same type is the event-sub-process start, where the
// condition legally reads the enclosing scope (§10.4.3).
func (p *Process) validateTopLevelStarts(ee *[]error) {
	for _, n := range p.nodes {
		en, ok := n.(flow.EventNode)
		if !ok || en.EventClass() != flow.StartEventClass {
			continue
		}

		for _, d := range en.Definitions() {
			if d.Type() != flow.TriggerConditional {
				continue
			}

			*ee = append(*ee, errs.New(
				errs.M("a Conditional trigger isn't supported on a "+
					"top-level Start Event (it arrives with event "+
					"Sub-Processes)"),
				errs.C(errorClass, errs.InvalidObject),
				errs.D(observability.AttrNodeID, en.ID()),
				errs.D(observability.AttrNodeName, en.Name())))
		}
	}
}

// Elements returns all processes elements.
func (p *Process) Elements() []flow.Element {
	fee := make([]flow.Element, 0, len(p.nodes)+len(p.flows))

	for _, n := range p.nodes {
		// flow.Node embeds flow.Element, so no assertion is involved.
		fee = append(fee, n)
	}

	for _, f := range p.flows {
		fee = append(fee, f)
	}

	return fee
}

// Remove deletes single flow or node for the Process p.
func (p *Process) Remove(e flow.Element) error {
	if e == nil {
		return errs.New(
			errs.M("element couldn't be empty"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if _, ok := p.nodes[e.ID()]; ok {
		delete(p.nodes, e.ID())

		return e.Unbind()
	}

	if _, ok := p.flows[e.ID()]; ok {
		delete(p.flows, e.ID())

		return e.Unbind()
	}

	return errs.New(
		errs.M("element %q(%s) not found in process", e.Name(), e.ID()),
		errs.C(errorClass, errs.ObjectNotFound))
}

// ------------------------------------------------------------------------------
var _ flow.Container = (*Process)(nil)
