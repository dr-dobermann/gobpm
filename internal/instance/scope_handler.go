package instance

import (
	"context"
	"slices"

	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// scopeHandlerWatch is one armed Event Sub-Process handler (SRD-052 FR-5): the
// scope-level twin of boundaryWatch. It carries the event-sub node, its
// triggered start, and the start's trigger definition; on a hub fire its
// ProcessEvent emits evScopeHandlerFire to the loop (the boundaryWatch idiom).
// A Conditional-start handler is loopOwned — no hub waiter; its condWatch is
// armed on the loop and fires through the conditional sweep instead.
type scopeHandlerWatch struct {
	inst      *Instance
	handler   flow.Node            // the event-sub SubProcess node
	start     flow.EventNode       // its single triggered start
	def       flow.EventDefinition // the start's trigger definition
	path      scope.DataPath       // the enclosing scope this handler guards
	loopOwned bool                 // Conditional — armed via a condWatch, not the hub
	// interrupting is the triggered start's isInterrupting (SRD-053): an
	// interrupting fire runs cancel-and-run; a non-interrupting fire forks a
	// concurrent handler instance and leaves the watch armed.
	interrupting bool
}

// ID returns the hub-waiter id for this handler (its start node id).
func (w *scopeHandlerWatch) ID() string {
	return w.start.ID()
}

// ProcessEvent is called by the hub when the handler's trigger fires: it emits
// evScopeHandlerFire to the loop, where the fire is arbitrated against the
// scope's interrupting budget and run (SRD-052 FR-7; M3). Runs off the hub
// goroutine — it only emits, never mutates loop state.
func (w *scopeHandlerWatch) ProcessEvent(
	_ context.Context,
	eDef flow.EventDefinition,
) error {
	w.inst.emit(trackEvent{
		kind: evScopeHandlerFire,
		node: w.handler,
		eDef: eDef,
	})

	return nil
}

// armScopeHandlers arms every Event Sub-Process declared directly in nodes at
// the scope path (SRD-052 FR-5): the boundary-watch pattern lifted from an
// activity's window to a scope's window. Called when a scope opens (the
// enclosing composite's inner nodes) and at instance start (the process's
// top-level nodes at the root path). A scope with no event sub-processes arms
// nothing (NFR-3). Runs on the loop goroutine.
func (ls *loopState) armScopeHandlers(
	ctx context.Context,
	nodes []flow.Node,
	path scope.DataPath,
) {
	if ls.stopping {
		return
	}

	var ws []*scopeHandlerWatch

	for _, n := range nodes {
		if !isEventSubHandler(n) {
			continue
		}

		start, def, ok := triggeredStartOf(n)
		if !ok {
			// Validate guarantees exactly one triggered start with one
			// definition, so this is unreachable for a validated model.
			continue
		}

		// the triggered start's mode drives the fire (SRD-053); default
		// interrupting if the node somehow doesn't expose it (unreachable for a
		// validated StartEvent).
		interrupting := true
		if si, ok := start.(interface{ IsInterrupting() bool }); ok {
			interrupting = si.IsInterrupting()
		}

		w := &scopeHandlerWatch{
			inst: ls.inst, handler: n, start: start, def: def, path: path,
			interrupting: interrupting,
		}

		// A Conditional or Error start is not a hub waiter: the Conditional is
		// loop-owned (armed as a condWatch below); an Error is caught by the
		// §2.6 scope-chain walk at the throw site (FR-8, M3), never armed.
		switch def.Type() {
		case flow.TriggerConditional:
			w.loopOwned = true

		case flow.TriggerError:
			// no waiter — the chain catches it (M3).
			ws = append(ws, w)
			ls.reportHandler(w, observability.PhaseArmed)

			continue

		default:
			if err := ls.inst.RegisterEvent(w, def); err != nil {
				ls.inst.fail(errs.New(
					errs.M("arm event sub-process handler %q failed", n.ID()),
					errs.C(errorClass, errs.OperationFailed),
					errs.E(err)))
				ls.stopAll()

				return
			}
		}

		ws = append(ws, w)
		ls.reportHandler(w, observability.PhaseArmed)
	}

	if len(ws) == 0 {
		return
	}

	ls.scopeHandlers[path] = append(ls.scopeHandlers[path], ws...)

	// Arm the Conditional-start subscriptions AFTER the watch list landed, so
	// an arm-time-true edge that fires immediately sees a consistent registry
	// (the armCondBoundary discipline).
	for _, w := range ws {
		if w.loopOwned {
			ls.armCondScopeHandler(ctx, w)
		}
	}
}

// disarmScopeHandlers tears down every handler armed at path (SRD-052 FR-5):
// the scope drained, was canceled, or its interrupting budget closed, so its
// handlers no longer guard anything. Best-effort hub unregister (a miss is the
// idempotent case). Runs on the loop goroutine.
func (ls *loopState) disarmScopeHandlers(path scope.DataPath) {
	for _, w := range ls.scopeHandlers[path] {
		if !w.loopOwned {
			if err := ls.inst.UnregisterEvent(w, w.def.ID()); err != nil {
				ls.inst.Logger().Debug("scope handler disarm failed",
					"scope_path", string(path), "waiter_id", w.ID(),
					"event_definition_id", w.def.ID(), "error", err.Error())
			}
		}

		ls.reportHandler(w, observability.PhaseDisarmed)
	}

	delete(ls.scopeHandlers, path)
	ls.clearCondScopeHandlers(path)
}

// reportHandler emits one Boundary-family fact for a scope handler's
// arm/disarm/fire lifecycle (SRD-052 FR-10) — the event-sub node, its trigger
// definition, and the scope path.
func (ls *loopState) reportHandler(
	w *scopeHandlerWatch,
	phase observability.Phase,
) {
	ls.inst.report(observability.Fact{
		Kind:     observability.KindBoundary,
		Phase:    phase,
		NodeID:   w.handler.ID(),
		NodeName: w.handler.Name(),
		Details: map[string]string{
			observability.AttrEventDefinitionID: w.def.ID(),
			observability.AttrScopePath:         string(w.path),
		},
	})
}

// fireScopeHandler applies an interrupting Event Sub-Process fire on the loop
// goroutine (SRD-052 FR-7) — the scope-level peer of fireBoundary. A late fire
// whose handler already disarmed (its scope closed, the budget spent) is void,
// the armedFor race guard one scope up. Otherwise it arbitrates the fire against
// the scope's shared interrupting budget (FR-6) and runs the cancel-and-run.
func (ls *loopState) fireScopeHandler(ctx context.Context, ev trackEvent) {
	if ls.stopping {
		return
	}

	w := ls.handlerWatchFor(ev.node)
	if w == nil {
		return
	}

	if ls.scopeInterrupted[w.path] {
		// an interrupting handler (event-sub or boundary) already fired in this
		// scope — the budget is spent, this fire is suppressed (FR-6).
		return
	}

	ls.reportHandler(w, observability.PhaseFired)
	ls.runScopeHandler(ctx, w, ev.eDef)
}

// runScopeHandler executes the interrupting cancel-and-run for a fired handler
// (SRD-052 FR-7), shared by the trigger-fire path (fireScopeHandler) and the
// Error-chain catch (matchErrorScopeChain, FR-8). It spends the scope's
// interrupting budget, binds the trigger's payload into the enclosing scope (the
// handler reads it by walk-up), spawns the handler as a track in that scope
// BEFORE canceling the siblings — so the enclosing scope's drain counter never
// prematurely hits zero — then closes the scope's remaining handlers. Runs on
// the loop goroutine.
func (ls *loopState) runScopeHandler(
	ctx context.Context,
	w *scopeHandlerWatch,
	payload flow.EventDefinition,
) {
	ls.scopeInterrupted[w.path] = true

	// the handler's own child scope (E/sp-handler): kept out of the sibling
	// cancel. spawn opens it synchronously for a born-parked composite, so it
	// can already be open by the time interruptScopeSiblings runs.
	childPrefix, err := w.path.Append(scopeSegment(w.handler))
	if err != nil {
		ls.inst.fail(err)
		ls.stopAll()

		return
	}

	// bind the trigger's payload into the enclosing scope, so the handler's
	// inner nodes observe the event data by walking up from their own child
	// scope (FR-7; one scope down from a born-from-event instance's root bind).
	if payload != nil {
		if err = ls.inst.sc.bindEventPayloadAt(w.path, payload); err != nil {
			ls.inst.fail(err)
			ls.stopAll()

			return
		}
	}

	// spawn the handler as a track in the enclosing scope; it parks as a
	// scopeHost and opens its own child scope (onScopeOpen → seedScope →
	// handlerSeeds, the triggered start treated as fired). spawn's incScope
	// counts it into the enclosing scope before the canceled siblings decrement
	// it, so the scope stays open until the handler itself drains.
	ht, err := newTrack(w.handler, ls.inst, nil)
	if err != nil {
		ls.inst.fail(errs.New(
			errs.M("couldn't start event sub-process %q", w.handler.ID()),
			errs.C(errorClass, errs.BulidingFailed),
			errs.E(err)))
		ls.stopAll()

		return
	}

	ht.scopePath = w.path
	ls.inst.trackCount.Add(1)
	ls.spawn(ctx, ht)

	// cancel the enclosing scope's other tracks — its data plane STAYS OPEN, so
	// the handler runs in the parent's data context (FR-7 step 1); the handler
	// track and its own child scope are kept.
	ls.interruptScopeSiblings(w.path, ht.ID(), childPrefix)

	// the scope is interrupted — its other handlers no longer guard it, and the
	// fired handler's own waiter is one-shot (FR-6).
	ls.disarmScopeHandlers(w.path)
}

// interruptScopeSiblings stops every live track under path except the handler
// track keepID and its own child subtree keepPrefix, and closes the nested
// scopes among the canceled siblings — leaving the enclosing scope's own data
// plane and entry (and the handler's freshly-opened scope) intact (SRD-052
// FR-7). It mirrors cancelScope but preserves the scope itself: the handler runs
// on in it, and the stopped siblings decrement the scope's drain counter through
// their own evEnded (each goroutine returns and emits it), so the scope
// completes only once the handler also drains. A canceled nested track's later
// evEnded finds its entry already gone and no-ops, so no stray host-resume
// fires. Runs on the loop goroutine.
func (ls *loopState) interruptScopeSiblings(
	path scope.DataPath,
	keepID string,
	keepPrefix scope.DataPath,
) {
	for _, t := range ls.inst.tracks {
		if t.ID() == keepID || underScope(t.scopePath, keepPrefix) ||
			!underScope(t.scopePath, path) {
			continue
		}

		t.stop()
		t.cancel()
		ls.flipNotParked(t)
		ls.disarmBoundaries(t.ID())
	}

	// close the nested scopes STRICTLY under path (the siblings' own
	// composites), deepest-first; path itself and the handler's own child
	// scope (keepPrefix) are preserved.
	sub := []scope.DataPath{}

	for p := range ls.scopes {
		if len(p) > len(path) && underScope(p, path) &&
			!underScope(p, keepPrefix) {
			sub = append(sub, p)
		}
	}

	slices.SortFunc(sub, func(a, b scope.DataPath) int {
		return len(b) - len(a)
	})

	for _, p := range sub {
		entry := ls.scopes[p]
		delete(ls.scopes, p)
		ls.disarmScopeHandlers(p)

		if err := ls.inst.sc.plane.CloseScope(p); err != nil {
			ls.inst.Logger().Debug("interrupted-scope close failed",
				"scope_path", string(p), "error", err.Error())
		}

		ls.reportScope(observability.PhaseCanceled, entry.node, p)
	}
}

// handlerWatchFor finds the armed watch for an event-sub node across every open
// scope, or nil if it is no longer armed.
func (ls *loopState) handlerWatchFor(node flow.Node) *scopeHandlerWatch {
	for _, ws := range ls.scopeHandlers {
		for _, w := range ws {
			if w.handler.ID() == node.ID() {
				return w
			}
		}
	}

	return nil
}

// disarmAllScopeHandlers tears down every armed handler across all scopes — the
// instance is terminating (drop), so no handler guards anything (SRD-052 FR-5).
func (ls *loopState) disarmAllScopeHandlers() {
	for path := range ls.scopeHandlers {
		ls.disarmScopeHandlers(path)
	}
}

// rootNodes returns the process's top-level nodes as a slice — the source for
// arming the instance-root Event Sub-Process handlers.
func rootNodes(inst *Instance) []flow.Node {
	if inst.s == nil {
		return nil
	}

	nodes := make([]flow.Node, 0, len(inst.s.Nodes))
	for _, n := range inst.s.Nodes {
		nodes = append(nodes, n)
	}

	return nodes
}

// handlerSeeds returns the seed nodes for an Event Sub-Process scope: the
// targets of its triggered start's outgoing flows — the start is treated as
// already fired, so its successors run (SRD-052 FR-7). Empty when the start has
// no outgoing (a degenerate handler).
func handlerSeeds(sh scopeHost) []flow.Node {
	start, _, ok := triggeredStartOf(sh)
	if !ok {
		return nil
	}

	targets := make([]flow.Node, 0, len(start.Outgoing()))
	for _, f := range start.Outgoing() {
		targets = append(targets, f.Target().Node())
	}

	return targets
}

// triggeredStartOf returns the single triggered Start Event of an event
// sub-process node and its one trigger definition. ok is false if the node is
// not a container or its shape is unexpected (Validate prevents that for a
// registered model).
func triggeredStartOf(n flow.Node) (flow.EventNode, flow.EventDefinition, bool) {
	c, ok := n.(interface{ Nodes() []flow.Node })
	if !ok {
		return nil, nil, false
	}

	for _, inner := range c.Nodes() {
		en, ok := inner.(flow.EventNode)
		if !ok || en.EventClass() != flow.StartEventClass {
			continue
		}

		defs := en.Definitions()
		if len(defs) == 1 {
			return en, defs[0], true
		}
	}

	return nil, nil, false
}
