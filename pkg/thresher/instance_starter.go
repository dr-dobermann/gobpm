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
// event-triggered start (a message or signal StartEvent; an instantiate
// ReceiveTask) into a new process instance (ADR-015 §2.2; signals SRD-026 §4.2).
// It is registered on the engine EventHub as a persistent EventProcessor — one
// per instantiating start trigger of a process — so it fires for every matching
// message or signal broadcast and is never removed after a single fire. It owns
// no Instance state; on a fired event it asks the Thresher to launch a new
// instance born from that event.
type instanceStarter struct {
	thr       *Thresher
	snapshot  *snapshot.Snapshot
	startNode flow.Node
	eDef      flow.EventDefinition // message (optionally correlated) or signal (never)
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

	keyName := ""
	if s.corrKey != nil {
		keyName = s.corrKey.Name
	}

	s.thr.cfg.logger.Debug("instance-starter fired",
		"start_node_id", s.startNode.ID(),
		"event_definition_id", eDef.ID(),
		"key_name", keyName,
		"key", key)

	return s.thr.resolveAndLaunch(
		ctx, s.snapshot, s.startNode, eDef, keyName, key)
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

	// Reached only when corrKey != nil, i.e. a correlated message starter; a
	// signal starter has corrKey == nil and returned above. So eDef is a message
	// definition here.
	key, ok, err := msgflow.DeriveKey(ctx, s.thr.cfg.ExpressionEngine(),
		s.corrKey, s.eDef.(*events.MessageEventDefinition).Message(), payload)
	if err != nil {
		return "", err
	}

	if !ok {
		return "", nil
	}

	return key, nil
}

// scanInstantiatingStarts wraps each of the snapshot's precomputed instantiating
// start descriptors (snapshot.InstantiatingStarts, discovered once in
// snapshot.New) into an instanceStarter, binding the engine: the live Thresher and
// a fresh starter id. It builds the starters but does not register them — the
// Thresher decides when (auto mode, at the later of RegisterProcess/Run) and
// whether (manual mode registers none, FR-9).
//
// It is an unexported helper called only by RegisterProcess with a freshly built
// snapshot and the live Thresher, so it takes both as guaranteed non-nil.
func scanInstantiatingStarts(s *snapshot.Snapshot, thr *Thresher) []*instanceStarter {
	starters := make([]*instanceStarter, 0, len(s.InstantiatingStarts))

	for _, is := range s.InstantiatingStarts {
		starters = append(starters, &instanceStarter{
			thr:       thr,
			snapshot:  s,
			startNode: is.StartNode,
			eDef:      is.EventDef,
			corrKey:   is.CorrelationKey,
			id:        foundation.GenerateID(),
		})
	}

	return starters
}

// triggerName returns the human-readable trigger name of a starter's definition:
// the message name for a message starter, the signal name for a signal starter
// (SRD-026), or "" for any other kind.
func triggerName(eDef flow.EventDefinition) string {
	switch d := eDef.(type) {
	case *events.MessageEventDefinition:
		if m := d.Message(); m != nil {
			return m.Name()
		}

	case *events.SignalEventDefinition:
		if s := d.Signal(); s != nil {
			return s.Name()
		}
	}

	return ""
}

// interface check
var _ eventproc.EventProcessor = (*instanceStarter)(nil)
