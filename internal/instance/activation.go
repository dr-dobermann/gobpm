package instance

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/exec"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// guardEval returns an exec.GuardEval that evaluates a Complex gateway's data guard
// (ADR-005 v.3 §2.11 / SRD-023) against the instance's process-level data. The
// activation decision happens at a point with no per-node execution frame (a track's
// Activate before it executes, or the loop's death-recheck), so the guard reads a
// fresh frame opened at the root scope — the same live, frame-free read the observe
// reader uses (SRD-018). The count and gate identities never reach this expression;
// only process data does, so there is no name collision.
func (inst *Instance) guardEval(ctx context.Context) exec.GuardEval {
	return func(cond data.FormalExpression) (bool, error) {
		f, err := inst.sc.openFrame("complex-guard", "complex-guard")
		if err != nil {
			return false,
				errs.New(
					errs.M("couldn't build the complex gateway guard frame"),
					errs.C(errorClass, errs.OperationFailed),
					errs.E(err))
		}

		res, err := inst.ExpressionEngine().Evaluate(ctx, cond, newExecEnv(inst, f))
		if err != nil {
			return false,
				errs.New(
					errs.M("complex gateway guard evaluation failed"),
					errs.C(errorClass, errs.OperationFailed),
					errs.E(err))
		}

		v, ok := res.Get(ctx).(bool)
		if !ok {
			return false,
				errs.New(
					errs.M("complex gateway guard is not boolean"),
					errs.C(errorClass, errs.TypeCastingError))
		}

		return v, nil
	}
}

// announceCreated emits the instance's Created transition (SRD-041 §3.4). New
// sets the state directly rather than through setState, so this is the one place
// Created is observable. No local handle observer can exist yet (the handle
// reaches the host only after StartProcess), so it reaches the engine sink +
// echo.
func (inst *Instance) announceCreated() {
	inst.observe(observability.ObsEvent{
		Kind:  observability.KindInstanceState,
		Phase: observability.PhaseCreated,
	})
}

// fail records err as the instance's fatal error and cancels it, driving the loop's
// termination (the parked tracks unblock via ctx.Done). Called only from the loop
// goroutine (recheckJoin), so it stays the single writer of lastErr.
func (inst *Instance) fail(err error) {
	// The whole instance is faulting — an actionable failure that affected
	// engine state (ADR-022 v.1 §2.4 canonical Error). This is the single fault
	// boundary; every fault path routes here (ADR-022 §2.3). The operator-log
	// record is now the producer's echo of this InstanceState/Failed event (it
	// echoes at Error and carries the same instance_id + error), not a separate
	// Logger().Error call — one producer, two channels (SRD-041 FR-4). Failed is
	// phase-only: the State enum is untouched, so State()/WaitCompletion still
	// report Terminated.
	inst.observe(observability.ObsEvent{
		Kind:    observability.KindInstanceState,
		Phase:   observability.PhaseFailed,
		Details: map[string]string{observability.AttrError: err.Error()},
	})

	inst.lastErr.Store(&err)

	if inst.cancel != nil {
		inst.cancel()
	}
}
