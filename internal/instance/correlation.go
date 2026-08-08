package instance

import (
	"context"
	"sync"

	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/msgflow"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// correlator owns the instance's conversation keys and the message-correlation
// protocol (SRD-017, BPMN §8.4.2). It has its own lock because it is touched
// from three goroutine contexts — track goroutines via the msgflow recorder
// (AssociateConversationKey on a keyed send), the message waiter's structural
// CorrelationKeys() read, and the loop (validateAndAssociate in dispatch) — the
// one Instance concern besides observation that is NOT loop-confined (SRD-040).
type correlator struct {
	inst *Instance
	keys map[string]string
	// iterKeys are the parked executions' iteration-correlation values
	// (SRD-085 FR-3), keyed by track id: they join CorrelationKeys() so
	// the hub's subscription filter sees each waiting iteration's
	// value; the loop drops an entry when its track flips out of
	// waiting.
	iterKeys map[string]string
	// iterKeyNames are the declared keys used at ITERATION granularity
	// (ADR-006 v.5 §2.9.3): the conversation gate must not associate or
	// mismatch on them — one instance legitimately receives N different
	// values of such a key, one per iteration.
	iterKeyNames map[string]struct{}

	m sync.Mutex
}

// addIterKey records a parked execution's iteration-correlation value.
func (c *correlator) addIterKey(trackID, value string) {
	c.m.Lock()
	defer c.m.Unlock()

	if c.iterKeys == nil {
		c.iterKeys = map[string]string{}
	}

	c.iterKeys[trackID] = value
}

// dropIterKey removes a track's iteration-correlation value.
func (c *correlator) dropIterKey(trackID string) {
	c.m.Lock()
	defer c.m.Unlock()

	delete(c.iterKeys, trackID)
}

// markIterationKeyName records that the named declared key routes at
// iteration granularity, excluding it from conversation gating.
func (c *correlator) markIterationKeyName(name string) {
	c.m.Lock()
	defer c.m.Unlock()

	if c.iterKeyNames == nil {
		c.iterKeyNames = map[string]struct{}{}
	}

	c.iterKeyNames[name] = struct{}{}
}

// isIterationKeyName reports whether the named key routes at iteration
// granularity.
func (c *correlator) isIterationKeyName(name string) bool {
	c.m.Lock()
	defer c.m.Unlock()

	_, ok := c.iterKeyNames[name]

	return ok
}

// AssociateConversationKey records value under the conversation key named name
// set-if-absent (SRD-017 FR-1). It is the no-result form the optional msgflow
// recorder capability uses (first keyed send); the delivery path uses the
// bool-returning associate to learn whether to extend receivers.
func (inst *Instance) AssociateConversationKey(name, value string) {
	inst.corr.associate(name, value)
}

// associate records value under name if name is not already held, returning
// whether it was added (a new conversation key). Empty inputs are a no-op
// returning false. Guarded by c.m — forked tracks run concurrently.
func (c *correlator) associate(name, value string) bool {
	if name == "" || value == "" {
		return false
	}

	c.m.Lock()
	if _, ok := c.keys[name]; ok {
		c.m.Unlock()

		return false
	}

	c.keys[name] = value
	c.m.Unlock()

	// A new conversation key was learned (SRD-041 §3.4). Emitted off c.m so the
	// sink's echo/fan-out never runs under the correlator lock.
	c.inst.report(observability.Fact{
		Kind:  observability.KindCorrelation,
		Phase: observability.PhaseKeyAssociated,
		Details: map[string]string{
			observability.AttrCorrelationKey:   name,
			observability.AttrCorrelationValue: value,
		},
	})

	return true
}

// values returns a snapshot of the instance's conversation key values
// (SRD-017 §4.3): the keys its in-instance message receivers subscribe on so a
// follow-up message routes to this instance. An instance with no established
// key returns nil (a wildcard subscription). Taken under c.m — forked tracks
// run on concurrent goroutines.
func (c *correlator) values() []string {
	c.m.Lock()
	defer c.m.Unlock()

	if len(c.keys) == 0 && len(c.iterKeys) == 0 {
		return nil
	}

	vals := make([]string, 0, len(c.keys)+len(c.iterKeys))
	for _, v := range c.keys {
		vals = append(vals, v)
	}

	// the parked iterations' values (SRD-085 FR-3) — each waiting
	// execution is reachable by its own key.
	for _, v := range c.iterKeys {
		vals = append(vals, v)
	}

	return vals
}

// deriveNamed derives the NAMED declared correlation key's value from a
// received message's payload (SRD-085 FR-2) — the envelope side of the
// iteration-correlation match, over the same DeriveKey machinery
// validateAndAssociate uses. false when the key is unknown, the
// definition carries no message, or derivation fails (each already a
// logged concern on the conversation path).
func (c *correlator) deriveNamed(
	ctx context.Context,
	eDef flow.EventDefinition,
	keyName string,
) (string, bool) {
	mr, ok := eDef.(interface {
		Message() *bpmncommon.Message
	})
	if !ok {
		return "", false
	}

	var payload any
	if items := eDef.GetItemsList(); len(items) != 0 {
		payload = items[0].Structure().Get(ctx)
	}

	for _, key := range c.inst.s.CorrelationKeys {
		if key.Name != keyName {
			continue
		}

		v, ok, err := msgflow.DeriveKey(
			ctx, c.inst.ExpressionEngine(), key, mr.Message(), payload)
		if err != nil || !ok {
			return "", false
		}

		return v, true
	}

	return "", false
}

// The Instance is the hub-facing event processor for Message catches (SRD-027 FR-8).
var _ eventproc.EventProcessor = (*Instance)(nil)

// ProcessEvent (eventproc.EventProcessor) is the hub-facing entry for a Message catch: the
// Instance is the registered processor (SRD-027 FR-8), because message correlation state is
// instance-owned. It does NOT touch track state — it emits the fired event to its own loop
// carrying NO track; the loop resolves the parked track via its msgEDef→track index and runs
// validateAndAssociate before dispatch. Returns once enqueued, not once applied.
func (inst *Instance) ProcessEvent(
	_ context.Context,
	eDef flow.EventDefinition,
) error {
	if eDef == nil {
		return errs.New(
			errs.M("Instance.ProcessEvent: a nil EventDefinition isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed, errs.InvalidParameter))
	}

	// track == nil marks the Message branch — the loop resolves the target via msgIdx (§3.4).
	inst.emit(trackEvent{kind: evDeliver, eDef: eDef})

	return nil
}

// CorrelationKeys returns the conversation key values this instance has established
// (SRD-017 §4.3, SRD-027 FR-8). The message waiter reads it structurally (the "declared
// filter") to subscribe this receiver keyed to its conversation; an instance with no keys
// yields none, leaving a wildcard subscription. Ownership moved here from track when the
// Instance became the message processor — only the message path was ever keyed.
func (inst *Instance) CorrelationKeys() []string {
	return inst.corr.values()
}

// validateAndAssociate applies the conversation-token rules on a received
// message (SRD-017 §4.5, BPMN §8.4.2). It derives every declared correlation key
// from the message payload in two passes: first it checks for a mismatch — a key
// this instance already holds whose derived value differs — and, if any, reports
// mismatch=true and associates nothing (the message isn't for this conversation,
// so the caller rejects it); otherwise it associates each not-yet-held value
// (lazy secondary-key initialization), extending currently-parked receivers so
// the conversation becomes reachable by the new key, and reports mismatch=false.
func (c *correlator) validateAndAssociate(
	ctx context.Context,
	eDef flow.EventDefinition,
) (mismatch bool) {
	keys := c.inst.s.CorrelationKeys
	if len(keys) == 0 {
		return false
	}

	mr, ok := eDef.(interface {
		Message() *bpmncommon.Message
	})
	if !ok {
		return false
	}

	msg := mr.Message()

	var payload any
	if items := eDef.GetItemsList(); len(items) != 0 {
		payload = items[0].Structure().Get(ctx)
	}

	derived := make(map[string]string, len(keys))

	for _, key := range keys {
		// an iteration-granular key is not a conversation key (ADR-006
		// v.5 §2.9.3): N iterations of one instance legitimately hold N
		// values of it, so the conversation gate neither associates nor
		// mismatches on it — the loop's subscription match routes it.
		if c.isIterationKeyName(key.Name) {
			continue
		}

		v, ok, err := msgflow.DeriveKey(
			ctx, c.inst.ExpressionEngine(), key, msg, payload)
		if err != nil {
			c.inst.Logger().Warn("conversation key derivation failed",
				observability.AttrInstanceID, c.inst.ID(), observability.AttrCorrelationKey, key.Name,
				observability.AttrError, err.Error())

			continue
		}

		if !ok {
			continue
		}

		if held, isHeld := c.held(key.Name); isHeld && held != v {
			// The message belongs to a different conversation — dropped. The
			// producer echoes this at Debug, replacing the former direct log
			// (SRD-041 §3.4).
			c.inst.report(observability.Fact{
				Kind:  observability.KindCorrelation,
				Phase: observability.PhaseMismatched,
				Details: map[string]string{
					observability.AttrCorrelationKey:   key.Name,
					observability.AttrCorrelationValue: v,
				},
			})

			return true
		}

		derived[key.Name] = v
	}

	for name, v := range derived {
		if c.associate(name, v) {
			c.extendReceivers(v)
		}
	}

	// The message correlated to this instance's conversation (no mismatch, at
	// least one declared key derived): announce Matched (SRD-041 §3.4).
	if len(derived) != 0 {
		c.inst.report(observability.Fact{
			Kind:    observability.KindCorrelation,
			Phase:   observability.PhaseMatched,
			Details: map[string]string{observability.AttrEventDefinitionID: eDef.ID()},
		})
	}

	return false
}

// held returns the value held for the named conversation key and whether it is
// held. Read under c.m — forked tracks run concurrently.
func (c *correlator) held(name string) (string, bool) {
	c.m.Lock()
	defer c.m.Unlock()

	v, ok := c.keys[name]

	return v, ok
}

// extendReceivers adds a newly-learned correlation value to every in-instance
// message receiver's broker subscription (SRD-017 §4.5), so a follow-up carrying
// it routes here. It reaches the EventHub's optional AddEventKey capability
// structurally (no interface change). A receiver that isn't parked yet has no
// waiter — a benign no-op; it picks the value up from the grown key-set when it
// registers.
func (c *correlator) extendReceivers(value string) {
	adder, ok := c.inst.parentEventProducer.(interface {
		AddEventKey(eDefID, key string) error
	})
	if !ok {
		return
	}

	for _, n := range c.inst.s.Nodes {
		c.extendNode(n, adder, value)
	}
}

// extendNode extends one node's message subscriptions and DESCENDS into
// a composite's inner nodes (SRD-085 FR-3): a message catch inside a
// Sub-Process body — a parallel-MI iteration's catch being the
// canonical case — is a receiver too, and the flat top-level walk
// silently missed every one of them.
func (c *correlator) extendNode(
	n flow.Node,
	adder interface {
		AddEventKey(eDefID, key string) error
	},
	value string,
) {
	if en, ok := n.(flow.EventNode); ok {
		for _, d := range en.Definitions() {
			if d.Type() != flow.TriggerMessage {
				continue
			}

			if err := adder.AddEventKey(d.ID(), value); err != nil {
				// Best-effort (ADR-022 v.1 §2.3(2)): AddEventKey no-ops on a
				// not-yet-parked receiver; a real failure is degradation, logged
				// with its error and the flow continues.
				c.inst.Logger().Debug("extend receiver subscription failed",
					observability.AttrInstanceID, c.inst.ID(),
					observability.AttrEventDefinitionID, d.ID(),
					observability.AttrError, err.Error())
			}
		}
	}

	if inner, ok := n.(interface{ Nodes() []flow.Node }); ok {
		for _, in := range inner.Nodes() {
			c.extendNode(in, adder, value)
		}
	}
}

// snapshotKeys copies the conversation key-set for the checkpoint
// (SRD-070 FR-3); nil when no key is associated yet.
func (c *correlator) snapshotKeys() map[string]string {
	c.m.Lock()
	defer c.m.Unlock()

	if len(c.keys) == 0 {
		return nil
	}

	out := make(map[string]string, len(c.keys))
	for k, v := range c.keys {
		out[k] = v
	}

	return out
}

// restoreKeys adopts a checkpoint's recorded conversation key-set
// (SRD-070 FR-6).
func (c *correlator) restoreKeys(keys map[string]string) {
	if len(keys) == 0 {
		return
	}

	c.m.Lock()
	defer c.m.Unlock()

	for k, v := range keys {
		c.keys[k] = v
	}
}
