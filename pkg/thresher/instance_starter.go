package thresher

import (
	"context"

	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/pkg/eventproc"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// instanceStarter is the definition-level collaborator that turns an
// event-triggered start (a message StartEvent today; an instantiate ReceiveTask
// in M4) into a new process instance (ADR-015 §2.2). It is registered on the
// engine EventHub as a persistent EventProcessor — one per instantiating start
// trigger of a process — so it fires for every matching message and is never
// removed after a single fire. It owns no Instance state; on a fired event it
// asks the Thresher to launch a new instance born from that event.
type instanceStarter struct {
	thr       *Thresher
	snapshot  *snapshot.Snapshot
	startNode flow.Node
	eDef      *events.MessageEventDefinition
	id        string
}

// ID returns the starter id (a fresh foundation id, distinct from any node or
// instance id).
func (s *instanceStarter) ID() string {
	return s.id
}

// ProcessEvent is the EventProcessor entry point invoked by the persistent
// waiter on a matching message: it asks the Thresher to launch a new instance
// born from the event, carrying the message payload onto the start node.
func (s *instanceStarter) ProcessEvent(
	ctx context.Context,
	eDef flow.EventDefinition,
) error {
	return s.thr.launchInstanceFromEvent(ctx, s.snapshot, s.startNode, eDef)
}

// scanInstantiatingStarts walks a process snapshot and builds an instanceStarter
// for every instantiating start trigger: a StartEvent that carries a
// MessageEventDefinition and has no incoming sequence flow (§13.2 / §13.5.1).
// The instantiate ReceiveTask is added in M4 (FR-4). It builds the starters but
// does not register them — the Thresher decides when (auto mode, at the later of
// RegisterProcess/Run) and whether (manual mode registers none, FR-9).
//
// It is an unexported helper called only by RegisterProcess with a freshly built
// snapshot and the live Thresher, so it takes both as guaranteed non-nil.
func scanInstantiatingStarts(s *snapshot.Snapshot, thr *Thresher) []*instanceStarter {
	var starters []*instanceStarter

	for _, n := range s.Nodes {
		en, ok := n.(flow.EventNode)
		if !ok || en.EventClass() != flow.StartEventClass {
			continue
		}

		if len(n.Incoming()) != 0 {
			continue
		}

		for _, eDef := range en.Definitions() {
			med, ok := eDef.(*events.MessageEventDefinition)
			if !ok {
				continue
			}

			starters = append(starters, &instanceStarter{
				thr:       thr,
				snapshot:  s,
				startNode: n,
				eDef:      med,
				id:        foundation.GenerateID(),
			})
		}
	}

	return starters
}

// interface check
var _ eventproc.EventProcessor = (*instanceStarter)(nil)
