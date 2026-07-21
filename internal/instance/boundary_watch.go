package instance

import (
	"context"
	"errors"

	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// boundaryWatch is the hub-facing processor for one interrupting boundary event
// guarding a running activity. It is a loop-owned catch subscription over the
// host activity's execution window — not a node a token flows into. On a fire it
// does NOT touch instance state: it emits evBoundary to the per-instance loop
// (the boundary peer of track.ProcessEvent), which arbitrates the
// completion-vs-fire race and applies the interruption on its single-writer
// goroutine (SRD-029 FR-5/FR-8).
type boundaryWatch struct {
	host     *track
	boundary flow.BoundaryEvent
	def      flow.EventDefinition
	// loopOwned marks a watch with no hub registration — a Conditional
	// boundary (SRD-048 FR-15): its trigger source is the instance's own
	// commits, so it is armed as a condWatch in the loop's conditional
	// registry instead. It still lives in ls.watchers so armedFor and the
	// disarm lifecycle govern it, but disarm skips the hub unregister.
	loopOwned bool
}

// ID returns the watch's hub identity, unique per (host track, boundary,
// definition) so two boundaries on one activity — or one boundary's two
// definitions — register as distinct subscribers (eventproc.EventProcessor).
func (w *boundaryWatch) ID() string {
	return "boundary-watch:" + w.host.ID() + ":" +
		w.boundary.ID() + ":" + w.def.ID()
}

// ProcessEvent is called by a Timer/Signal producer on its OWN goroutine when the
// boundary's event fires. It hands the fire to the per-instance loop as evBoundary
// (carrying the guarded host track and the fired boundary node) and returns once
// enqueued, not once applied — the loop owns the interruption (eventproc.EventProcessor).
func (w *boundaryWatch) ProcessEvent(
	_ context.Context,
	eDef flow.EventDefinition,
) error {
	w.host.instance.emit(trackEvent{
		kind:  evBoundary,
		track: w.host,
		node:  w.boundary,
		eDef:  eDef,
	})

	return nil
}

// boundaryHoster is the narrow view of an activity that carries boundary events.
// Only activities expose it; other nodes (events, gateways) do not, so a type
// assertion against it is how the loop decides whether a node needs watchers.
type boundaryHoster interface {
	BoundaryEvents() []flow.EventNode
}

// armBoundaries registers a boundaryWatch for every boundary event guarding node
// — interrupting and non-interrupting alike — once the track t has arrived on it
// (SRD-029 FR-5). Non-activity nodes carry no boundaries and are skipped. A
// registration failure can't honor the activity's declared boundary, so it faults
// the instance (fail+stopAll), mirroring spawnForks' build-failure handling.
// fireBoundary discriminates interrupting vs non-interrupting on fire. Called only
// from the loop goroutine.
func (ls *loopState) armBoundaries(
	ctx context.Context,
	t *track,
	node flow.Node,
) {
	host, ok := node.(boundaryHoster)
	if !ok {
		return
	}

	var ws []*boundaryWatch

	for _, be := range host.BoundaryEvents() {
		// every entry was attached via AddBoundaryEvent(flow.BoundaryEvent), so the
		// cast cannot fail — use the panicking form (no unreachable error branch).
		bev := be.(flow.BoundaryEvent)

		for _, d := range bev.Definitions() {
			// An Error boundary is not a waiting catch — an error is not a published
			// trigger that arrives on the hub. It is matched against the failing
			// activity in the loop's evFailed handling (matchErrorBoundary, FR-9), so
			// it is never armed as a hub waiter (SRD-029 §4.4). An Escalation
			// boundary is the same: an escalation is not a hub-published trigger but
			// climbs the throwing execution's scope chain, resolved at the throw site
			// by matchEscalationScopeChain (SRD-058 FR-2/FR-5). A Compensation
			// boundary is not even a live subscription (ADR-006 §2.3) — it becomes
			// ELIGIBLE at the activity's completion, recorded into the completion
			// ledger and resolved at throw time (SRD-059 FR-3).
			if d.Type() == flow.TriggerError ||
				d.Type() == flow.TriggerEscalation ||
				d.Type() == flow.TriggerCompensation {
				continue
			}

			w := &boundaryWatch{host: t, boundary: bev, def: d}

			// A Conditional boundary is loop-owned (SRD-048 FR-15): no hub
			// registration — its condWatch is armed below, once the watch list
			// is in ls.watchers (armedFor must see it if the arm-time
			// evaluation fires immediately).
			if d.Type() == flow.TriggerConditional {
				w.loopOwned = true
				ws = append(ws, w)

				ls.reportBoundaryPhase(w, observability.PhaseArmed)

				continue
			}

			if err := ls.inst.RegisterEvent(w, d); err != nil {
				werr := errs.New(
					errs.M("arm boundary %q on activity %q failed",
						bev.ID(), node.ID()),
					errs.C(errorClass, errs.OperationFailed),
					errs.E(err))

				ls.inst.fail(werr)
				ls.stopAll()

				return
			}

			ws = append(ws, w)

			// The boundary now guards the activity's execution window (SRD-041
			// §3.4).
			ls.inst.report(observability.Fact{
				Kind:     observability.KindBoundary,
				Phase:    observability.PhaseArmed,
				NodeID:   bev.ID(),
				NodeName: bev.Name(),
				Details:  map[string]string{observability.AttrEventDefinitionID: d.ID()},
			})
		}
	}

	if len(ws) > 0 {
		ls.watchers[t.ID()] = ws
	}

	// Arm the conditional boundary subscriptions AFTER the watch list landed
	// in ls.watchers: an arm-time-true fire goes through fireBoundary, whose
	// armedFor guard reads that list (SRD-048 FR-9/FR-15).
	for _, w := range ws {
		if w.loopOwned && !ls.armCondBoundary(ctx, w) {
			return // evaluation failure — the instance is stopping
		}
	}
}

// reportBoundaryPhase emits one KindBoundary fact for a watch — the shared
// shape of the arm/disarm/fire reports (SRD-041 §3.4).
func (ls *loopState) reportBoundaryPhase(
	w *boundaryWatch,
	phase observability.Phase,
) {
	ls.inst.report(observability.Fact{
		Kind:     observability.KindBoundary,
		Phase:    phase,
		NodeID:   w.boundary.ID(),
		NodeName: w.boundary.Name(),
		Details:  map[string]string{observability.AttrEventDefinitionID: w.def.ID()},
	})
}

// disarmBoundaries unregisters every watch armed for track trackID and drops its
// entry — the activity's execution window has closed (the track moved off it,
// ended, or failed), so its boundaries no longer guard anything (SRD-029 FR-6).
// UnregisterEvent is idempotent, so a watch the hub already removed is a no-op.
// Called only from the loop goroutine.
func (ls *loopState) disarmBoundaries(trackID string) {
	for _, w := range ls.watchers[trackID] {
		// A loop-owned (Conditional) watch has no hub entry — its condWatch
		// is cleared below (SRD-048 FR-15).
		if w.loopOwned {
			ls.reportBoundaryPhase(w, observability.PhaseDisarmed)

			continue
		}

		// The hub owns the waiter's lifecycle; a miss (waiter/processor already
		// gone) is the expected idempotent case, not a fault — best-effort
		// (ADR-022 v.1 §2.3(2)): log at Debug with its error and move on.
		if err := ls.inst.UnregisterEvent(w, w.def.ID()); err != nil {
			ls.inst.Logger().Debug("boundary watch disarm failed",
				"track_id", trackID, "waiter_id", w.ID(),
				"event_definition_id", w.def.ID(), "error", err.Error())
		}

		// The activity's window closed — the boundary no longer guards it
		// (SRD-041 §3.4).
		ls.inst.report(observability.Fact{
			Kind:     observability.KindBoundary,
			Phase:    observability.PhaseDisarmed,
			NodeID:   w.boundary.ID(),
			NodeName: w.boundary.Name(),
			Details:  map[string]string{observability.AttrEventDefinitionID: w.def.ID()},
		})
	}

	delete(ls.watchers, trackID)
	ls.clearCondBoundaries(trackID)
}

// fireBoundary applies a boundary fire on the loop goroutine. The loop is the
// single writer, so it arbitrates the completion-vs-fire race: if the host's window
// already closed (its watchers were torn down between the watch emitting evBoundary
// and this point), the fire lost and is dropped (FR-8). Otherwise it continues the
// instance on the boundary's outgoing flow as a fresh track lineaged from the host,
// then discriminates on cancelActivity:
//
//   - interrupting: also cancel the guarded track (M2's per-track cancel) — its
//     execution is abandoned by the §3.7 checkpoint — and tear the watchers down
//     (one-shot).
//   - non-interrupting: the host runs on and the watch stays armed, so it can fire
//     again (multi-shot via the hub's retained signal waiter); it is disarmed when
//     the host completes (evMoved-off / evEnded).
//
// Called only from the loop goroutine.
func (ls *loopState) fireBoundary(ctx context.Context, ev trackEvent) {
	hostID := ev.track.ID()

	if !armedFor(ls.watchers[hostID], ev.node) {
		return // the host already completed and disarmed — the fire lost the race.
	}

	// ev.node is the boundaryWatch's own boundary (set in ProcessEvent), so the cast
	// cannot fail — use the panicking form to avoid an unreachable error branch.
	be := ev.node.(flow.BoundaryEvent)

	// shared interrupting budget (SRD-052 FR-6): an interrupting boundary on a
	// composite competes with the composite's Event Sub-Processes for the ONE
	// interruption its scope allows. If an event-sub already interrupted that
	// scope, this boundary fire is suppressed — the two cooperate rather than
	// double-fire (NFR-1).
	child, composite := ls.hostChildScope(ev.track)
	if be.CancelActivity() && composite && ls.scopeInterrupted[child] {
		return
	}

	// both kinds spawn a token on the boundary's outgoing (exception / parallel) flow.
	ls.spawnForks(ctx,
		trackEvent{track: ev.track, flows: ev.node.Outgoing()})

	// The boundary fired and continued the instance on its outgoing flow
	// (SRD-041 §3.4).
	ls.inst.report(observability.Fact{
		Kind:     observability.KindBoundary,
		Phase:    observability.PhaseFired,
		NodeID:   ev.node.ID(),
		NodeName: ev.node.Name(),
	})

	if be.CancelActivity() {
		// spend the scope's interrupting budget, so a later event-sub fire in
		// the same scope is suppressed (SRD-052 FR-6).
		if composite {
			ls.scopeInterrupted[child] = true
		}

		// a composite host's open child scope dies with it (ADR-023 §2.5):
		// cancel the subtree before the host, so the inner tracks stop and
		// the scope closes as a unit.
		ls.cancelHostScope(ev.track)

		ev.track.cancel()
		ls.disarmBoundaries(hostID)
	}
}

// hostChildScope returns the open child-scope path of a composite host and
// whether the host is a composite at all — the scope path both the interrupting
// boundary (the shared budget, SRD-052 FR-6) and cancelHostScope reference.
func (ls *loopState) hostChildScope(host *track) (scope.DataPath, bool) {
	node, ok := ls.position[host.ID()].(scopeHost)
	if !ok {
		return "", false
	}

	child, err := host.scopePath.Append(scopeSegment(node))
	if err != nil {
		return "", false // an unopenable path can't hold an open scope.
	}

	return child, true
}

// cancelHostScope cancels the open child scope of a composite host, if
// any — the interrupting-fire and error-catch companion (SRD-049 FR-10).
// Runs on the loop goroutine.
func (ls *loopState) cancelHostScope(host *track) {
	// a parallel Multi-Instance host has N distinct instance scopes, not the
	// single default sp-<id> segment hostChildScope computes — tear down all of
	// them (SRD-056.A FR-10).
	if grp, ok := ls.miGroups[host.ID()]; ok {
		ls.cancelParallelGroup(grp)

		return
	}

	child, ok := ls.hostChildScope(host)
	if !ok {
		return
	}

	ls.cancelScope(child, observability.PhaseCanceled)
}

// matchErrorBoundary handles the Error-catch path on the loop goroutine (SRD-029
// FR-9 / §4.4): an Error boundary is not a waiting catch but a match against the
// failing activity at the moment the track fails. If the track failed with a typed
// BpmnError and the activity it failed on carries an Error boundary whose errorRef
// code equals the BpmnError's Code, the loop routes to that boundary's outgoing
// (exception) flow — the failed track being the canceled guarded activity — and
// returns true so the caller does NOT fault the instance. A plain (untyped) error,
// a failing node that holds no boundaries (e.g. an Error End Event), or no code
// match returns false: the instance-fault path (§1.3) is unchanged. Called only
// from the loop goroutine.
func (ls *loopState) matchErrorBoundary(ctx context.Context, t *track) bool {
	var be *events.BpmnError
	if !errors.As(t.lastErr, &be) {
		return false // an untyped failure → fault, as before.
	}

	// position holds the node the track failed on; a node that carries
	// boundaries may catch, one that carries none (an end event, a gateway)
	// cannot — the BpmnError then escapes as Uncaught below.
	if host, ok := ls.position[t.ID()].(boundaryHoster); ok {
		for _, en := range host.BoundaryEvents() {
			// every entry was attached as a flow.BoundaryEvent — panicking form.
			bev := en.(flow.BoundaryEvent)

			for _, d := range bev.Definitions() {
				eed, ok := d.(*events.ErrorEventDefinition)
				if !ok || eed.Error().ErrorCode() != be.Code {
					continue
				}

				// The boundary caught the thrown fault (SRD-041 §3.4).
				ls.inst.report(observability.Fact{
					Kind:     observability.KindFault,
					Phase:    observability.PhaseCaught,
					NodeID:   bev.ID(),
					NodeName: bev.Name(),
					Details:  map[string]string{observability.AttrError: be.Code},
				})

				ls.spawnForks(ctx,
					trackEvent{track: t, flows: bev.Outgoing()})

				return true
			}
		}
	}

	return false
}

// reportUncaught emits the Uncaught fault fact once the WHOLE catch chain —
// the failing activity's own boundary and every enclosing composite's
// (SRD-049 FR-12) — missed; the caller faults the instance, whose separate
// InstanceState/Failed record carries the operator-facing Error echo. An
// untyped failure carries no code detail.
func (ls *loopState) reportUncaught(t *track) {
	details := map[string]string{}

	var be *events.BpmnError
	if errors.As(t.lastErr, &be) {
		details[observability.AttrError] = be.Code
	}

	ls.inst.report(observability.Fact{
		Kind:    observability.KindFault,
		Phase:   observability.PhaseUncaught,
		Details: details,
	})
}

// matchErrorScopeChain walks the failing track's enclosing composites,
// innermost first (§10.5.1/§10.5.7 — the innermost enclosing catcher;
// ADR-023 §2.6): at each open scope, an Error boundary on the composite
// host whose errorRef code matches catches — the scope cancels as a unit
// and the exception flow continues in the HOST's context (the host itself
// is canceled: the exception routing replaces its continuation). Fired
// directly rather than through fireBoundary — an Error boundary is never
// armed as a watch (SRD-029 §4.4), so armedFor cannot guard it. Returns
// false when the chain misses up to the root. Runs on the loop goroutine.
func (ls *loopState) matchErrorScopeChain(ctx context.Context, t *track) bool {
	var be *events.BpmnError
	if !errors.As(t.lastErr, &be) {
		return false
	}

	for path := t.scopePath; ; {
		entry, ok := ls.scopes[path]
		if !ok {
			return false // above every open scope — the root reached.
		}

		// an event-sub Error handler INSIDE this scope catches before the Error
		// boundary ON the scope host (§10.5.6 precedence — the inline handler is
		// the more inner catcher): it absorbs the fault and the scope runs the
		// handler (SRD-052 FR-8/FR-9). Skipped once the scope is already
		// interrupted (its budget is spent).
		if w := ls.errorHandlerAt(path, be.Code); w != nil &&
			!ls.scopeInterrupted[path] {
			ls.inst.report(observability.Fact{
				Kind:     observability.KindFault,
				Phase:    observability.PhaseCaught,
				NodeID:   w.handler.ID(),
				NodeName: w.handler.Name(),
				Details:  map[string]string{observability.AttrError: be.Code},
			})
			ls.reportHandler(w, observability.PhaseFired)
			ls.runScopeHandler(ctx, w, nil)

			return true
		}

		if bev := errorBoundaryOn(entry.node, be.Code); bev != nil {
			ls.inst.report(observability.Fact{
				Kind:     observability.KindFault,
				Phase:    observability.PhaseCaught,
				NodeID:   bev.ID(),
				NodeName: bev.Name(),
				Details:  map[string]string{observability.AttrError: be.Code},
			})

			ls.cancelScope(path, observability.PhaseCanceled)

			// the exception flow replaces the host's continuation.
			ls.spawnForks(ctx,
				trackEvent{track: entry.host, flows: bev.Outgoing()})
			entry.host.cancel()
			ls.flipNotParked(entry.host)
			ls.disarmBoundaries(entry.host.ID())

			return true
		}

		path = entry.parent
	}
}

// errorHandlerAt returns the armed event-sub Error handler at path whose
// triggered start's errorRef code matches, or nil — the scope-chain peer of
// errorBoundaryOn (SRD-052 FR-8). An Error handler is armed with no hub waiter
// (armScopeHandlers), so like the Error boundary it is resolved here, at the
// throw site, not delivered.
func (ls *loopState) errorHandlerAt(
	path scope.DataPath,
	code string,
) *scopeHandlerWatch {
	for _, w := range ls.scopeHandlers[path] {
		if eed, ok := w.def.(*events.ErrorEventDefinition); ok &&
			eed.Error().ErrorCode() == code {
			return w
		}
	}

	return nil
}

// errorBoundaryOn returns the node's Error boundary whose errorRef code
// matches, or nil — the code-matching scan matchErrorBoundary uses, shared
// with the scope-chain walk.
func errorBoundaryOn(node flow.Node, code string) flow.BoundaryEvent {
	host, ok := node.(boundaryHoster)
	if !ok {
		return nil
	}

	for _, en := range host.BoundaryEvents() {
		bev := en.(flow.BoundaryEvent)

		for _, d := range bev.Definitions() {
			if eed, ok := d.(*events.ErrorEventDefinition); ok &&
				eed.Error().ErrorCode() == code {
				return bev
			}
		}
	}

	return nil
}

// armedFor reports whether node is still among the watches armed for a host —
// the loop's race guard against a fire that arrives after the host's window
// closed (its watchers were already torn down).
func armedFor(ws []*boundaryWatch, node flow.Node) bool {
	for _, w := range ws {
		if w.boundary.ID() == node.ID() {
			return true
		}
	}

	return false
}
