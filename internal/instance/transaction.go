package instance

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// The Transaction Sub-Process abort (BPMN §10.7, ADR-028, SRD-061): a Cancel
// End Event inside a Transaction aborts it in a fixed, load-bearing order —
// compensate the completed activities, terminate the residual live ones, then
// leave through the Cancel boundary. Cancel is a direct-resolution event: the
// loop resolves it locally (never the hub), reusing the ADR-026 compensation
// sweep as the ACID-like barrier before teardown. All state is loop-owned.

// cancelTransaction aborts the Transaction Sub-Process enclosing the track that
// reached a Cancel End Event (ADR-028 §2.3, SRD-061 FR-5). Step 1 runs first and
// on its own: a wait-mode, scope-wide compensation sweep over the Transaction
// scope's ledger, so every compensation handler completes BEFORE the abort
// proceeds — the teardown in finalizeTransaction discards the very ledger the
// sweep consumes, so it must run last. The scope is flagged aborting so a
// residual track draining to zero mid-sweep does not resume the host normally
// (decScope); the finalize owns the teardown, driven off the sweep's completion.
func (ls *loopState) cancelTransaction(ctx context.Context, t *track) {
	entry, ok := ls.scopes[t.scopePath]
	if !ok {
		return // the Transaction scope already closed — a late Cancel is benign.
	}

	entry.aborting = true

	ls.inst.report(observability.Fact{
		Kind:  observability.KindCompensation,
		Phase: observability.PhaseThrown,
		Details: map[string]string{
			observability.AttrScopePath: string(t.scopePath),
		},
	})

	sweep := &compSweep{
		path:   t.scopePath,
		txHost: entry.host,
	}
	sweep.queue = ls.collectCompensation(t.scopePath, "")

	if len(sweep.queue) == 0 {
		ls.finishSweep(ctx, sweep) // nothing to compensate — finalize at once.

		return
	}

	ls.runNextCompensation(ctx, sweep)
}

// finalizeTransaction completes a Transaction abort once its compensation sweep
// has drained (SRD-061 FR-5 steps 2–3). cancelScope terminates the residual live
// tracks under the Transaction scope and discards the now-consumed ledger; the
// host then leaves through the Cancel boundary — the boundary's outgoing flow
// replaces the host's normal continuation. A Transaction without a Cancel
// boundary simply ends there: the abort tears the host down and no token
// continues (ADR-028 §2.4). The scope's node/host are read BEFORE cancelScope,
// which deletes the scope entry.
func (ls *loopState) finalizeTransaction(
	ctx context.Context, sweep *compSweep,
) {
	entry, ok := ls.scopes[sweep.path]
	if !ok {
		return
	}

	node, host := entry.node, entry.host

	ls.cancelScope(sweep.path, observability.PhaseCanceled)

	if bev := cancelBoundaryOn(node); bev != nil {
		ls.spawnForks(ctx, trackEvent{track: host, flows: bev.Outgoing()})
	}

	host.cancel()
	ls.flipNotParked(host)
	ls.disarmBoundaries(host.ID())
}

// cancelBoundaryOn returns the Cancel boundary event attached to node, or nil.
// A Transaction carries at most one always-interrupting Cancel boundary (ADR-028
// §2.4); it is the model-declared exit the abort routes control to.
func cancelBoundaryOn(node flow.Node) flow.BoundaryEvent {
	host, ok := node.(boundaryHoster)
	if !ok {
		return nil
	}

	for _, en := range host.BoundaryEvents() {
		bev := en.(flow.BoundaryEvent)

		for _, d := range bev.Definitions() {
			if d.Type() == flow.TriggerCancel {
				return bev
			}
		}
	}

	return nil
}
