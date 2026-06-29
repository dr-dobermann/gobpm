package snapshot

import (
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// InstantiatingStart is an engine-agnostic descriptor of one instantiating start
// trigger discovered in a snapshot: the start node a new instance runs from, the
// message or signal definition that fires it, and the correlation key it declares
// (nil for a signal, or a name-match-only message). It deliberately carries no
// engine binding (no *Thresher, no starter id) so it can live on the immutable,
// engine-agnostic Snapshot (ADR-019 §2.3); the thresher wraps each descriptor into
// an instanceStarter at registration.
type InstantiatingStart struct {
	StartNode      flow.Node
	EventDef       flow.EventDefinition // message (optionally correlated) or signal
	CorrelationKey *bpmncommon.CorrelationKey
}

// discoverInstantiatingStarts walks the snapshot's cloned node graph once and
// builds an InstantiatingStart for every instantiating start trigger: a StartEvent
// (or instantiate ReceiveTask) carrying a message or signal definition with no
// incoming sequence flow (BPMN §13.2 / §13.5.1; signals SRD-026). It is computed
// in New after wireClonedGraph (so each clone's Incoming() is populated) and stored
// on the snapshot, replacing a second O(nodes) re-scan at registration. The arm and
// correlation-key logic mirrors what the thresher's starter setup used to do — only
// the engine binding (the *Thresher and a fresh id) is left for the caller.
func discoverInstantiatingStarts(nodes map[string]flow.Node) []InstantiatingStart {
	var starts []InstantiatingStart

	for _, n := range nodes {
		if len(n.Incoming()) != 0 || !isInstantiatingStartNode(n) {
			continue
		}

		en, ok := n.(flow.EventNode)
		if !ok {
			continue
		}

		// An instantiating Event-Based gateway exposes its arms' definitions as its
		// union (SRD-024), detected structurally so the snapshot stays free of the
		// gateways package. Exclusive-start: each fired arm's instance runs from that
		// ARM's continuation, so the arm is the start node (SRD-025 §4.2). Parallel-start:
		// the instance is born from the GATE (seedParallelStart pre-fires the firing arm,
		// arms the others), so the gate stays the start node and the arms share the
		// gate's CorrelationKey, so resolveAndLaunch dedups to one instance (§4.3).
		router, isGate := n.(interface {
			ArmFor(flow.EventDefinition) (flow.Node, bool)
		})

		parallel := false
		if ps, ok := n.(interface{ ParallelStart() bool }); ok {
			parallel = ps.ParallelStart()
		}

		for _, eDef := range en.Definitions() {
			// A start trigger backs an instance-starter when it is a message
			// (point-to-point, optionally correlated) or a signal (broadcast, never
			// correlated — BPMN §10.5.7, SRD-026). Other kinds don't instantiate here.
			var corrKey *bpmncommon.CorrelationKey
			switch eDef.(type) {
			case *events.MessageEventDefinition:
				corrKey = correlationKeyOf(n)
			case *events.SignalEventDefinition:
				// signals carry no correlation — corrKey stays nil
			default:
				continue
			}

			startNode := n
			if isGate {
				if arm, ok := router.ArmFor(eDef); ok && !parallel {
					startNode = arm
					corrKey = correlationKeyOf(arm)
				}
			}

			starts = append(starts, InstantiatingStart{
				StartNode:      startNode,
				EventDef:       eDef,
				CorrelationKey: corrKey,
			})
		}
	}

	return starts
}

// isInstantiatingStartNode reports whether n is an instantiating start trigger:
// a StartEvent, or an instantiate ReceiveTask (BPMN §13.2 / §13.3.3 / §13.5.1).
// The incoming-flow check is the caller's. The ReceiveTask is matched structurally
// (Instantiate() bool) to avoid coupling the snapshot to the activities package.
func isInstantiatingStartNode(n flow.Node) bool {
	if en, ok := n.(flow.EventNode); ok &&
		en.EventClass() == flow.StartEventClass {
		return true
	}

	if rt, ok := n.(interface{ Instantiate() bool }); ok && rt.Instantiate() {
		return true
	}

	return false
}

// correlationKeyOf returns the CorrelationKey a start node declares, read
// structurally so the snapshot needn't depend on the concrete node package
// (StartEvent / instantiate ReceiveTask both expose CorrelationKey()). nil =
// name-match only.
func correlationKeyOf(n flow.Node) *bpmncommon.CorrelationKey {
	if ck, ok := n.(interface {
		CorrelationKey() *bpmncommon.CorrelationKey
	}); ok {
		return ck.CorrelationKey()
	}

	return nil
}
