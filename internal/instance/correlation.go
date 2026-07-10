package instance

import (
	"context"

	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/msgflow"
)

// AssociateConversationKey records value under the conversation key named name
// set-if-absent (SRD-017 FR-1). It is the no-result form the optional msgflow
// recorder capability uses (first keyed send); the delivery path uses the
// bool-returning associateConversationKey to learn whether to extend receivers.
func (inst *Instance) AssociateConversationKey(name, value string) {
	inst.associateConversationKey(name, value)
}

// associateConversationKey records value under name if name is not already held,
// returning whether it was added (a new conversation key). Empty inputs are a
// no-op returning false. Guarded by convMu — forked tracks run concurrently.
func (inst *Instance) associateConversationKey(name, value string) bool {
	if name == "" || value == "" {
		return false
	}

	inst.convMu.Lock()
	defer inst.convMu.Unlock()

	if _, ok := inst.convKeys[name]; ok {
		return false
	}

	inst.convKeys[name] = value

	return true
}

// conversationKeyValues returns a snapshot of the instance's conversation key
// values (SRD-017 §4.3): the keys its in-instance message receivers subscribe
// on so a follow-up message routes to this instance. An instance with no
// established key returns nil (a wildcard subscription). Taken under convMu —
// forked tracks run on concurrent goroutines.
func (inst *Instance) conversationKeyValues() []string {
	inst.convMu.Lock()
	defer inst.convMu.Unlock()

	if len(inst.convKeys) == 0 {
		return nil
	}

	vals := make([]string, 0, len(inst.convKeys))
	for _, v := range inst.convKeys {
		vals = append(vals, v)
	}

	return vals
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
	return inst.conversationKeyValues()
}

// validateAndAssociate applies the conversation-token rules on a received
// message (SRD-017 §4.5, BPMN §8.4.2). It derives every declared correlation key
// from the message payload in two passes: first it checks for a mismatch — a key
// this instance already holds whose derived value differs — and, if any, reports
// mismatch=true and associates nothing (the message isn't for this conversation,
// so the caller rejects it); otherwise it associates each not-yet-held value
// (lazy secondary-key initialization), extending currently-parked receivers so
// the conversation becomes reachable by the new key, and reports mismatch=false.
func (inst *Instance) validateAndAssociate(
	ctx context.Context,
	eDef flow.EventDefinition,
) (mismatch bool) {
	keys := inst.s.CorrelationKeys
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
		v, ok, err := msgflow.DeriveKey(
			ctx, inst.ExpressionEngine(), key, msg, payload)
		if err != nil {
			inst.Logger().Warn("conversation key derivation failed",
				"instance_id", inst.ID(), "correlation_key", key.Name)

			continue
		}

		if !ok {
			continue
		}

		if held, isHeld := inst.heldConversationKey(key.Name); isHeld &&
			held != v {
			inst.Logger().Debug("correlation key mismatch — message dropped",
				"instance_id", inst.ID(), "correlation_key", key.Name)

			return true
		}

		derived[key.Name] = v
	}

	for name, v := range derived {
		if inst.associateConversationKey(name, v) {
			inst.extendReceivers(v)
		}
	}

	return false
}

// heldConversationKey returns the value held for the named conversation key and
// whether it is held. Read under convMu — forked tracks run concurrently.
func (inst *Instance) heldConversationKey(name string) (string, bool) {
	inst.convMu.Lock()
	defer inst.convMu.Unlock()

	v, ok := inst.convKeys[name]

	return v, ok
}

// extendReceivers adds a newly-learned correlation value to every in-instance
// message receiver's broker subscription (SRD-017 §4.5), so a follow-up carrying
// it routes here. It reaches the EventHub's optional AddEventKey capability
// structurally (no interface change). A receiver that isn't parked yet has no
// waiter — a benign no-op; it picks the value up from the grown key-set when it
// registers.
func (inst *Instance) extendReceivers(value string) {
	adder, ok := inst.parentEventProducer.(interface {
		AddEventKey(eDefID, key string) error
	})
	if !ok {
		return
	}

	for _, n := range inst.s.Nodes {
		en, ok := n.(flow.EventNode)
		if !ok {
			continue
		}

		for _, d := range en.Definitions() {
			if d.Type() != flow.TriggerMessage {
				continue
			}

			if err := adder.AddEventKey(d.ID(), value); err != nil {
				inst.Logger().Debug("extend receiver subscription failed",
					"instance_id", inst.ID(),
					"event_definition_id", d.ID())
			}
		}
	}
}
