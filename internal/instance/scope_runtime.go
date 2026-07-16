package instance

import (
	"context"

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

	child, err := host.scopePath.Append(scopeSegment(node))
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

	ls.seedScope(ctx, sh, child)
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
	seeds := scopeSeeds(sh)

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

	ls.reportScope(observability.PhaseCompleted, entry.node, path)

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
