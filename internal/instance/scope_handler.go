package instance

import (
	"context"

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
	handler   flow.Node          // the event-sub SubProcess node
	start     flow.EventNode     // its single triggered start
	def       flow.EventDefinition // the start's trigger definition
	path      scope.DataPath     // the enclosing scope this handler guards
	loopOwned bool               // Conditional — armed via a condWatch, not the hub
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

		w := &scopeHandlerWatch{
			inst: ls.inst, handler: n, start: start, def: def, path: path,
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

// fireScopeHandler applies a scope-handler fire on the loop goroutine (SRD-052
// FR-7). This slice (M2) records the fire; the interrupting cancel-and-run —
// the shared budget, cancelScope, and running the handler in the scope — is the
// next milestone (M3).
func (ls *loopState) fireScopeHandler(_ context.Context, ev trackEvent) {
	w := ls.handlerWatchFor(ev.node)
	if w == nil {
		// the handler already disarmed (its scope closed / the budget spent) —
		// a late fire is void.
		return
	}

	ls.reportHandler(w, observability.PhaseFired)
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
