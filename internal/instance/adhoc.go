package instance

import (
	"context"

	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/adhoc"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// adHocOf reports the node's Ad-Hoc routing configuration, or nil when the node
// is not an Ad-Hoc Sub-Process — the sibling of standardLoopOf and
// multiInstanceOf, resolving a capability rather than a concrete type so the
// runtime never depends on a model variant (SRD-074 §3.4).
func adHocOf(node flow.Node) activities.AdHocSpec {
	h, ok := node.(interface {
		AdHoc() activities.AdHocSpec
	})
	if !ok {
		return nil
	}

	return h.AdHoc()
}

// adHocProgress is an Ad-Hoc scope's routing state, owned by the loop goroutine
// (the only writer) and handed to the Router as the progress half of its
// decision: how many times each inner activity has settled, how many instances
// of each are live, and whether routing has already stopped.
type adHocProgress struct {
	completed map[string]int
	running   map[string]int
	// stopped marks a scope whose Router has answered empty: no further routing
	// happens, and the scope completes once its live activities drain.
	stopped bool
}

func newAdHocProgress() *adHocProgress {
	return &adHocProgress{
		completed: map[string]int{},
		running:   map[string]int{},
	}
}

// state projects the progress into the Router's view, copying the counters so a
// Router cannot mutate engine state through the maps it is handed.
func (p *adHocProgress) state(last string, r service.DataReader) adhoc.State {
	return adhoc.State{
		Completed: copyCounts(p.completed),
		Running:   copyCounts(p.running),
		Last:      last,
		Data:      r,
	}
}

func copyCounts(src map[string]int) map[string]int {
	out := make(map[string]int, len(src))
	for k, v := range src {
		out[k] = v
	}

	return out
}

// seedAdHoc opens an Ad-Hoc scope by asking its Router what may run first: the
// standard's "initially enabled" set is the Router's first answer, not a
// hardcoded rule (ADR-035 v.1 §2.2). An empty first answer completes the
// container without running anything, which is a legitimate decision.
func (ls *loopState) seedAdHoc(
	ctx context.Context,
	child scope.DataPath,
	spec activities.AdHocSpec,
) {
	entry, ok := ls.scopes[child]
	if !ok {
		return
	}

	entry.adHoc = newAdHocProgress()

	ls.routeAdHoc(ctx, child, entry, spec, "")

	// An empty first answer leaves no track behind, so no settle will ever
	// drive the drain — the scope's completion has to be driven here. The
	// drain condition is already met: nothing is live.
	if entry.active == 0 && !ls.stopping {
		ls.completeScope(ctx, child, entry)
	}
}

// routeAdHoc asks the container's Router for the activities to run next and
// spawns a track for each. An empty answer stops routing: the scope then
// completes as soon as its live activities drain — completion is the drain, not
// a separate mechanism (ADR-035 v.1 §2.3). Runs on the loop goroutine.
func (ls *loopState) routeAdHoc(
	ctx context.Context,
	path scope.DataPath,
	entry *scopeEntry,
	spec activities.AdHocSpec,
	last string,
) {
	nodes, err := ls.askAdHocRouter(ctx, path, entry, spec, last)
	if err != nil {
		ls.inst.fail(err)
		ls.stopAll()

		return
	}

	if len(nodes) == 0 {
		entry.adHoc.stopped = true

		// Routing stopped while activities are still live: BPMN's
		// cancelRemainingInstances decides whether they are cut short or
		// awaited. Waiting needs no action — the scope completes when they
		// drain through decScope.
		if entry.active > 0 && spec.CancelsRemaining() {
			// cancelScope leaves the host's fate to its caller (a boundary
			// resumes through the exception flow, a scoped Terminate resumes
			// directly). An ad-hoc container COMPLETES when routing stops, so
			// the host is resumed here — otherwise it would stay parked with
			// nothing left to wake it.
			ls.cancelScope(path, observability.PhaseCanceled)
			ls.resumeScopeHost(ctx, path, entry)
		}

		return
	}

	for _, n := range nodes {
		ls.spawnAdHoc(ctx, path, entry, n)
	}
}

// askAdHocRouter evaluates the Router against a transient read frame opened at
// the Ad-Hoc scope — a consistent snapshot for the whole decision, the same
// mechanism loop conditions and conditional events read through — and resolves
// its answer to inner nodes.
func (ls *loopState) askAdHocRouter(
	ctx context.Context,
	path scope.DataPath,
	entry *scopeEntry,
	spec activities.AdHocSpec,
	last string,
) ([]flow.Node, error) {
	frame, err := ls.inst.sc.openFrameAt("adhoc", entry.node.ID(), path)
	if err != nil {
		return nil, err
	}
	defer frame.Discard()

	ids, err := spec.Router().Next(ctx, entry.adHoc.state(last, frame))
	if err != nil {
		return nil, errs.New(
			errs.M("ad-hoc router of %q failed", entry.node.Name()),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err))
	}

	if len(ids) == 0 {
		return nil, nil
	}

	// Sequential ordering permits one live activity, so a multi-successor
	// answer is a modeling error — reported, never truncated to the first
	// (ADR-035 v.1 §2.5).
	if spec.Ordering() == activities.AdHocSequential &&
		len(ids)+entry.active > 1 {
		return nil, errs.New(
			errs.M("sequential ad-hoc %q was routed to %d activities while %d "+
				"were live: only one may run at a time",
				entry.node.Name(), len(ids), entry.active),
			errs.C(errorClass, errs.InvalidState))
	}

	return resolveAdHocNodes(entry.node, ids)
}

// resolveAdHocNodes maps the Router's answer to this container's inner nodes,
// by id and then by name — an unresolvable id is a loud failure, never a
// silently skipped activity.
func resolveAdHocNodes(host flow.Node, ids []string) ([]flow.Node, error) {
	c, ok := host.(interface{ Nodes() []flow.Node })
	if !ok {
		return nil, errs.New(
			errs.M("ad-hoc host %q holds no inner nodes", host.Name()),
			errs.C(errorClass, errs.InvalidState))
	}

	inner := c.Nodes()
	out := make([]flow.Node, 0, len(ids))

	for _, id := range ids {
		found := findAdHocNode(inner, id)
		if found == nil {
			return nil, errs.New(
				errs.M("ad-hoc router of %q chose %q, which is not one of its "+
					"activities", host.Name(), id),
				errs.C(errorClass, errs.ObjectNotFound))
		}

		out = append(out, found)
	}

	return out, nil
}

func findAdHocNode(inner []flow.Node, id string) flow.Node {
	for _, n := range inner {
		if n.ID() == id {
			return n
		}
	}

	for _, n := range inner {
		if n.Name() == id {
			return n
		}
	}

	return nil
}

// spawnAdHoc starts one routed activity as its own track in the Ad-Hoc scope,
// counting it live. Mirrors seedScope's pre-spawn discipline: the scope path
// and the routed activity are set on the loop goroutine, before the track's own
// goroutine exists.
func (ls *loopState) spawnAdHoc(
	ctx context.Context,
	path scope.DataPath,
	entry *scopeEntry,
	node flow.Node,
) {
	nt, err := newTrack(node, ls.inst, entry.host)
	if err != nil {
		ls.inst.fail(errs.New(
			errs.M("couldn't start ad-hoc activity %q", node.Name()),
			errs.C(errorClass, errs.BulidingFailed),
			errs.E(err)))
		ls.stopAll()

		return
	}

	nt.scopePath = path
	nt.adHocActivity = node.ID()

	entry.adHoc.running[node.ID()]++

	ls.inst.trackCount.Add(1)
	ls.inst.tracks[nt.ID()] = nt
	// spawn counts the track into its scope (incScope) — the ad-hoc path must
	// not count it a second time, or the scope never drains.
	ls.spawn(ctx, nt)

	if ls.stopping {
		nt.stop()
	}
}

// settleAdHoc records a routed activity's completion and asks the Router what
// follows — the standard's "after each completion the enabled set is updated"
// (§13.3.5). Returns true when the scope must NOT complete yet: either routing
// produced new work or live activities remain. Runs on the loop goroutine.
func (ls *loopState) settleAdHoc(
	ctx context.Context, path scope.DataPath, entry *scopeEntry, t *track,
) bool {
	id := t.adHocActivity
	if id == "" {
		return false
	}

	entry.adHoc.running[id]--
	if entry.adHoc.running[id] <= 0 {
		delete(entry.adHoc.running, id)
	}

	entry.adHoc.completed[id]++

	// A stopped container runs no further routing; it is only waiting for its
	// live activities to drain.
	if entry.adHoc.stopped {
		return entry.active > 0
	}

	spec := adHocOf(entry.node)
	if spec == nil {
		return false
	}

	ls.routeAdHoc(ctx, path, entry, spec, id)

	return entry.active > 0
}
