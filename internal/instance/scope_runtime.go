package instance

import (
	"context"
	"slices"

	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// scopeHost is the runtime view of a composite node — an activity that
// contains its own graph (the embedded Sub-Process, ADR-023 §2.2). The
// capability assert keeps the runtime free of model-package imports, the
// boundaryHoster idiom.
type scopeHost interface {
	flow.ActivityNode

	Nodes() []flow.Node
}

// scopeEntry is one open nested scope, loop-owned (SRD-049 FR-9): the
// parked host resumes when active drains to zero; a queued second host (a
// parallel sibling arrived while the scope is open, §4.4 of the
// accompanying SRD) reopens the scope after the close.
type scopeEntry struct {
	host   *track
	node   flow.Node
	parent scope.DataPath
	queue  []*track
	active int
}

// scopeDoneTrigger is the internal trigger of the scope-completion
// delivery — not a BPMN trigger; it never leaves the engine.
const scopeDoneTrigger flow.EventTrigger = "gobpm:scope-done"

// scopeDone is the synthetic completion the loop delivers to a parked
// composite host when its scope drains (the task/job completion idiom).
type scopeDone struct {
	foundation.BaseElement
}

// newScopeDone mints one completion sentinel.
func newScopeDone() *scopeDone {
	return &scopeDone{BaseElement: *foundation.MustBaseElement()}
}

// Type returns the internal scope-completion trigger.
func (sd *scopeDone) Type() flow.EventTrigger { return scopeDoneTrigger }

// GetItemsList returns no payload — the delivery itself is the signal.
func (sd *scopeDone) GetItemsList() []*data.ItemDefinition { return nil }

// scopeSegment derives the child path segment from the composite node —
// the node's ID is stable across clones and unique in the graph, unlike
// its name (SRD-049 §4.3).
func scopeSegment(node flow.Node) string {
	return "sp-" + node.ID()
}

// onScopeOpen opens the nested scope for a parked composite host (SRD-049
// FR-8): derive the child path, open the data-plane scope, register the
// entry, and seed the inner tracks per the validated shape. A second host
// arriving while the scope is open queues (one DataPath cannot hold two
// concurrent scopes; multi-instance owns real parallelism — ADR-023 §2.8).
// Runs on the loop goroutine.
func (ls *loopState) onScopeOpen(ctx context.Context, host *track, node flow.Node) {
	if ls.stopping {
		return
	}

	sh, ok := node.(scopeHost)
	if !ok {
		// checkNodeType only routes scopeHost nodes here; a mismatch is a
		// corrupt graph.
		ls.inst.fail(errs.New(
			errs.M("scope open for a non-composite node %q", node.ID()),
			errs.C(errorClass, errs.TypeCastingError)))
		ls.stopAll()

		return
	}

	// a host may override the child segment (SRD-053): a non-interrupting
	// Event Sub-Process handler carries a unique per-fire segment so concurrent
	// instances of the same node open distinct scopes; every normal composite
	// uses scopeSegment(node).
	seg := scopeSegment(node)
	if host.scopeSeg != "" {
		seg = host.scopeSeg
	}

	child, err := host.scopePath.Append(seg)
	if err != nil {
		ls.inst.fail(err)
		ls.stopAll()

		return
	}

	if entry, open := ls.scopes[child]; open {
		// re-entry while open — queue this host; it reopens after the close.
		entry.queue = append(entry.queue, host)

		return
	}

	if err := ls.inst.sc.plane.OpenScope(child); err != nil {
		ls.inst.fail(errs.New(
			errs.M("couldn't open scope %q for sub-process %q",
				string(child), node.ID()),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err)))
		ls.stopAll()

		return
	}

	// the host parked on its evtCh — record it parked-and-undelivered so
	// the drain's synthetic completion can dispatch to it (the
	// onTaskWaiting discipline); idempotent for the born-parked path.
	ls.waiting[host.ID()] = struct{}{}

	entry := &scopeEntry{host: host, node: node, parent: host.scopePath}
	ls.scopes[child] = entry

	ls.reportScope(observability.PhaseOpened, node, child)

	// a non-interrupting handler instance binds its OWN trigger payload into its
	// own child scope (SRD-053 FR-4) — isolated from concurrent instances, read
	// by its inner nodes directly; before seeding so the seeds observe it. The
	// interrupting handler binds at the enclosing scope instead (unchanged).
	if host.bornPayload != nil {
		if err := ls.inst.sc.bindEventPayloadAt(child, host.bornPayload); err != nil {
			ls.inst.fail(err)
			ls.stopAll()

			return
		}
	}

	ls.seedScope(ctx, sh, child)

	// arm the scope's Event Sub-Process handlers while it is open (SRD-052
	// FR-5) — the boundary-watch pattern at scope granularity.
	ls.armScopeHandlers(ctx, sh.Nodes(), child)
}

// seedScope spawns the inner entry tracks per the ADR-023 §2.3 validated
// shape: the unique None Start Event when present, otherwise every
// flow-less inner activity/gateway. Runs on the loop goroutine — the
// spawn path records born-parked inner waiters and arms boundaries as for
// any track.
func (ls *loopState) seedScope(
	ctx context.Context,
	sh scopeHost,
	child scope.DataPath,
) {
	// An Event Sub-Process scope is entered by its FIRED triggered start, not
	// seeded normally: seed from the start's outgoing targets (the start
	// treated as fired), so the handler's inner flow runs (SRD-052 FR-7).
	seeds := scopeSeeds(sh)
	if isEventSubHandler(sh) {
		seeds = handlerSeeds(sh)
	}

	for _, n := range seeds {
		nt, err := newTrack(n, ls.inst, nil)
		if err != nil {
			ls.inst.fail(errs.New(
				errs.M("couldn't seed sub-process scope %q", string(child)),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(err)))
			ls.stopAll()

			return
		}

		// the seed belongs to the child scope; set pre-spawn on the loop
		// goroutine, before the run goroutine exists (the position-seeding
		// discipline).
		nt.scopePath = child

		ls.inst.trackCount.Add(1)
		ls.inst.tracks[nt.ID()] = nt
		ls.spawn(ctx, nt)

		if ls.stopping {
			nt.stop()
		}
	}
}

// scopeSeeds returns the inner entry nodes per the validated shape: the
// None start when present (validation guarantees uniqueness), else every
// flow-less inner activity/gateway (§13.3.4; boundary events are never
// entries).
func scopeSeeds(sh scopeHost) []flow.Node {
	var flowless []flow.Node

	for _, n := range sh.Nodes() {
		// an Event Sub-Process is a scope-armed handler, not an entry
		// (ADR-023 v.2 §2.10): it is armed when the scope opens, never seeded
		// (SRD-052 FR-3).
		if isEventSubHandler(n) {
			continue
		}

		if en, ok := n.(flow.EventNode); ok &&
			en.EventClass() == flow.StartEventClass {
			return []flow.Node{n}
		}

		if _, isBoundary := n.(flow.BoundaryEvent); isBoundary {
			continue
		}

		if len(n.Incoming()) == 0 && n.NodeType() != flow.EventNodeType {
			flowless = append(flowless, n)
		}
	}

	return flowless
}

// isEventSubHandler reports whether node is an Event Sub-Process — a
// scope-armed handler skipped by every entry-seeding path (the top-level
// createTracks and the scope-open scopeSeeds), armed instead (SRD-052).
func isEventSubHandler(node flow.Node) bool {
	h, ok := node.(interface{ IsEventSubProcess() bool })

	return ok && h.IsEventSubProcess()
}

// incScope counts a spawned track into its scope's drain accounting; root
// tracks have no entry and cost nothing (NFR-1). Runs on the loop
// goroutine.
func (ls *loopState) incScope(t *track) {
	if entry, ok := ls.scopes[t.scopePath]; ok {
		entry.active++
	}
}

// decScope counts a terminal track out of its scope; at zero the scope
// drained (§13.3.4 — no tokens remain) and completes. Runs on the loop
// goroutine.
func (ls *loopState) decScope(ctx context.Context, t *track) {
	entry, ok := ls.scopes[t.scopePath]
	if !ok {
		return
	}

	entry.active--

	if entry.active > 0 {
		return
	}

	ls.completeScope(ctx, t.scopePath, entry)
}

// completeScope closes a drained scope and resumes its parked host with
// the synthetic completion (SRD-049 FR-9); a queued re-entry host reopens
// the scope afterwards. Runs on the loop goroutine.
func (ls *loopState) completeScope(
	ctx context.Context,
	path scope.DataPath,
	entry *scopeEntry,
) {
	if err := ls.inst.sc.plane.CloseScope(path); err != nil {
		// a child scope still open below — a corrupt tree; fail loudly, the
		// invariant-violation class.
		ls.inst.fail(errs.New(
			errs.M("couldn't close drained scope %q", string(path)),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err)))
		ls.stopAll()

		return
	}

	delete(ls.scopes, path)

	// the scope's window closed — its Event Sub-Process handlers no longer
	// guard anything (SRD-052 FR-5).
	ls.disarmScopeHandlers(path)

	ls.reportScope(observability.PhaseCompleted, entry.node, path)

	ls.resumeScopeHost(ctx, path, entry)
}

// resumeScopeHost resumes a closed scope's parked host with the synthetic
// completion and reopens the scope for a queued re-entry host (§4.4). Runs
// on the loop goroutine.
func (ls *loopState) resumeScopeHost(
	ctx context.Context,
	path scope.DataPath,
	entry *scopeEntry,
) {
	// resume the parked host through the standard parked-dispatch contract.
	ls.dispatchToParked(ctx, trackEvent{
		kind:  evDeliver,
		track: entry.host,
		eDef:  newScopeDone(),
	})

	// a queued sibling host reopens the scope (sequential re-entry, §4.4).
	if len(entry.queue) > 0 {
		next := entry.queue[0]
		entry.queue = entry.queue[1:]

		ls.onScopeOpen(ctx, next, entry.node)

		if len(entry.queue) > 0 {
			// carry the remaining queue into the fresh entry.
			if fresh, ok := ls.scopes[path]; ok {
				fresh.queue = entry.queue
			}
		}
	}
}

// underScope reports whether p is path itself or a descendant of it.
func underScope(p, path scope.DataPath) bool {
	return p == path ||
		len(p) > len(path) && string(p[:len(path)]) == string(path) &&
			p[len(path)] == '/'
}

// cancelScope abandons a scope as a unit (ADR-023 §2.5): every live track
// under path (descendant scopes included) is stopped and its context
// canceled — a parked inner track wakes through ctx.Done, a running one
// hits the discard checkpoint — and its loop-registry state is cleared;
// the subtree's data-plane scopes close deepest-first and their entries
// drop, each reported with phase. The HOST is untouched — callers decide
// its fate (resume for a scoped Terminate, cancellation for an
// interrupting boundary, the exception flow for an error catch). Runs on
// the loop goroutine.
func (ls *loopState) cancelScope(path scope.DataPath, phase observability.Phase) {
	if _, ok := ls.scopes[path]; !ok {
		return // already closed/canceled — a late signal is benign.
	}

	// stop the subtree's tracks and clear their loop state.
	for _, t := range ls.inst.tracks {
		if !underScope(t.scopePath, path) {
			continue
		}

		t.stop()
		t.cancel()
		ls.flipNotParked(t)
		ls.disarmBoundaries(t.ID())
	}

	// close deepest-first: collect the subtree entries, longest path first.
	sub := []scope.DataPath{}

	for p := range ls.scopes {
		if underScope(p, path) {
			sub = append(sub, p)
		}
	}

	slices.SortFunc(sub, func(a, b scope.DataPath) int {
		return len(b) - len(a)
	})

	for _, p := range sub {
		entry := ls.scopes[p]
		delete(ls.scopes, p)

		// the canceled scope's Event Sub-Process handlers no longer guard it
		// (SRD-052 FR-5).
		ls.disarmScopeHandlers(p)

		// best-effort close: the subtree is being abandoned; a close error
		// here cannot be acted on beyond logging (ADR-022 §2.3(2)).
		if err := ls.inst.sc.plane.CloseScope(p); err != nil {
			ls.inst.Logger().Debug("canceled-scope close failed",
				"scope_path", string(p), "error", err.Error())
		}

		ls.reportScope(phase, entry.node, p)
	}
}

// terminateScope realizes the scoped Terminate End Event (§13.5.6, SRD-049
// FR-11): the enclosing scope's tokens are discarded, the scope closes, and
// the parked host resumes — the composite completes abnormally-but-locally
// and the parent continues on its outgoing. A late signal for an
// already-closed scope is a benign no-op. Runs on the loop goroutine.
func (ls *loopState) terminateScope(ctx context.Context, path scope.DataPath) {
	entry, ok := ls.scopes[path]
	if !ok {
		return
	}

	ls.cancelScope(path, observability.PhaseTerminated)
	ls.resumeScopeHost(ctx, path, entry)
}

// reportScope emits one scope-lifecycle fact (SRD-049 FR-13).
func (ls *loopState) reportScope(
	phase observability.Phase,
	node flow.Node,
	path scope.DataPath,
) {
	ls.inst.report(observability.Fact{
		Kind:     observability.KindScope,
		Phase:    phase,
		NodeID:   node.ID(),
		NodeName: node.Name(),
		Details: map[string]string{
			observability.AttrScopePath: string(path),
		},
	})
}
