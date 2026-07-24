package instance

import (
	"context"
	"strconv"

	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// The compensation sweep (ADR-026 §2.2/§2.4, SRD-059 FR-5/FR-6/FR-8): a
// Compensation throw resolves DIRECTLY against the completion ledger — never
// the hub. Targeted (activityRef) or scope-wide (reverse completion order),
// handlers run SEQUENTIALLY as spawned tracks; a wait-for-completion thrower
// parks and resumes when the sweep drains. Unresolved compensation is logged,
// never silent, never a fault. All state is loop-owned.

// compensationDoneTrigger is the internal trigger of the sweep-completion
// sentinel the loop delivers to a parked wait-throw (the scopeDone pattern).
const compensationDoneTrigger flow.EventTrigger = "gobpm:compensation-done"

// compensationDone is the synthetic completion delivered to the parked
// thrower's evtCh when the sweep drains.
type compensationDone struct {
	flow.EventDefinition
}

// newCompensationDone mints one completion sentinel.
func newCompensationDone() *compensationDone {
	return &compensationDone{}
}

// Type implements flow.EventDefinition for the sentinel.
func (*compensationDone) Type() flow.EventTrigger {
	return compensationDoneTrigger
}

// ID implements foundation identity for the sentinel (never registered).
func (*compensationDone) ID() string { return "gobpm-compensation-done" }

// compSweep is one in-flight compensation run: the remaining entries (already
// in invocation order), the parked thrower to resume (nil for a
// fire-and-forget throw), and the scope context handlers spawn in.
type compSweep struct {
	thrower *track
	// txHost tags a Transaction-abort sweep (SRD-061 FR-5): when set, the sweep
	// drives finalizeTransaction (terminate residuals, exit via the Cancel
	// boundary) once it drains, instead of resuming a parked compensation thrower.
	txHost  *track
	path    scope.DataPath
	queue   []*ledgerEntry
	wait    bool
}

// applyCompensate resolves a Compensation throw (SRD-059 FR-6): report Thrown,
// collect the targeted entry or the scope's whole ledger in reverse completion
// order (consuming them — a compensated entry leaves the ledger), and start
// the sequential sweep. A wait-for-completion thrower is registered
// parked-and-undelivered so the drain can resume it. Runs on the loop
// goroutine.
func (ls *loopState) applyCompensate(ctx context.Context, ev trackEvent) {
	if ev.compWait {
		ls.waiting[ev.track.ID()] = struct{}{}
	}

	details := map[string]string{
		observability.AttrScopePath: string(ev.track.scopePath),
	}
	if ev.compRef != "" {
		details["activity_ref"] = ev.compRef
	}

	ls.inst.report(observability.Fact{
		Kind:    observability.KindCompensation,
		Phase:   observability.PhaseThrown,
		Details: details,
	})

	sweep := &compSweep{
		path: ev.track.scopePath,
		wait: ev.compWait,
	}
	if ev.compWait {
		sweep.thrower = ev.track
	}

	sweep.queue = ls.collectCompensation(ev.track.scopePath, ev.compRef)

	if len(sweep.queue) == 0 {
		ls.reportUnresolvedCompensation(ev)
		ls.finishSweep(ctx, sweep)

		return
	}

	ls.runNextCompensation(ctx, sweep)
}

// collectCompensation consumes the matching ledger entries at path: the one
// targeted entry (searched through folded children too), or — scope-wide —
// every entry in REVERSE completion order (ADR-026 §2.4; folded children of a
// swept entry ride with their parent entry and are not separately queued in
// this landing — recursive default compensation is ADR-026's designed-for).
func (ls *loopState) collectCompensation(
	path scope.DataPath,
	ref string,
) []*ledgerEntry {
	book := ls.ledgers[path]

	if ref == "" {
		delete(ls.ledgers, path)

		out := make([]*ledgerEntry, 0, len(book))
		for i := len(book) - 1; i >= 0; i-- {
			if book[i].handlerID != "" {
				out = append(out, book[i])
			}
		}

		return out
	}

	rest, found := extractLedgerEntry(book, ref)
	ls.ledgers[path] = rest

	if found == nil || found.handlerID == "" {
		return nil
	}

	return []*ledgerEntry{found}
}

// extractLedgerEntry removes and returns the entry with activityID == ref,
// searching folded children recursively (a targeted throw may address an
// activity inside a completed Sub-Process — SRD-059 FR-6).
func extractLedgerEntry(
	book []*ledgerEntry,
	ref string,
) ([]*ledgerEntry, *ledgerEntry) {
	for i, le := range book {
		if le.activityID == ref {
			return append(book[:i:i], book[i+1:]...), le
		}

		if rest, found := extractLedgerEntry(le.folded, ref); found != nil {
			le.folded = rest

			return book, found
		}
	}

	return book, nil
}

// runNextCompensation pops the sweep's next entry, reports Compensating, and
// spawns its handler as a track (the runScopeHandler spawn shape). The sweep
// advances on the handler track's evEnded and aborts on its evFailed.
func (ls *loopState) runNextCompensation(ctx context.Context, sweep *compSweep) {
	if ls.stopping || len(sweep.queue) == 0 {
		ls.finishSweep(ctx, sweep)

		return
	}

	le := sweep.queue[0]
	sweep.queue = sweep.queue[1:]

	handler, ok := ls.compensationNodeByID(le.handlerID)
	if !ok {
		ls.inst.fail(errs.New(
			errs.M("compensation handler %q of %q isn't in the instance graph",
				le.handlerName, le.activityName),
			errs.C(errorClass, errs.ObjectNotFound)))
		ls.stopAll()

		return
	}

	ls.inst.report(observability.Fact{
		Kind:     observability.KindCompensation,
		Phase:    observability.PhaseCompensating,
		NodeID:   le.activityID,
		NodeName: le.activityName,
		Details: map[string]string{
			observability.AttrScopePath: string(sweep.path),
			observability.AttrOrdinal:   strconv.Itoa(le.ordinal),
		},
	})

	ht, err := newTrack(handler, ls.inst, nil)
	if err != nil {
		ls.inst.fail(errs.New(
			errs.M("couldn't start compensation handler %q", le.handlerName),
			errs.C(errorClass, errs.BulidingFailed),
			errs.E(err)))
		ls.stopAll()

		return
	}

	ht.scopePath = sweep.path
	if le.handlerEventSub {
		// the handler is a compensation Event Sub-Process: it parks as a
		// scope host and its fresh child scope is seeded with the snapshot at
		// open (shadowing reads; SRD-059 FR-4 for the composite form).
		ht.compScopeSeed = le.snapshot
	} else {
		// a boundary-handler activity reads the snapshot through its frame
		// inputs; its writes commit to the live scope (ADR-026 §2.5).
		ht.compFrameSeed = le.snapshot
	}

	ls.sweeps[ht.ID()] = &sweepRun{sweep: sweep, entry: le}
	ls.inst.trackCount.Add(1)
	ls.spawn(ctx, ht)
}

// compensationNodeByID resolves a handler node in the instance's cloned graph:
// the top-level node map first, then a walk into each composite's inner graph
// (a handler recorded from inside a Sub-Process — its boundary-linked activity
// or its compensation Event Sub-Process — lives there, not at the top level).
func (ls *loopState) compensationNodeByID(id string) (flow.Node, bool) {
	if n, ok := ls.inst.s.Nodes[id]; ok {
		return n, true
	}

	var find func(nodes []flow.Node) (flow.Node, bool)
	find = func(nodes []flow.Node) (flow.Node, bool) {
		for _, n := range nodes {
			if n.ID() == id {
				return n, true
			}

			if c, ok := n.(interface{ Nodes() []flow.Node }); ok {
				if f, found := find(c.Nodes()); found {
					return f, true
				}
			}
		}

		return nil, false
	}

	for _, n := range ls.inst.s.Nodes {
		if c, ok := n.(interface{ Nodes() []flow.Node }); ok {
			if f, found := find(c.Nodes()); found {
				return f, true
			}
		}
	}

	return nil, false
}

// sweepRun ties a spawned handler track to its sweep and the entry it
// compensates, for the evEnded/evFailed hooks.
type sweepRun struct {
	sweep *compSweep
	entry *ledgerEntry
}

// compensationTrackEnded advances the sweep when a handler track finishes
// (SRD-059 FR-6): a normal end reports Compensated and runs the next entry; a
// failure aborts the sweep — the handler's fault travels the ordinary Error
// chain (`Compensating → Failed`), and the thrower is resumed either way (its
// wait is over). Returns false when the track is not a sweep handler. Runs on
// the loop goroutine.
func (ls *loopState) compensationTrackEnded(
	ctx context.Context,
	t *track,
	failed bool,
) bool {
	run, ok := ls.sweeps[t.ID()]
	if !ok {
		return false
	}

	delete(ls.sweeps, t.ID())

	if failed {
		ls.finishSweep(ctx, run.sweep)

		return true
	}

	ls.inst.report(observability.Fact{
		Kind:     observability.KindCompensation,
		Phase:    observability.PhaseCompensated,
		NodeID:   run.entry.activityID,
		NodeName: run.entry.activityName,
		Details: map[string]string{
			observability.AttrScopePath: string(run.sweep.path),
			observability.AttrOrdinal:   strconv.Itoa(run.entry.ordinal),
		},
	})

	ls.runNextCompensation(ctx, run.sweep)

	return true
}

// finishSweep resumes a wait-for-completion thrower once the sweep has
// drained (or resolved to nothing / aborted): the loop delivers the
// completion sentinel through the standard parked-dispatch contract. On a
// stopping instance the resume is dropped — the teardown already closed the
// parked tracks' channels (the fireScopeHandler stopping discipline).
func (ls *loopState) finishSweep(ctx context.Context, sweep *compSweep) {
	// a Transaction-abort sweep: the ACID-like compensation barrier drained, so
	// finalize the abort — terminate the residual tracks and exit via the Cancel
	// boundary (SRD-061 FR-5 steps 2–3). Skipped while stopping: a tearing-down
	// instance abandons the exit.
	if sweep.txHost != nil {
		if !ls.stopping {
			ls.finalizeTransaction(ctx, sweep)
		}

		return
	}

	if ls.stopping || !sweep.wait || sweep.thrower == nil {
		return
	}

	ls.dispatchToParked(ctx, trackEvent{
		kind:  evDeliver,
		track: sweep.thrower,
		eDef:  newCompensationDone(),
	})

	sweep.thrower = nil // resume once
}

// reportUnresolvedCompensation logs a throw that resolved to nothing — an
// unknown or never-completed activityRef, or a scope-wide sweep over an empty
// ledger (SRD-059 FR-8). It is NOT a fault — execution continues; the fact
// echoes at Warn (the cross-cutting uncaught-events-always-log rule).
func (ls *loopState) reportUnresolvedCompensation(ev trackEvent) {
	details := map[string]string{
		observability.AttrScopePath: string(ev.track.scopePath),
	}
	if ev.compRef != "" {
		details["activity_ref"] = ev.compRef
	}

	ls.inst.report(observability.Fact{
		Kind:    observability.KindCompensation,
		Phase:   observability.PhaseUnresolved,
		Details: details,
	})
}

// seedFrameInputs loads a compensation snapshot into an execution frame's
// INPUT side (SRD-059 FR-4): reads resolve frame-first, and Commit pushes
// only outputs and puts — so the snapshot is a pure read surface. Runs on the
// handler track's goroutine.
func seedFrameInputs(f *scope.Frame, seed []data.Data) error {
	params := make([]*data.Parameter, 0, len(seed))

	for _, d := range seed {
		p, ok := d.(*data.Parameter)
		if !ok {
			return errs.New(
				errs.M("compensation snapshot datum %q isn't a parameter",
					d.Name()),
				errs.C(errorClass, errs.TypeCastingError))
		}

		params = append(params, p)
	}

	return f.InstantiateInputs(params)
}

// applyThrowPropagation routes the non-fault throw propagations — Escalation
// (SRD-058) and Compensation (SRD-059) — keeping the loop's apply dispatch
// under its complexity budget.
func (ls *loopState) applyThrowPropagation(ctx context.Context, ev trackEvent) {
	if ev.kind == evEscalate {
		ls.applyEscalate(ctx, ev)

		return
	}

	ls.applyCompensate(ctx, ev)
}
