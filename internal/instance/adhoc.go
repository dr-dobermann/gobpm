package instance

import (
	"cmp"
	"context"
	"strings"

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

// The values the Ad-Hoc fact vocabulary carries (SRD-074 §3.6): who selected an
// activated activity, and why routing ended. Named constants rather than
// literals — the audit-trail tests assert them, and a stream consumer switches
// on them.
const (
	adHocByRouter = "router"
	adHocByHost   = "host"

	adHocStopRouterEmpty = "router-empty"
	adHocStopCanceled    = "canceled"
)

// adHocProgress is an Ad-Hoc scope's routing state, owned by the loop goroutine
// (the only writer) and handed to the Router as the progress half of its
// decision: how many times each inner activity has settled, how many instances
// of each are live, and whether routing has already stopped.
type adHocProgress struct {
	completed map[string]int
	running   map[string]int
	// stopReason records WHY routing ended, for the container's terminal fact —
	// the audit half of the decision trail (SRD-074 FR-13). Empty while routing
	// continues, and for a container cut short from outside, which never stopped
	// its own routing. Declared before offered to keep the pointer-scannable
	// prefix minimal (fieldalignment).
	stopReason string
	// offered are the candidates a manual container is waiting on: the Router
	// answered, and the container now holds them until a host activates one
	// (ADR-035 v.1 §2.6). Empty in automatic mode, where an answer runs at once.
	offered []flow.Node
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
	// drain condition is already met: nothing is live. A container holding
	// offers is NOT done: it is waiting for a selection.
	if entry.active == 0 && len(entry.adHoc.offered) == 0 && !ls.stopping {
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

	// Every answer is reported, the empty one included: "the Router considered
	// the state and chose to stop" is exactly the fact an auditor needs, and
	// suppressing it would make a terminal indistinguishable from a Router that
	// was never asked (SRD-074 §3.6).
	ls.reportAdHoc(observability.PhaseOffered, entry.node,
		observability.AttrCandidates, adHocIDs(nodes))

	if len(nodes) == 0 {
		entry.adHoc.stopped = true
		entry.adHoc.stopReason = adHocStopRouterEmpty

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

	// Manual selection offers the answer instead of running it: the container
	// waits for a host to choose, and the wait costs no goroutine — the scope's
	// host track is already parked (ADR-035 v.1 §2.6).
	if spec.IsManual() {
		entry.adHoc.offered = nodes

		return
	}

	for _, n := range nodes {
		ls.spawnAdHoc(ctx, path, entry, n, adHocByRouter)
	}
}

// adHocIDs joins the routed activities' ids for the candidates attribute — ids
// only, never the data the Router read (the masking rule).
func adHocIDs(nodes []flow.Node) string {
	ids := make([]string, 0, len(nodes))
	for _, n := range nodes {
		ids = append(ids, n.ID())
	}

	return strings.Join(ids, ",")
}

// reportAdHoc emits one Ad-Hoc routing fact (SRD-074 FR-12). Each moment
// carries exactly one attribute — the candidate set, the selector, or the stop
// reason — while the container's scope open/close keeps riding KindScope, so
// nothing is reported twice.
func (ls *loopState) reportAdHoc(
	phase observability.Phase, node flow.Node, key, value string,
) {
	ls.inst.report(observability.Fact{
		Kind:     observability.KindAdHoc,
		Phase:    phase,
		NodeID:   node.ID(),
		NodeName: node.Name(),
		Details:  map[string]string{key: value},
	})
}

// reportAdHocSettled emits an Ad-Hoc container's terminal fact — the audit pair
// to its Offered/Activated stream (SRD-074 FR-13) — naming why routing ended. A
// no-op for a scope that is not ad-hoc, so the scope machinery calls it
// unconditionally.
func (ls *loopState) reportAdHocSettled(
	entry *scopeEntry, phase observability.Phase,
) {
	if entry.adHoc == nil {
		return
	}

	// A container cut short from outside — an interrupting boundary, a scoped
	// Terminate — never stopped its own routing, so the cancellation itself is
	// the reason.
	ls.reportAdHoc(phase, entry.node, observability.AttrStopReason,
		cmp.Or(entry.adHoc.stopReason, adHocStopCanceled))
}

// activateAdHoc starts one offered activity, chosen by the host. Activating
// something that is not currently offered is a classified error, never a silent
// no-op — a stale or mistaken choice must be visible to whoever made it. Runs on the
// loop goroutine.
func (ls *loopState) activateAdHoc(
	ctx context.Context, hostNodeID, activityID string,
) error {
	path, entry := ls.findAdHocScope(hostNodeID)
	if entry == nil {
		return errs.New(
			errs.M("no open ad-hoc scope for node %q", hostNodeID),
			errs.C(errorClass, errs.ObjectNotFound))
	}

	idx := -1

	for i, n := range entry.adHoc.offered {
		if n.ID() == activityID || n.Name() == activityID {
			idx = i

			break
		}
	}

	if idx < 0 {
		return errs.New(
			errs.M("%q is not among the activities offered by ad-hoc %q",
				activityID, entry.node.Name()),
			errs.C(errorClass, errs.InvalidState))
	}

	node := entry.adHoc.offered[idx]

	// The whole offer is consumed: the enabled set is re-derived from the
	// Router after this activity settles, so keeping the unchosen candidates
	// would let a host activate against a stale view.
	entry.adHoc.offered = nil

	ls.spawnAdHoc(ctx, path, entry, node, adHocByHost)

	return nil
}

// findAdHocScope resolves the open ad-hoc scope hosted by nodeID.
func (ls *loopState) findAdHocScope(
	nodeID string,
) (scope.DataPath, *scopeEntry) {
	for p, e := range ls.scopes {
		if e.adHoc != nil && e.node.ID() == nodeID {
			return p, e
		}
	}

	return "", nil
}

// adHocView reports an open ad-hoc scope's offered and running activities for
// host inspection. Runs on the loop goroutine.
func (ls *loopState) adHocView(nodeID string) ([]string, []string, error) {
	_, entry := ls.findAdHocScope(nodeID)
	if entry == nil {
		return nil, nil, errs.New(
			errs.M("no open ad-hoc scope for node %q", nodeID),
			errs.C(errorClass, errs.ObjectNotFound))
	}

	offered := make([]string, 0, len(entry.adHoc.offered))
	for _, n := range entry.adHoc.offered {
		offered = append(offered, n.ID())
	}

	running := make([]string, 0, len(entry.adHoc.running))
	for id := range entry.adHoc.running {
		running = append(running, id)
	}

	return offered, running, nil
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
// counting it live, and records who selected it. Mirrors seedScope's pre-spawn
// discipline: the scope path and the routed activity are set on the loop
// goroutine, before the track's own goroutine exists.
func (ls *loopState) spawnAdHoc(
	ctx context.Context,
	path scope.DataPath,
	entry *scopeEntry,
	node flow.Node,
	by string,
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

	// Reported before the track runs, so the selection precedes the activity's
	// own node facts in the stream and the pairing with its Offered reads in
	// order (SRD-074 FR-13).
	ls.reportAdHoc(observability.PhaseActivated, node,
		observability.AttrSelectedBy, by)

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

	// Offers keep the container open exactly as live activities do.
	return entry.active > 0 || len(entry.adHoc.offered) > 0
}

// ActivateAdHoc starts one activity that the ad-hoc container hosted by
// nodeID has offered — BPMN §13.3.5's "one enabled Activity is selected for
// execution", performed by the host rather than the engine. Activating
// something not currently offered is a classified error: a host acting on a
// stale view learns so instead of silently doing nothing. Serviced by the
// instance loop.
func (inst *Instance) ActivateAdHoc(
	ctx context.Context, nodeID, activityID string,
) error {
	if nodeID == "" || activityID == "" {
		return errs.New(
			errs.M("ActivateAdHoc: both the container and the activity must "+
				"be named"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	_, err := inst.taskRoundtrip(ctx, taskRequest{
		kind:   reqActivateAdHoc,
		nodeID: nodeID,
		taskID: activityID,
	})

	return err
}

// AdHocView reports what the ad-hoc container hosted by nodeID currently
// offers for selection and what it is already running — the enabled set a host
// chooses from. Serviced by the instance loop.
func (inst *Instance) AdHocView(
	ctx context.Context, nodeID string,
) (offered, running []string, err error) {
	if nodeID == "" {
		return nil, nil, errs.New(
			errs.M("AdHocView: the container must be named"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	req := taskRequest{kind: reqAdHocView, nodeID: nodeID}
	req.reply = make(chan taskReply, 1)

	select {
	case inst.taskReq <- req:
	case <-inst.loopDone:
		return nil, nil, errs.New(
			errs.M("instance %q is not running", inst.ID()),
			errs.C(errorClass, errs.InvalidState))
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}

	select {
	case r := <-req.reply:
		return r.offered, r.running, r.err
	case <-inst.loopDone:
		return nil, nil, errs.New(
			errs.M("instance %q stopped before the ad-hoc view"),
			errs.C(errorClass, errs.InvalidState))
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
}
