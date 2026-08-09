package thresher

import (
	"context"

	"github.com/dr-dobermann/gobpm/internal/instance"
	"github.com/dr-dobermann/gobpm/pkg/errs"
)

// wakeForIncidentOp rebuilds a parked instance from its checkpoint and applies
// the operator's incident operation on the fresh loop (SRD-079 §3.6). The op
// rides the rebuild as a pending request — the trigger-absent hydration shape
// (SRD-071 FR-8) — so it is applied BEFORE the loop's park decision and can
// never be lost to an immediate re-park: a retry or resolve spawns the
// continuation that keeps the loop alive, a drop closes and parks again.
func (t *Thresher) wakeForIncidentOp(
	ctx context.Context,
	h *InstanceHandle,
	op instance.IncidentOp,
	incidentID string,
) error {
	resp := make(chan error, 1)

	return t.rebuildForOp(ctx, h, "incident op", resp,
		instance.WithPendingIncidentOp(op, incidentID, resp))
}

// cancelParked cancels an instance whose loop has already exited (FIX-038
// §1.10). A dehydrated instance has no loop to observe a context cancellation,
// so InstanceHandle.Cancel used to cancel a context nobody was reading: the
// request vanished and the next wake resumed the instance as if it had never
// been made. The cancel rides a rebuild instead, exactly as an incident
// operation does, and the fresh loop tears the instance down before its park
// decision.
func (t *Thresher) cancelParked(ctx context.Context, h *InstanceHandle) error {
	resp := make(chan error, 1)

	return t.rebuildForOp(ctx, h, "cancel", resp,
		instance.WithPendingCancel(resp))
}

// rebuildForOp is the shape every operator request against a PARKED instance
// needs: a parked instance has no loop to receive anything, so the request
// rides a rebuild and the fresh loop applies it before deciding whether to park
// again. op names the request in the failure message; opt is the carrier, and
// resp is the channel that carrier answers on.
//
// The two callers were near-copies, and the copy is what let FIX-038 §1.9's fix
// land in one of them only: an incident-op rebuild adopted the new instance
// object without re-attaching the handle's observers, so a host's subscription
// went quiet at the first operator retry — the very defect the handle-owned
// registry exists to prevent.
func (t *Thresher) rebuildForOp(
	ctx context.Context,
	h *InstanceHandle,
	op string,
	resp <-chan error,
	opt instance.Option,
) error {
	// Refuse before claiming anything if the engine is already going away.
	// engineContext reports "running" for the whole life of the process once
	// Run has been called — the engine pointer is never cleared — so a request
	// arriving after shutdown otherwise rebuilt the instance from its
	// checkpoint, watched the fresh loop tear straight back down on the dead
	// engine context, and reported SUCCESS to the operator. That is the same
	// silent loss as the pre-rebuild cancel: the operator is told the request
	// landed when nothing observed it.
	if engCtx, running := t.engineContext(); !running || engCtx.Err() != nil {
		return t.errEngineNotRunning(op)
	}

	// Take the wake latch like every other rebuild path. It is the ONLY thing
	// preventing two live loops over one instance: the repository claim does
	// not exclude, it orders — claimForWake RETRIES a lost CAS, so two
	// concurrent rebuilds both succeed at successive incarnations (FIX-037
	// §1.3). Without this an operator's request racing a timer wake started a
	// second execution loop over the same state.
	if err := t.awaitClaim(h.ID(), op); err != nil {
		return err
	}

	defer t.releaseWake(h.ID())

	if err := t.rebuildAndContinue(h.ID(), nil, opt); err != nil {
		return errs.New(
			errs.M("%s: the parked instance %q doesn't wake", op, h.ID()),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err))
	}

	if inst, err := t.instanceByID(h.ID()); err == nil {
		h.adopt(inst)
		h.reattachObservers()
	}

	return awaitOpVerdict(ctx, resp)
}

// awaitOpVerdict waits for the fresh loop's answer to a request that rode a
// rebuild. The caller's context bounds the wait, so an operator is never left
// blocked on a loop that died before it could answer.
func awaitOpVerdict(ctx context.Context, resp <-chan error) error {
	select {
	case err := <-resp:
		return err

	case <-ctx.Done():
		return ctx.Err()
	}
}
