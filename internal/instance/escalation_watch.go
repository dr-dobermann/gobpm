package instance

import (
	"context"

	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// applyEscalate resolves an escalation raised by a throw on ev.track (SRD-058
// FR-1/FR-2). It walks the track's scope chain to the innermost matching
// catcher; an escalation that misses up to the root is logged, never faulted
// (FR-4). Unlike applyFailed it does NOT tear down the throwing track — the
// token continues (an Escalation Intermediate Throw) or ends normally (an
// Escalation End Event) on its own, and only an interrupting catcher cancels
// its scope. Runs on the loop goroutine.
func (ls *loopState) applyEscalate(ctx context.Context, ev trackEvent) {
	ls.reportEscalationThrown(ev)

	if !ls.matchEscalationScopeChain(ctx, ev.track, ev.escCode) {
		ls.reportUnresolvedEscalation(ev)
	}
}

// reportEscalationThrown emits the Thrown fact at the throw site — the throwing
// node (the loop's position for ev.track) plus the code — the escalation peer of
// the fault's KindFault/Thrown (track.go). It opens the Thrown → Caught /
// Unresolved triple (NFR-5).
func (ls *loopState) reportEscalationThrown(ev trackEvent) {
	f := observability.Fact{
		Kind:    observability.KindEscalation,
		Phase:   observability.PhaseThrown,
		Details: map[string]string{observability.AttrEscalation: ev.escCode},
	}

	if node := ls.position[ev.track.ID()]; node != nil {
		f.NodeID = node.ID()
		f.NodeName = node.Name()
	}

	ls.inst.report(f)
}

// matchEscalationScopeChain walks the throwing track's enclosing composites,
// innermost first (§10.5.6 — the innermost enclosing catcher; ADR-006 §2.6,
// ADR-023 §2.6): at each open scope an Escalation boundary on the composite
// host whose escalationRef code matches catches. Escalation is non-critical, so
// a catch never faults the instance; and it is non-interrupting-capable
// (ADR-018): an interrupting boundary cancels the scope as a unit and the
// exception flow replaces the host's continuation (the Error idiom), while a
// non-interrupting one spawns a parallel token on the boundary's outgoing and
// the scope runs on. An empty escalationRef code on the boundary catches ANY
// escalation (catch-all, SRD-058 §4.3). Returns false when the chain misses up
// to the root. Runs on the loop goroutine.
func (ls *loopState) matchEscalationScopeChain(
	ctx context.Context,
	t *track,
	code string,
) bool {
	for path := t.scopePath; ; {
		entry, ok := ls.scopes[path]
		if !ok {
			return false // above every open scope — the root reached.
		}

		// an event-sub Escalation handler INSIDE this scope catches before the
		// Escalation boundary ON the scope host (§10.5.6 precedence — the inline
		// handler is the more inner catcher), mirroring matchErrorScopeChain.
		if ls.catchEscalationHandler(ctx, path, code) {
			return true
		}

		if bev := escalationBoundaryOn(entry.node, code); bev != nil {
			ls.catchEscalationBoundary(ctx, path, entry, bev, code)

			return true
		}

		path = entry.parent
	}
}

// catchEscalationHandler fires the event-sub Escalation handler armed at path,
// if one matches the code, and reports whether it caught (SRD-058 FR-6). A
// non-interrupting handler always catches — it forks a concurrent handler and
// the scope runs on. An interrupting handler catches unless the scope's
// interrupting budget is already spent (§10.5.6), in which case the escalation
// keeps propagating outward. It is a non-fault catch — runScopeHandler /
// runNonInterruptingHandler are the same dispatch fireScopeHandler uses.
func (ls *loopState) catchEscalationHandler(
	ctx context.Context,
	path scope.DataPath,
	code string,
) bool {
	w := ls.escalationHandlerAt(path, code)
	if w == nil {
		return false
	}

	if w.interrupting && ls.scopeInterrupted[path] {
		return false // budget spent — propagate to an outer catcher.
	}

	ls.inst.report(observability.Fact{
		Kind:     observability.KindEscalation,
		Phase:    observability.PhaseCaught,
		NodeID:   w.handler.ID(),
		NodeName: w.handler.Name(),
		Details:  map[string]string{observability.AttrEscalation: code},
	})
	ls.reportHandler(w, observability.PhaseFired)

	if w.interrupting {
		ls.runScopeHandler(ctx, w, nil)
	} else {
		ls.runNonInterruptingHandler(ctx, w, nil)
	}

	return true
}

// escalationHandlerAt returns the armed event-sub Escalation handler at path
// whose start's escalationRef catches the code (exact or catch-all, §4.3), or
// nil — the scope-chain peer of escalationBoundaryOn, mirroring errorHandlerAt.
func (ls *loopState) escalationHandlerAt(
	path scope.DataPath,
	code string,
) *scopeHandlerWatch {
	for _, w := range ls.scopeHandlers[path] {
		if eed, ok := w.def.(*events.EscalationEventDefinition); ok &&
			escalationMatches(eed, code) {
			return w
		}
	}

	return nil
}

// catchEscalationBoundary fires a matched Escalation boundary. Non-interrupting
// (cancelActivity=false, ADR-018): a parallel token is spawned on the boundary's
// outgoing and the host scope runs on undisturbed. Interrupting: the scope
// cancels as a unit and the exception flow replaces the host's continuation
// (the Error idiom). Either way it is a non-fault catch (SRD-058 FR-3).
func (ls *loopState) catchEscalationBoundary(
	ctx context.Context,
	path scope.DataPath,
	entry *scopeEntry,
	bev flow.BoundaryEvent,
	code string,
) {
	ls.inst.report(observability.Fact{
		Kind:     observability.KindEscalation,
		Phase:    observability.PhaseCaught,
		NodeID:   bev.ID(),
		NodeName: bev.Name(),
		Details:  map[string]string{observability.AttrEscalation: code},
	})

	if !bev.CancelActivity() {
		// non-interrupting: a parallel token on the boundary's outgoing; the
		// host scope is left running.
		ls.spawnForks(ctx,
			trackEvent{track: entry.host, flows: bev.Outgoing()})

		return
	}

	// interrupting: the scope cancels as a unit and the exception flow replaces
	// the host's continuation (the host is canceled).
	ls.cancelScope(path, observability.PhaseCanceled)
	ls.spawnForks(ctx,
		trackEvent{track: entry.host, flows: bev.Outgoing()})
	entry.host.cancel()
	ls.flipNotParked(entry.host)
	ls.disarmBoundaries(entry.host.ID())
}

// escalationBoundaryOn returns the node's Escalation boundary that catches an
// escalation with the given code (an exact escalationRef match, or a catch-all
// when the boundary's own code is empty — SRD-058 §4.3), or nil. The escalation
// peer of errorBoundaryOn.
func escalationBoundaryOn(node flow.Node, code string) flow.BoundaryEvent {
	host, ok := node.(boundaryHoster)
	if !ok {
		return nil
	}

	for _, en := range host.BoundaryEvents() {
		bev := en.(flow.BoundaryEvent)

		for _, d := range bev.Definitions() {
			if eed, ok := d.(*events.EscalationEventDefinition); ok &&
				escalationMatches(eed, code) {
				return bev
			}
		}
	}

	return nil
}

// escalationMatches reports whether a catcher's EscalationEventDefinition
// catches an escalation with the given code: an exact code match, or a
// catch-all when the catcher's own code is empty (SRD-058 §4.3).
func escalationMatches(
	eed *events.EscalationEventDefinition,
	code string,
) bool {
	catcher := eed.Escalation().Code()

	return catcher == "" || catcher == code
}

// reportUnresolvedEscalation logs an escalation that reached the scope-chain
// root with no matching catcher (SRD-058 FR-4). It is NOT a fault — execution
// continues; the fact echoes at Warn (a throw with no reachable catcher is a
// likely modeling mistake), so an unresolved escalation is always observable,
// never silently dropped.
func (ls *loopState) reportUnresolvedEscalation(ev trackEvent) {
	ls.inst.report(observability.Fact{
		Kind:    observability.KindEscalation,
		Phase:   observability.PhaseUnresolved,
		Details: map[string]string{observability.AttrEscalation: ev.escCode},
	})
}
