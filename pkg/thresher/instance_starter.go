package thresher

import (
	"context"

	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/pkg/eventproc"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/msgflow"
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
	corrKey   *bpmncommon.CorrelationKey
	id        string
}

// ID returns the starter id (a fresh foundation id, distinct from any node or
// instance id).
func (s *instanceStarter) ID() string {
	return s.id
}

// ProcessEvent is the EventProcessor entry point invoked by the persistent
// waiter on a matching message: it derives the incoming message's correlation
// key from the payload (ADR-016 v.1 §2.2) and asks the Thresher to resolve
// create-or-route-or-join by that key (§2.3), launching a new instance born
// from the event when the key is unseen.
func (s *instanceStarter) ProcessEvent(
	ctx context.Context,
	eDef flow.EventDefinition,
) error {
	key, err := s.deriveKey(ctx, eDef)
	if err != nil {
		return err
	}

	return s.thr.resolveAndLaunch(ctx, s.snapshot, s.startNode, eDef, key)
}

// deriveKey computes the incoming message's composite correlation key from the
// fired event's payload, or "" when the starter declares no CorrelationKey (or
// the key can't be fully resolved — an underivable key correlates nothing, so
// the message instantiates without dedup, ADR-016 v.1 §2.2).
func (s *instanceStarter) deriveKey(
	ctx context.Context,
	eDef flow.EventDefinition,
) (string, error) {
	if s.corrKey == nil {
		return "", nil
	}

	var payload any
	if items := eDef.GetItemsList(); len(items) != 0 {
		payload = items[0].Structure().Get(ctx)
	}

	key, ok, err := msgflow.DeriveKey(ctx, s.thr.cfg.ExpressionEngine(),
		s.corrKey, s.eDef.Message(), payload)
	if err != nil {
		return "", err
	}

	if !ok {
		return "", nil
	}

	return key, nil
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
		if len(n.Incoming()) != 0 || !isInstantiatingStartNode(n) {
			continue
		}

		en, ok := n.(flow.EventNode)
		if !ok {
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
				corrKey:   correlationKeyOf(n),
				id:        foundation.GenerateID(),
			})
		}
	}

	return starters
}

// correlationKeyOf returns the CorrelationKey a start node declares, read
// structurally so the thresher needn't depend on the concrete node package
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

// isInstantiatingStartNode reports whether n is an instantiating start trigger:
// a message StartEvent, or an instantiate ReceiveTask (BPMN §13.2 / §13.3.3 /
// §13.5.1). The incoming-flow check is the caller's. The ReceiveTask is matched
// structurally (Instantiate() bool) to avoid coupling the thresher to the
// activities package.
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

// interface check
var _ eventproc.EventProcessor = (*instanceStarter)(nil)
