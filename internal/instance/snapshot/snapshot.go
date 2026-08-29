// Package snapshot provides process instance snapshot functionality.
package snapshot

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	dataobjects "github.com/dr-dobermann/gobpm/pkg/model/data_objects"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/tasks"
)

const errorClass = "SNAPSHOT_ERRORS"

// Snapshot holds process'es snapshot ready to run.
type Snapshot struct {
	foundation.ID

	ProcessID   string
	ProcessName string
	Nodes       map[string]flow.Node
	Flows       map[string]*flow.SequenceFlow
	Properties  []*data.Property
	// DataObjects are the Process-level Data Objects, cloned per instance so no
	// two instances share DataObject state (SRD-063 FR-2). Seeded into the root
	// scope at instance start; their task associations are re-pointed at these
	// clones by wireClonedDataObjects.
	DataObjects []*dataobjects.DataObject
	// IOSpec is the process's declared I/O contract (ADR-040), nil for a
	// contract-less process. Shared by reference across clones: a
	// declaration whose parameters are per-execution templates
	// (ADR-010 §2.3), not per-instance state.
	IOSpec *data.InputOutputSpecification
	// CorrelationKeys are the process's declared correlation keys (the Key of
	// each CorrelationSubscription). An in-instance receiver derives them from
	// an incoming message's payload to grow the instance's conversation key-set
	// (lazy association — SRD-017 §4.5). Immutable config, shared by Clone.
	CorrelationKeys []*bpmncommon.CorrelationKey
	// InstantiatingStarts are the process's instantiating start triggers
	// (message / signal StartEvents and instantiate ReceiveTasks), discovered
	// once by New after the graph is wired. The thresher wraps each into a
	// persistent instance-starter at registration instead of re-scanning the
	// node graph. Engine-agnostic descriptors only; immutable, shared by Clone.
	InstantiatingStarts []InstantiatingStart
	// Version is the registered process version this snapshot belongs to
	// (SRD-070 FR-1): the thresher stamps it at registration, clones carry
	// it, and a checkpoint pins (ProcessID, Version) so recovery re-clones
	// exactly the version the instance started from. Zero means
	// "unregistered" (a snapshot built outside the registry).
	Version int

	// HasConditionals reports whether any node carries a Conditional event
	// definition (catch, boundary, or event-based-gateway arm), precomputed
	// once by New (SRD-048): a track emits the commit-diff signal to the
	// instance loop only when true, so a conditional-free process never pays
	// for it. Immutable, shared by Clone.
	HasConditionals bool
}

// cloneProcessData deep-copies a process's properties and data objects for the
// snapshot template (FIX-016; SRD-063 FR-2), so the frozen snapshot owns private
// instances a later process edit can't reach.
func cloneProcessData(
	p *process.Process,
) ([]*data.Property, []*dataobjects.DataObject, error) {
	// CloneProperties / CloneDataObjects already wrap with per-element context,
	// so propagate as-is (these failures are defensive — a value-less element is
	// rejected at construction).
	props, err := data.CloneProperties(p.Properties())
	if err != nil {
		return nil, nil, err
	}

	dobjs, err := dataobjects.CloneDataObjects(p.DataObjects())
	if err != nil {
		return nil, nil, err
	}

	return props, dobjs, nil
}

// New creates a new snapshot from the Process p and returns its
// pointer on success or error on failure.
func New(
	p *process.Process,
	_ ...options.Option,
) (*Snapshot, error) {
	if p == nil {
		return nil,
			errs.New(
				errs.M("process is empty"),
				errs.C(errorClass, errs.EmptyNotAllowed))
	}

	// validate the process graph before taking the snapshot, so a malformed
	// process (a sequence flow whose source or target is not in the process)
	// is rejected at registration rather than producing a broken snapshot.
	if err := p.Validate(); err != nil {
		return nil, err
	}

	// Clone the process properties and data objects into the snapshot so the
	// frozen template owns private instances — like the node graph cloned below
	// — and a process edit after registration can't reach the registered version
	// (FIX-016, ADR-019 §2.3; SRD-063 FR-2 for data objects).
	props, dobjs, err := cloneProcessData(p)
	if err != nil {
		return nil, err
	}

	s := Snapshot{
		ID:          *foundation.NewID(),
		ProcessID:   p.ID(),
		ProcessName: p.Name(),
		Nodes:       map[string]flow.Node{},
		Properties:  props,
		DataObjects: dobjs,
		IOSpec:      p.IOSpec(),
	}

	s.CorrelationKeys = correlationKeys(p)

	seExists := false
	eeExists := false
	instStartExists := false

	// srcNodes keeps the original process nodes for wireClonedGraph's boundary
	// rebind; s.Nodes gets their clones so the snapshot owns an isolated graph
	// (ADR-019 §2.3) — edits to the process after registration can't reach it.
	srcNodes := make(map[string]flow.Node, len(p.Nodes()))

	for _, n := range p.Nodes() {
		srcNodes[n.ID()] = n

		cn, cerr := cloneAfterCheck(n)
		if cerr != nil {
			return nil, cerr
		}

		s.Nodes[n.ID()] = cn

		// An instantiate ReceiveTask with no incoming flow is a valid process
		// instantiation point on its own (BPMN §13.3.3) — the task-shaped peer
		// of a message start event.
		if isInstantiatingTask(n) {
			instStartExists = true
		}

		// check events
		if n.NodeType() == flow.EventNodeType {
			en, ok := n.(flow.EventNode)
			if !ok {
				return nil,
					errs.New(
						errs.M("failed to convert to EventNode"),
						errs.C(errorClass, errs.TypeCastingError),
						errs.D(observability.AttrNodeID, n.ID()),
						errs.D(observability.AttrNodeName, n.Name()))
			}

			switch en.EventClass() {
			case flow.StartEventClass:
				seExists = true

			case flow.IntermediateEventClass:
				break

			case flow.EndEventClass:
				eeExists = true
			}
		}
	}

	// BPMN requires that, if there is an EndEvent, the process has an
	// instantiation point: a StartEvent or a no-incoming instantiate
	// ReceiveTask (§13.3.3).
	if !hasInstantiationPoint(seExists, eeExists, instStartExists) {
		return nil,
			errs.New(
				errs.M("no StartEvent or instantiating ReceiveTask in process " +
					"with an EndEvent"))
	}

	srcFlows := make(map[string]*flow.SequenceFlow, len(p.Flows()))
	for _, f := range p.Flows() {
		srcFlows[f.ID()] = f
	}

	// Wire the cloned graph the same way Clone does — relink flows between the
	// clones, remap default flows, rebind boundary events — so the snapshot is
	// born isolated in a single pass over the definition (SRD-031.A §3.3).
	flows, err := flow.WireClonedGraph(s.Nodes, srcNodes, srcFlows)
	if err != nil {
		return nil, err
	}

	s.Flows = flows

	// With the graph wired (each clone's Incoming() now populated), discover the
	// instantiating start triggers once and store them, so registration wraps the
	// descriptors into starters instead of re-walking the node graph.
	s.InstantiatingStarts = discoverInstantiatingStarts(s.Nodes)
	s.HasConditionals = hasConditionals(s.Nodes)

	return &s, nil
}

// hasConditionals reports whether any event node — an intermediate catch, a
// boundary event, or an event-based-gateway arm — carries a Conditional
// definition at ANY nesting depth (SRD-048; deep per SRD-049 FR-5: an inner
// conditional must arm too). One walk at snapshot build; the flag gates the
// per-commit evDataCommit emit.
func hasConditionals(nodes map[string]flow.Node) bool {
	found := false

	walkNodesDeep(nodes, func(n flow.Node) bool {
		en, ok := n.(flow.EventNode)
		if !ok {
			return true
		}

		for _, d := range en.Definitions() {
			if d.Type() == flow.TriggerConditional {
				found = true

				return false
			}
		}

		return true
	})

	return found
}

// nodeLister is the container capability the deep walk descends through —
// the ElementsContainer surface a composite node (a Sub-Process) embeds.
type nodeLister interface {
	Nodes() []flow.Node
}

// NodeByID resolves a node anywhere in the snapshot's graph — the
// top-level index first, then a deep walk into nested containers: a
// mid-composite checkpoint records INNER nodes, which the top-level
// index does not carry (SRD-082 FR-5).
func (s *Snapshot) NodeByID(id string) (flow.Node, bool) {
	if n, ok := s.Nodes[id]; ok {
		return n, true
	}

	var found flow.Node

	walkNodesDeep(s.Nodes, func(n flow.Node) bool {
		if n.ID() == id {
			found = n

			return false
		}

		return true
	})

	return found, found != nil
}

// Walk visits every node of the snapshot's graph and, recursively, of every
// nested container, returning false if visit stopped the walk early.
//
// It is the exported face of the same deep traversal NodeByID uses, for
// callers that must inspect ALL nodes rather than find one — registration-time
// model validation, which has to reach a Script Task nested in a Sub-Process
// just as surely as one at the top level.
func (s *Snapshot) Walk(visit func(flow.Node) bool) bool {
	return walkNodesDeep(s.Nodes, visit)
}

// walkNodesDeep visits every node of the graph and, recursively, of every
// nested container (SRD-049 FR-5). visit returning false stops the walk.
func walkNodesDeep(nodes map[string]flow.Node, visit func(flow.Node) bool) bool {
	for _, n := range nodes {
		if !visit(n) {
			return false
		}

		if nl, ok := n.(nodeLister); ok {
			inner := nl.Nodes()
			m := make(map[string]flow.Node, len(inner))

			for _, in := range inner {
				m[in.ID()] = in
			}

			if !walkNodesDeep(m, visit) {
				return false
			}
		}
	}

	return true
}

// correlationKeys extracts the process's declared correlation keys — the Key of
// each non-nil CorrelationSubscription — for the snapshot (SRD-017 §4.5). An
// in-instance receiver derives these from an incoming message to grow its
// conversation key-set.
func correlationKeys(p *process.Process) []*bpmncommon.CorrelationKey {
	keys := make([]*bpmncommon.CorrelationKey, 0, len(p.CorrelationSubscriptions))

	for _, cs := range p.CorrelationSubscriptions {
		if cs != nil && cs.Key != nil {
			keys = append(keys, cs.Key)
		}
	}

	return keys
}

// cloneAfterCheck validates the node for the snapshot and returns its
// clone. The only check here is the iterated-waiting-leaf refusal
// (#313); everything else the snapshot rejects is structural and
// belongs to the whole-process passes below.
func cloneAfterCheck(n flow.Node) (flow.Node, error) {
	// A WAITING leaf under Multi-Instance or a Standard Loop is refused
	// until the iteration decorator owns the node's event registration
	// (#313): the engine would otherwise run the iteration without ever
	// waiting past its first pass.
	if err := checkUncorrelatedParallelMessage(n); err != nil {
		return nil, err
	}

	return cloneNode(n)
}

// checkUncorrelatedParallelMessage refuses the ONE iterated waiting shape
// that is ambiguous in the model rather than merely unimplemented: a PARALLEL
// Multi-Instance over a Message catch that declares no iteration correlation
// (SRD-090.B FR-4, ADR-006 §2.9.5).
//
// A Message is point-to-point — one envelope reaches one receiver (ADR-006
// §2.9.2) — so N instances waiting at once need something that says which
// envelope belongs to which. With a declared key the decorator routes by it;
// without one, any choice is arbitrary. "The first still waiting in ordinal
// order" is a defensible rule for a SEQUENTIAL iteration, where exactly one
// instance waits at a time and each pass consumes one envelope, and it is a
// coin toss where N wait together.
//
// Every other iterated waiting shape now builds: a sequential Multi-Instance,
// a Standard Loop, and a parallel Multi-Instance over a Signal, Timer or
// Conditional — those fan out to every waiting instance, which is their own
// semantics rather than a fallback.
//
// This replaces the blanket refusal of any activity that both iterates and
// waits. That guard existed because a wait was armed on ARRIVAL and an
// in-place iteration never re-arrives, so passes after the first ran without
// waiting; the decorator owning the subscription removes the arrival
// dependency entirely (SRD-090.B FR-1/FR-2), so the construct is correct and
// there is nothing left to refuse but the ambiguity.
func checkUncorrelatedParallelMessage(n flow.Node) error {
	if _, ok := parallelMultiInstance(n); !ok {
		return nil
	}

	// A CAPABILITY-PARKED FAN-OUT is one step from working, and the reason it
	// is still refused is now narrow.
	//
	// Its instances DO classify and park their own nodes, mint their own
	// identities and announce one task each; a completion reaches the
	// instance that owns it. What is missing is RESTORE: the identities are
	// recorded per instance but a rehydrated fan-out does not take them back,
	// so a dehydration part-way through the approvals leaves every
	// outstanding task handle naming nothing.
	//
	// That matters more here than for most waits, because a human task is the
	// wait most likely to dehydrate — a fan-out of approvals is exactly the
	// case that sits idle for days. Refused until the identities survive the
	// round trip.
	// A PARALLEL fan-out over EXTERNAL-WORKER work still has one identity for
	// N instances (see parksOnWorker), so N of them would dispatch a single
	// job and the rest would complete without anyone doing the work — the
	// silent wrong answer this guard exists to prevent. It names the
	// sequential shape, which is correct: one instance dispatches at a time
	// and each pass is reported on its own.
	if parksOnWorker(n) {
		return errs.New(
			errs.M("activity %q is a PARALLEL Multi-Instance over an "+
				"external-worker Service Task: its instances would share one "+
				"job identity, so a single report would complete work nobody "+
				"performed. Make the Multi-Instance sequential — one instance "+
				"dispatches at a time there, and each pass is reported on its "+
				"own", n.Name()),
			errs.C(errorClass, errs.InvalidObject),
			errs.D(observability.AttrNodeID, n.ID()))
	}

	en, isEvent := n.(flow.EventNode)
	if !isEvent {
		return nil
	}

	if !hasMessageTrigger(en) || declaresIterationCorrelation(n) {
		return nil
	}

	return errs.New(
		errs.M("activity %q is a PARALLEL Multi-Instance over a Message "+
			"catch and declares no iteration correlation: with several "+
			"instances waiting at once, nothing says which message belongs "+
			"to which. Declare one with "+
			"activities.WithIterationCorrelation, or make the "+
			"Multi-Instance sequential — one instance waits at a time "+
			"there, so each pass consumes one message", n.Name()),
		errs.C(errorClass, errs.InvalidObject),
		errs.D(observability.AttrNodeID, n.ID()))
}

// parallelMultiInstance reports the node's Multi-Instance characteristics
// when it carries them AND they are parallel.
func parallelMultiInstance(
	n flow.Node,
) (*activities.MultiInstanceLoopCharacteristics, bool) {
	lch, ok := n.(interface {
		LoopCharacteristics() activities.LoopCharacteristics
	})
	if !ok {
		return nil, false
	}

	mi, isMI := lch.LoopCharacteristics().(*activities.MultiInstanceLoopCharacteristics)
	if !isMI || mi.IsSequential() {
		return nil, false
	}

	return mi, true
}

// hasMessageTrigger reports whether any of the node's definitions is a
// Message — the one trigger whose delivery is point-to-point.
func hasMessageTrigger(en flow.EventNode) bool {
	for _, d := range en.Definitions() {
		if d.Type() == flow.TriggerMessage {
			return true
		}
	}

	return false
}

// parksOnWorker reports whether executing this node parks by dispatching a job
// to an EXTERNAL WORKER — a wait addressed by a job identity rather than an
// event definition.
//
// A human task used to be here too, and no longer is: its identity is held per
// instance now, so a fan-out over one announces a task per instance and each is
// completed on its own. A worker job is still keyed to the TRACK — `ls.jobs`
// maps a job id to a track, with no ordinal — so N instances of one activity
// would share a single job and the rest would finish without anyone doing the
// work.
//
// A deferral, not a limit: the mechanism the human fan-out landed is what this
// path needs, one register down. Tracked by #355.
func parksOnWorker(n flow.Node) bool {
	ew, ok := n.(tasks.ExternalWorker)
	if !ok {
		return false
	}

	_, isWorker := ew.WorkerTopic()

	return isWorker
}

// declaresIterationCorrelation reports whether the node says how a
// concurrently-waiting iteration of it is addressed by an arriving message
// (SRD-085 FR-3) — the declaration that makes a parallel fan-out over a
// Message unambiguous.
func declaresIterationCorrelation(n flow.Node) bool {
	ic, ok := n.(interface {
		IterationCorrelation() (string, data.FormalExpression)
	})
	if !ok {
		return false
	}

	name, expr := ic.IterationCorrelation()

	return name != "" && expr != nil
}

// isInstantiatingTask reports whether n is a no-incoming instantiate
// ReceiveTask — a valid process instantiation point on its own (BPMN §13.3.3).
// Matched structurally to avoid coupling the snapshot to the activities package.
func isInstantiatingTask(n flow.Node) bool {
	rt, ok := n.(interface{ Instantiate() bool })

	return ok && rt.Instantiate() && len(n.Incoming()) == 0
}

// hasInstantiationPoint reports whether the process can be instantiated for
// BPMN's rule that a process containing an EndEvent must have an instantiation
// point — a StartEvent or a no-incoming instantiate ReceiveTask (§13.3.3).
func hasInstantiationPoint(seExists, eeExists, instStartExists bool) bool {
	return !eeExists || seExists || instStartExists
}

// cloneNode returns a clone of process node n, wrapping a clone failure with the
// node's identity. A node clone fails when a property is value-less and thus
// unclonable (FIX-017); a node without properties never errors.
func cloneNode(n flow.Node) (flow.Node, error) {
	cn, err := n.Clone()
	if err != nil {
		return nil, errs.New(
			errs.M("couldn't clone node %q", n.ID()),
			errs.C(errorClass, errs.BulidingFailed),
			errs.E(err))
	}

	return cn, nil
}

// Clone returns a per-instance copy of the Snapshot. Every node is cloned (its
// immutable configuration shared by reference, its runtime state fresh) and the
// flow graph is relinked between the clones, so an instance built from the clone
// mutates only its own nodes. Properties are cloned too — they carry per-instance
// mutable runtime state, so each instance owns its own (FIX-016). The genuinely
// immutable header — process id/name, correlation-key definitions and
// instantiating-start descriptors — is shared by reference. See ADR-009.
func (s *Snapshot) Clone() (*Snapshot, error) {
	props, err := data.CloneProperties(s.Properties)
	if err != nil {
		return nil, err
	}

	dobjs, err := dataobjects.CloneDataObjects(s.DataObjects)
	if err != nil {
		return nil, err
	}

	clone := Snapshot{
		ID:                  *foundation.NewID(),
		ProcessID:           s.ProcessID,
		ProcessName:         s.ProcessName,
		Nodes:               make(map[string]flow.Node, len(s.Nodes)),
		Properties:          props,
		DataObjects:         dobjs,
		IOSpec:              s.IOSpec,
		CorrelationKeys:     s.CorrelationKeys,
		InstantiatingStarts: s.InstantiatingStarts,
		HasConditionals:     s.HasConditionals,
		Version:             s.Version,
	}

	// Clone every node (its immutable configuration shared by reference, its
	// runtime state fresh); the clone starts with empty flows and any default
	// flow still points at the original edge until wireClonedGraph remaps it.
	for id, n := range s.Nodes {
		cn, cerr := cloneNode(n)
		if cerr != nil {
			return nil, cerr
		}

		clone.Nodes[id] = cn
	}

	flows, err := flow.WireClonedGraph(clone.Nodes, s.Nodes, s.Flows)
	if err != nil {
		return nil, err
	}

	clone.Flows = flows

	return &clone, nil
}
