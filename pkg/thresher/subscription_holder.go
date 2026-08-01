package thresher

import (
	"context"
	"errors"
	"github.com/dr-dobermann/gobpm/pkg/observability"

	gerrs "github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/exec"

	"github.com/dr-dobermann/gobpm/internal/instance"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// subHolder is the engine-owned hub subscriber standing in for ONE
// message/signal wait of one track (ADR-007 v.2 §2.4, SRD-071 FR-3/FR-7). The
// hub points at it from the moment the wait is armed, so it outlives the
// instance's goroutines: a trigger arriving after the instance released them
// reaches the holder, never a vanished loop. On fire it drives the shared wake
// path, which forks on residency (resident → deliver into the live loop;
// dehydrated → gate correlation, then rebuild and continue).
type subHolder struct {
	th   *Thresher
	eDef flow.EventDefinition
	foundation.BaseElement
	instanceID string
	trackID    string
	// convKeys are the held instance's conversation key values, captured at
	// arm time. The message waiter reads them through CorrelationKeys to build
	// its broker subscription, so the holder listens to exactly the
	// conversation the instance's own registration would have (ADR-016).
	convKeys []string
}

// CorrelationKeys implements the message waiter's declared-filter capability
// (SRD-017 §4.3): the holder stands in for the instance, so it contributes the
// same conversation keys — a foreign conversation is filtered at the broker and
// never reaches a released instance.
func (h *subHolder) CorrelationKeys() []string { return h.convKeys }

// ProcessEvent is the hub's delivery point: hand the trigger to the wake path.
// An error here would be reported by the hub against the holder, so failures
// are surfaced through the engine's own reporting instead (never fatal to the
// subscription).
func (h *subHolder) ProcessEvent(
	_ context.Context, eDef flow.EventDefinition,
) error {
	h.th.wakeFromSubscription(h, eDef)

	return nil
}

// subKey identifies one held subscription — a track may hold several (the
// Event-Based Gateway arms a set, SRD-071 FR-3a).
type subKey struct {
	instanceID string
	trackID    string
	eDefID     string
}

// HoldSubscription implements exec.WaitHolders (SRD-071 FR-7): register the
// engine's own subscriber for a message/signal wait so the subscription
// survives the instance's release. The instance then registers nothing itself.
func (t *Thresher) HoldSubscription(
	instanceID, trackID string,
	eDef flow.EventDefinition,
	convKeys []string,
	_ exec.WaitKind,
) error {
	if eDef == nil {
		return errNoHold("HoldSubscription: a nil EventDefinition isn't allowed")
	}

	// Without a repository there is no checkpoint to wake from, so holding a
	// subscription would strand the wait — decline and let the instance keep it.
	if !t.cfg.repoSet {
		return errNoHold("HoldSubscription: the engine holds no checkpoints")
	}

	be, err := foundation.NewBaseElement()
	if err != nil {
		return errNoHold("HoldSubscription: couldn't mint the holder identity")
	}

	h := &subHolder{
		BaseElement: *be,
		th:          t,
		instanceID:  instanceID,
		trackID:     trackID,
		eDef:        eDef,
		convKeys:    append([]string{}, convKeys...),
	}

	if err := t.RegisterEvent(h, eDef); err != nil {
		return err
	}

	t.subMu.Lock()
	t.subs[subKey{instanceID, trackID, eDef.ID()}] = h
	t.subMu.Unlock()

	return nil
}

// ReleaseWaits implements exec.WaitHolders: withdraw EVERY hold taken for a
// track — its timer deadline and all its subscriptions (the whole set an
// Event-Based Gateway armed). Idempotent.
func (t *Thresher) ReleaseWaits(instanceID, trackID string) {
	if t.timerSvc != nil {
		t.timerSvc.release(instanceID, trackID)
	}

	t.subMu.Lock()

	var dropped []*subHolder

	for k, h := range t.subs {
		if k.instanceID == instanceID && k.trackID == trackID {
			dropped = append(dropped, h)

			delete(t.subs, k)
		}
	}

	t.subMu.Unlock()

	// unregister outside the lock: the hub is an independent subsystem and must
	// never be touched under an engine lock (the FIX-002 RC2 rule).
	for _, h := range dropped {
		if err := t.UnregisterEvent(h, h.eDef.ID()); err != nil {
			t.cfg.logger.Debug("releasing a held subscription",
				observability.AttrInstanceID, instanceID, observability.AttrTrackID, trackID,
				observability.AttrEventDefinitionID, h.eDef.ID(), observability.AttrError, err.Error())
		}
	}
}

// wakeFromSubscription drives a fired message/signal into the wake path
// (SRD-071 FR-4/FR-7). Correlation is already settled by the time a message
// arrives here — the holder subscribes keyed to the instance's conversation,
// so the broker filters foreign ones exactly as it does for a resident
// instance. The rebuilt instance still applies the authoritative rule (it also
// ASSOCIATES the derived keys), and a message that fails it there is a benign
// drop, not an engine failure.
func (t *Thresher) wakeFromSubscription(
	h *subHolder, eDef flow.EventDefinition,
) {
	pending := &instance.PendingTrigger{TrackID: h.trackID, EDef: eDef}

	t.reportDropOrFailure(h, eDef, t.wakeInstance(h.instanceID, pending))
}

// reportDropOrFailure classifies a wake's outcome: nothing to say on success, a
// Debug line when the trigger simply belonged to ANOTHER conversation (a benign
// drop — the instance stays dehydrated, exactly as a resident one would drop
// the message and stay parked), and the loud operator fact for everything else.
func (t *Thresher) reportDropOrFailure(
	h *subHolder, eDef flow.EventDefinition, err error,
) {
	if err == nil {
		return
	}

	var ae *gerrs.ApplicationError
	if errors.As(err, &ae) && ae.HasClass(correlationDropClass) {
		t.cfg.logger.Debug("a wake trigger belongs to another conversation",
			observability.AttrInstanceID, h.instanceID, observability.AttrTrackID, h.trackID,
			observability.AttrEventDefinitionID, eDef.ID())

		return
	}

	t.reportWakeFailure(h.instanceID, err)
}
