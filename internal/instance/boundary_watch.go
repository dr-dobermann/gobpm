package instance

import (
	"context"
	"errors"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
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
func (ls *loopState) armBoundaries(t *track, node flow.Node) {
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
			// it is never armed as a hub waiter (SRD-029 §4.4).
			if d.Type() == flow.TriggerError {
				continue
			}

			w := &boundaryWatch{host: t, boundary: bev, def: d}

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
		}
	}

	if len(ws) > 0 {
		ls.watchers[t.ID()] = ws
	}
}

// disarmBoundaries unregisters every watch armed for track trackID and drops its
// entry — the activity's execution window has closed (the track moved off it,
// ended, or failed), so its boundaries no longer guard anything (SRD-029 FR-6).
// UnregisterEvent is idempotent, so a watch the hub already removed is a no-op.
// Called only from the loop goroutine.
func (ls *loopState) disarmBoundaries(trackID string) {
	for _, w := range ls.watchers[trackID] {
		// the hub owns the waiter's lifecycle; a miss means it is already gone.
		_ = ls.inst.UnregisterEvent(w, w.def.ID())
	}

	delete(ls.watchers, trackID)
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

	// both kinds spawn a token on the boundary's outgoing (exception / parallel) flow.
	ls.spawnForks(ctx,
		trackEvent{track: ev.track, flows: ev.node.Outgoing()})

	if be.CancelActivity() {
		ev.track.cancel()
		ls.disarmBoundaries(hostID)
	}
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

	// position holds the node the track failed on; a node that carries no
	// boundaries (an end event, a gateway) cannot catch — fault.
	host, ok := ls.position[t.ID()].(boundaryHoster)
	if !ok {
		return false
	}

	for _, en := range host.BoundaryEvents() {
		// every entry was attached as a flow.BoundaryEvent — panicking form.
		bev := en.(flow.BoundaryEvent)

		for _, d := range bev.Definitions() {
			eed, ok := d.(*events.ErrorEventDefinition)
			if !ok || eed.Error().ErrorCode() != be.Code {
				continue
			}

			ls.spawnForks(ctx,
				trackEvent{track: t, flows: bev.Outgoing()})

			return true
		}
	}

	return false
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
