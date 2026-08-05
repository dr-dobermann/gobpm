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

	if err := t.rebuildAndContinue(h.ID(), nil,
		instance.WithPendingIncidentOp(op, incidentID, resp)); err != nil {
		return errs.New(
			errs.M("incident op: the parked instance %q doesn't wake",
				h.ID()),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err))
	}

	if inst, err := t.instanceByID(h.ID()); err == nil {
		h.adopt(inst)
	}

	select {
	case err := <-resp:
		return err

	case <-ctx.Done():
		return ctx.Err()
	}
}
