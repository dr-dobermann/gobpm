package instance

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	dataobjects "github.com/dr-dobermann/gobpm/pkg/model/data_objects"
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

// dataObjectHost is the capability assert for a composite that carries
// SubProcess-level Data Objects (§10.4.1, SRD-063 FR-4): they seed the child
// scope when it opens. Kept a local assert so the runtime stays free of the
// model package (the scopeHost idiom).
type dataObjectHost interface {
	DataObjects() []*dataobjects.DataObject
}

// seedDataObjects commits a composite host's SubProcess-level Data Objects into
// its freshly-opened child scope (SRD-063 FR-4): each becomes a scope-resident
// named container, resolvable by walk-up within the sub-process and disposed
// when the scope closes (completeScope/cancelScope drop the scope's data). A
// birth-init commit — initial state, no DataChange facts (SRD-044 §4.4). A node
// that carries no Data Objects (or is not a dataObjectHost) is a no-op.
func seedDataObjects(
	plane *scope.Scope, node flow.Node, child scope.DataPath,
) error {
	doHost, ok := node.(dataObjectHost)
	if !ok {
		return nil
	}

	dobjs := doHost.DataObjects()
	if len(dobjs) == 0 {
		return nil
	}

	dd := make([]data.Data, 0, len(dobjs))
	for _, do := range dobjs {
		dd = append(dd, do)
	}

	if _, err := plane.Commit(child, dd...); err != nil {
		return errs.New(
			errs.M("couldn't seed sub-process data objects into %q",
				string(child)),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err))
	}

	return nil
}

// scopeEntry is one open nested scope, loop-owned (SRD-049 FR-9): the
// parked host resumes when active drains to zero; a queued second host (a
// parallel sibling arrived while the scope is open, §4.4 of the
// accompanying SRD) reopens the scope after the close.
type scopeEntry struct {
	host *track
	// drain is the activity instance waiting for this scope, signaled when
	// it closes. An entry opened by an executor is signaled DIRECTLY rather
	// than by resuming the host: N instances of one activity share one host
	// track, and a track has one park — which is the entire reason a
	// loop-owned group barrier had to serialize N concurrent drains onto it
	// (SRD-090.A M3b). nil for a scope no executor opened, which still
	// resumes its parked host the original way.
	drain chan struct{}
	// capture is the opening instance's output cell, filled from this scope
	// just before it closes and read by that instance after its drain
	// (SRD-090.A M3b). nil when the activity assembles no output.
	capture *instanceCapture
	// adHoc is the routing state of an Ad-Hoc scope (SRD-074 §3.4), nil for
	// every other scope: the per-activity completed/running counts the Router
	// decides on, and whether routing has already stopped.
	adHoc  *adHocProgress
	node   flow.Node
	parent scope.DataPath
	// queue holds the open requests waiting for this path to free — a
	// second host reached the same composite while this scope was open
	// (§4.4). Each carries its own reply channel, deferred until
	// completeScope serves it; the requester is parked in its roundtrip
	// meanwhile (SRD-090.A M3c, which moved the queue off the retired
	// loop-driven open path).
	queue   []scopeRequest
	active  int
	ordinal int
	// aborting marks a Transaction scope whose Cancel abort is in flight
	// (SRD-061 FR-5): a residual track draining to zero mid-sweep must NOT
	// resume the host normally — finalizeTransaction owns the teardown, driven
	// off the compensation sweep's own completion.
	aborting bool
	// awaitAttach marks a RESTORED own-iteration scope whose decorator
	// runner has not re-attached yet (SRD-082 FR-3): its drain must wait
	// for the re-attach roundtrip — the fence that makes the host's
	// miState readable on the loop. drainPending records a drain that
	// arrived early; the re-attach completes it.
	awaitAttach  bool
	drainPending bool
	// instance marks this scope as ONE of N fanned out from its host's
	// activity rather than the host's own pass (SRD-090.A M3b). It is what
	// ordinal means something on, and it is why the drain must not advance
	// the iteration mirror: a fanned-out position is the decorator's to
	// report (postPosition), never the host's loopCounter, which stands
	// still for the whole fan-out.
	instance bool
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
	return &scopeDone{BaseElement: foundation.EmptyBaseElement()}
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

// scopeSegmentFor picks the child segment a host opens under, in the one
// order every opener agrees on (SRD-090.A M3c): an EXECUTOR-named segment
// wins — it is one of N fanned-out instances, and only the executor knows
// its ordinal; then the HOST's own override, which a non-interrupting Event
// Sub-Process handler carries so concurrent fires of one handler open
// distinct scopes (SRD-053) and a restored track carries from its record;
// then the node's own segment, which is every ordinary composite.
//
// It exists because the two open paths derived this differently — the
// loop-driven one knew about the host override and not the executor's, the
// executor-driven one the reverse — and a merged path cannot have two
// answers.
func scopeSegmentFor(host *track, node flow.Node, instanceSeg string) string {
	if instanceSeg != "" {
		return instanceSeg
	}

	if host.scopeSeg != "" {
		return host.scopeSeg
	}

	return scopeSegment(node)
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
	// An Ad-Hoc scope has no entry shape: what runs first is its Router's first
	// answer, not a rule over flow-less nodes (ADR-035 v.1 §2.2).
	if spec := adHocOf(sh); spec != nil {
		ls.seedAdHoc(ctx, child, spec)

		return
	}

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

// adoptRestoredScopes derives the scope entries a checkpoint deliberately
// does not carry (SRD-082 FR-5, ADR-033 v.4 §2.1 minimality): for every
// open non-root scope path the document restored, the host track, its
// composite node and the parent path all follow from the restored track
// table — only the entry object needs rebuilding, so a drained scope
// resumes its host exactly once instead of the host re-entering the
// composite from the top. active starts at the scope's incident pins
// (an open or dead-lettered incident holds its scope, SRD-079 §3.2);
// the spawn loop's incScope counts the live tracks in. The host is
// marked parked-for-drain up front: its runner re-attaches (the
// re-attach branches in onScopeOpen / handleScopeOpen), and a scope
// that drains before the re-attach must still deliver, not drop.
// Runs on the loop goroutine, BEFORE the initial spawns.
func (ls *loopState) adoptRestoredScopes(initial []*track) error {
	if ls.inst.sc.plane == nil {
		return nil // a bare test instance carries no scope plane
	}

	// the parallel groups first (SRD-082 FR-4): their instance scopes
	// carry per-ordinal segments the generic host derivation below
	// cannot resolve — the group record is what identifies them.
	if err := ls.adoptRestoredGroups(initial); err != nil {
		return err
	}

	for _, path := range ls.inst.sc.plane.OpenPaths() {
		if path == ls.inst.sc.root {
			continue
		}

		if _, ok := ls.scopes[path]; ok {
			continue // a group instance scope, adopted above
		}

		// an open non-root path always has a parent — a DropTail failure
		// is the wired-itself-wrong class, not an input error.
		parent, err := path.DropTail()
		if err != nil {
			return errs.Invariant(
				"restored scope %q has no parent path: %v", string(path), err)
		}

		host, node, ord := ls.recordedScopeHost(initial, path)
		if host == nil {
			host, node, ord = restoredScopeHost(initial, parent, path)
		}

		if host == nil {
			return errs.New(
				errs.M("restored scope %q has no host track — the "+
					"checkpoint's scope and track tables disagree",
					string(path)),
				errs.C(errorClass, errs.InvalidState))
		}

		// an instance whose scope is still open cannot also be recorded
		// finished: the drain closes the scope BEFORE the decorator reports
		// it, so the reverse window does not exist. If a document says both,
		// its scope table and its executor set describe different moments —
		// and nothing would ever re-attach to that scope, leaving it open for
		// the life of the instance.
		if ord >= 0 && instanceRecordedDone(host, ord) {
			return errs.New(
				errs.M("restored instance scope %q is recorded completed — "+
					"the checkpoint's scope table and executor set disagree",
					string(path)),
				errs.C(errorClass, errs.InvalidState))
		}

		pins := 0

		for _, inc := range ls.inst.incidents {
			if inc.scopePath == path && incidentHoldsScope(inc) {
				pins++
			}
		}

		// a FANNED-OUT instance's scope carries its ordinal in its own
		// segment, so the entry is rebuilt from the path alone — no open-set
		// record, and no group to belong to (SRD-090.A M3b). Its drain waits
		// for the re-attach exactly as a serial pass's does: the instance
		// that resumes it is a NEW executor, and until it arrives there is
		// nothing to signal.
		ls.scopes[path] = &scopeEntry{
			host:        host,
			node:        node,
			parent:      parent,
			active:      pins,
			awaitAttach: drivesOwnIteration(node),
			instance:    ord >= 0,
			ordinal:     max(ord, 0),
		}
		ls.waiting[host.ID()] = struct{}{}
	}

	return nil
}

// adoptRestoredGroups translates a SCHEMA-5 parallel Multi-Instance group
// record into the executor-model state its own restore derives from the
// scope table (SRD-082 FR-4 → SRD-090.A FR-7).
//
// It is a migration, not a mechanism. Nothing writes an MIGroupRecord any
// more: a schema-6 capture records the executor set on the host's track and
// the instance ordinals in the scope paths themselves, which is why the
// generic derivation below needs no record at all. A document captured
// before that still has to restore, so its open set is read here and
// re-expressed as what the decorator now expects — one instance entry per
// open scope, and the executor set seeded on the host.
//
// It can go once schema-5 documents are out of support.
func (ls *loopState) adoptRestoredGroups(initial []*track) error {
	for i := range ls.inst.restoredGroups {
		rec := &ls.inst.restoredGroups[i]

		host := trackByID(initial, rec.HostTrack)
		if host == nil {
			return errs.New(
				errs.M("restored MI group names host track %q, which the "+
					"track table does not carry", rec.HostTrack),
				errs.C(errorClass, errs.InvalidState))
		}

		node := host.steps[len(host.steps)-1].node

		if multiInstanceOf(node) == nil {
			return errs.New(
				errs.M("restored MI group host %q is not a Multi-Instance "+
					"node", node.ID()),
				errs.C(errorClass, errs.InvalidState))
		}

		open := ls.inst.sc.plane.OpenPaths()
		live := make([]checkpoint.IterationInstance, 0, len(rec.Open))

		for _, o := range rec.Open {
			path := scope.DataPath(o.Path)

			if !slices.Contains(open, path) {
				return errs.New(
					errs.M("restored MI group open scope %q is not in the "+
						"document's scope table", o.Path),
					errs.C(errorClass, errs.InvalidState))
			}

			ls.scopes[path] = &scopeEntry{
				host:        host,
				node:        node,
				parent:      host.scopePath,
				ordinal:     o.Ordinal,
				instance:    true,
				awaitAttach: true,
			}

			live = append(live, checkpoint.IterationInstance{
				Ordinal: o.Ordinal, State: instanceRunning,
			})
		}

		ls.waiting[host.ID()] = struct{}{}

		// an ordinal the record does not list as open had already drained
		// before the capture, and its output is in the staging — so the
		// set names the open ones running and everything else completed,
		// which is exactly what restoredStates reads.
		host.iterSeed = &checkpoint.IterationRecord{
			Kind:      iterKindMIParallel,
			N:         rec.N,
			Completed: rec.N - len(rec.Open),
			Staging:   rec.Staging,
			Instances: completedOutside(live, rec.N),
		}
		host.miSeed = &checkpoint.MIRecord{
			N: rec.N, Completed: rec.N - len(rec.Open), Staging: rec.Staging,
		}
	}

	return nil
}

// completedOutside fills the ordinals live does not name, up to n, as
// completed instances — the half of a restored fan-out's executor set that
// a group record recorded only by omission.
func completedOutside(
	live []checkpoint.IterationInstance, n int,
) []checkpoint.IterationInstance {
	open := make(map[int]struct{}, len(live))
	for _, inst := range live {
		open[inst.Ordinal] = struct{}{}
	}

	set := make([]checkpoint.IterationInstance, 0, n)

	for ord := range n {
		state := instanceCompleted
		if _, ok := open[ord]; ok {
			state = instanceRunning
		}

		set = append(set, checkpoint.IterationInstance{
			Ordinal: ord, State: state,
		})
	}

	return set
}

// trackByID finds a restored track by its recorded id.
func trackByID(initial []*track, id string) *track {
	for _, t := range initial {
		if t.ID() == id {
			return t
		}
	}

	return nil
}

// recordedScopeHost resolves an open scope's host and ordinal by LOOKUP,
// from what the Schema-7 record says rather than from what the path looks
// like (SRD-090.A M3c). It reports a nil host when the document predates
// Schema 7 or names a track the table does not carry, which sends the
// caller to the derivation below.
//
// A track id survives a restore unchanged (restoredTrack rebuilds the
// recorded identity), so this is exact where the derivation is inferential
// — and it needs no precedence rule, because it never has to decide what a
// path segment MEANT.
//
// A recorded host that is absent from the track table is NOT an error
// here: a Schema-7 document could name a track whose record was pruned,
// and falling through to the derivation is strictly better than refusing —
// the derivation either finds a host or reports the disagreement itself,
// with the message that already exists for it.
func (ls *loopState) recordedScopeHost(
	initial []*track, path scope.DataPath,
) (*track, flow.Node, int) {
	for i := range ls.inst.restoredScopes {
		rec := &ls.inst.restoredScopes[i]
		if rec.Path != string(path) || rec.HostTrack == "" {
			continue
		}

		for _, t := range initial {
			if t.ID() != rec.HostTrack {
				continue
			}

			n := t.steps[len(t.steps)-1].node
			if _, ok := n.(scopeHost); !ok {
				// the recorded host is no longer on a composite — the
				// document's two tables disagree. Let the derivation
				// speak, so one message covers both routes.
				return nil, nil, -1
			}

			return t, n, rec.Ordinal
		}

		return nil, nil, -1
	}

	return nil, nil, -1
}

// restoredScopeHost finds the restored track hosting the open scope at
// path: a track in the parent scope whose current node is a composite
// and whose child segment derives that path.
//
// **The Schema ≤ 6 path** (SRD-090.A M3c). A Schema-7 document answers by
// lookup in recordedScopeHost above; this derivation stays for documents
// written before the scope table named its own hosts, and retires with
// Schema 6.
//
// It reports the instance ordinal the segment carries, or -1 for a host's
// own scope. A FANNED-OUT instance's segment is `sp-<id>-<ord>`, so the
// ordinal — the one thing about an open instance that the track table
// cannot show — is derivable from the path itself, and the open set needs
// no record of its own (SRD-090.A M3b, retiring MIGroupRecord.Open).
// A host's OWN scope is looked for across ALL candidates before any
// instance reading is tried, and only a node that actually fans out can own
// an instance. Both rules exist for the same collision: `sp-a-1` is node
// `a-1`'s own scope AND instance 1 of node `a`, and a single pass would
// answer whichever the track table happened to list first. Only one of the
// two can be open at a time — they are the same DataPath — so the
// precedence decides an interpretation, not a conflict.
func restoredScopeHost(
	initial []*track, parent, path scope.DataPath,
) (*track, flow.Node, int) {
	for _, t := range initial {
		n, seg, ok := restoredHostSegment(t, parent)
		if !ok {
			continue
		}

		if child, err := t.scopePath.Append(seg); err == nil && child == path {
			return t, n, -1
		}
	}

	for _, t := range initial {
		n, seg, ok := restoredHostSegment(t, parent)
		if !ok || !fansOut(n) {
			continue
		}

		if ord, ok := instanceOrdinalOf(t, seg, path); ok {
			return t, n, ord
		}
	}

	return nil, nil, -1
}

// restoredHostSegment reports the composite node a restored track is
// executing and the scope segment that track opens under parent, or false
// when the track hosts no scope there.
func restoredHostSegment(
	t *track, parent scope.DataPath,
) (flow.Node, string, bool) {
	if t.scopePath != parent {
		return nil, "", false
	}

	n := t.steps[len(t.steps)-1].node
	if _, ok := n.(scopeHost); !ok {
		return nil, "", false
	}

	if t.scopeSeg != "" {
		return n, t.scopeSeg, true
	}

	return n, scopeSegment(n), true
}

// instanceRecordedDone reports whether the host's restored executor set
// calls instance ord finished. False when the host carries no set at all —
// a document written before Schema 6, whose fanned-out position rides the
// group record instead (SRD-090.A FR-7).
func instanceRecordedDone(host *track, ord int) bool {
	if host.iterSeed == nil {
		return false
	}

	for _, inst := range host.iterSeed.Instances {
		if inst.Ordinal == ord {
			return inst.State == instanceCompleted
		}
	}

	return false
}

// instanceOrdinalOf reports the ordinal path carries as an instance scope
// of the host track's segment — `<seg>-<ord>` under the host's own scope.
//
// The prefix is built with Append rather than compared as a string, so the
// path grammar stays in one place; and what follows it must read back as
// exactly the number it names — which is also what keeps a DESCENDANT of an
// instance scope out, since any deeper path still carries a separator.
func instanceOrdinalOf(
	t *track, seg string, path scope.DataPath,
) (int, bool) {
	prefix, err := t.scopePath.Append(seg + "-")
	if err != nil {
		return 0, false
	}

	rest, found := strings.CutPrefix(string(path), string(prefix))
	if !found {
		return 0, false
	}

	ord, err := strconv.Atoi(rest)
	if err != nil || ord < 0 || strconv.Itoa(ord) != rest {
		return 0, false
	}

	return ord, true
}

// incidentHoldsScope reports whether the incident still holds its
// scope's pin: open states do, and so does a dead letter — the token
// died unconsumed, the scope must never settle past it (SRD-079 §3.2).
func incidentHoldsScope(inc *incident) bool {
	return inc.state.open() || inc.state == incidentDeadLettered
}

// incScope counts a spawned track into its scope's drain accounting; root
// tracks have no entry and cost nothing (NFR-1). Runs on the loop
// goroutine.
func (ls *loopState) incScope(t *track) {
	if entry, ok := ls.scopes[t.scopePath]; ok {
		entry.active++
	}
}

// decScopePinned releases one incident scope pin (SRD-079 §3.2): the count the
// failing track's exit deliberately skipped, released when the incident closes
// or its next attempt takes over the pin. The track object may be gone (a
// post-restore incident), so the ad-hoc settle — which needs the live track —
// is skipped; an ad-hoc scope re-routes on its next natural settle. Runs on
// the loop goroutine.
func (ls *loopState) decScopePinned(ctx context.Context, path scope.DataPath) {
	entry, ok := ls.scopes[path]
	if !ok {
		return
	}

	entry.active--

	if entry.active > 0 || entry.aborting {
		return
	}

	if entry.awaitAttach {
		entry.drainPending = true

		return
	}

	ls.completeScope(ctx, path, entry)
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

	// An Ad-Hoc scope routes on every settle (§13.3.5 "the enabled set is
	// updated"), which may start further activities — so the drain check comes
	// after routing, not before it.
	if entry.adHoc != nil && ls.settleAdHoc(ctx, t.scopePath, entry, t) {
		return
	}

	if entry.active > 0 {
		return
	}

	// a Transaction abort in flight drives its own teardown (finalizeTransaction
	// off the compensation sweep); a residual draining to zero here must not
	// resume the host normally (SRD-061 FR-5).
	if entry.aborting {
		return
	}

	// a restored own-iteration scope holds its drain for the runner's
	// re-attach — the fence that makes the host state loop-readable
	// (SRD-082 FR-3).
	if entry.awaitAttach {
		entry.drainPending = true

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
	// SRD-055: a sequential Multi-Instance captures the draining instance's
	// output item before the child scope closes — the last point its data is
	// readable, and the one loop-side step the off-loop decorator cannot do
	// (§4.2). A no-op for a non-MI scope (host.miState nil).
	if err := ls.captureSequentialOutput(ctx, entry, path); err != nil {
		ls.inst.fail(err)
		ls.stopAll()

		return
	}

	// SRD-090.A M3b: the OPENING INSTANCE's own capture, the executor-driven
	// successor to both of the above — read here for the same reason, and
	// handed over by the drain close below rather than by a lock.
	if err := ls.captureInstanceOutput(ctx, entry, path); err != nil {
		ls.inst.fail(err)
		ls.stopAll()

		return
	}

	// SRD-082 FR-2: one serial pass completed — advance the loop-owned
	// iteration mirror before the runner resumes.
	ls.markIterDrain(entry)

	// SRD-059 FR-3/FR-7: a completing composite enters the parent's completion
	// ledger (its own handler and/or its folded child ledger) — recorded here,
	// while the scope's data is still readable and its handlers still armed.
	ls.recordScopeCompletion(path, entry)

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

	ls.reportScope(
		observability.PhaseCompleted, entry.node, path, scopeFactOrdinal(entry))
	ls.reportAdHocSettled(entry, observability.PhaseCompleted)

	// an executor's own scope drains to the executor (SRD-090.A M3b). The
	// close is the signal — one scope drains exactly once, and a closed
	// channel needs no reader to be present yet, so an instance still
	// between its open and its wait cannot miss it.
	ls.releaseScopeHost(ctx, path, entry)
}

// releaseScopeHost wakes whatever was waiting on a scope that has just left
// the table — drained, canceled or terminated — and hands the path to the
// next host queued for it (§4.4).
//
// Every scope is opened by an activity instance's executor now (SRD-090.A
// M3c), so the wake is its drain channel: closing it needs no reader to be
// present yet, which is what lets an instance still between its open and
// its wait not miss the signal. A restored entry carries no channel until
// its runner re-attaches, and closing nothing is correct there — the
// re-attach either finds the entry and adopts its drain, or finds the path
// free and opens it afresh.
func (ls *loopState) releaseScopeHost(
	ctx context.Context, path scope.DataPath, entry *scopeEntry,
) {
	if entry.drain != nil {
		close(entry.drain)
	}

	ls.serveScopeQueue(ctx, path, entry)
}

// serveScopeQueue re-opens a just-closed path for the host that has been
// waiting on it (§4.4). Runs on the loop goroutine, after the scope is
// closed and its entry removed, so the open sees a free path.
//
// The remaining queue is carried onto the fresh entry rather than served
// in a loop: only one of them can hold the path, and the next one waits
// exactly as this one did.
func (ls *loopState) serveScopeQueue(
	ctx context.Context, path scope.DataPath, entry *scopeEntry,
) {
	if len(entry.queue) == 0 {
		return
	}

	next, rest := entry.queue[0], entry.queue[1:]

	ls.handleScopeOpen(ctx, next)

	if len(rest) == 0 {
		return
	}

	if fresh, ok := ls.scopes[path]; ok {
		fresh.queue = append(fresh.queue, rest...)

		return
	}

	// the re-open failed and replied with the error, so nothing holds the
	// path now. Serving the next one keeps the queue draining instead of
	// stranding every host behind a single failure.
	ls.serveScopeQueue(ctx, path, &scopeEntry{queue: rest})
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

	// a canceled scope's eligibility window closes with it: its completion
	// ledger (and every ledger under it) discards — the canceled work's
	// completed activities are no longer compensable (SRD-059 FR-3).
	ls.discardLedgers(path)

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
				observability.AttrScopePath, string(p), observability.AttrError, err.Error())
		}

		ls.reportScope(phase, entry.node, p, scopeFactOrdinal(entry))

		// An ad-hoc container's routing ended here however the scope was cut
		// short — a Terminate still leaves the routing canceled, so the terminal
		// fact reads Canceled while the scope's own fact carries the phase
		// (SRD-074 §3.6).
		ls.reportAdHocSettled(entry, observability.PhaseCanceled)
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
	ls.releaseScopeHost(ctx, path, entry)
}

// reportScope emits one scope-lifecycle fact (SRD-049 FR-13).
func (ls *loopState) reportScope(
	phase observability.Phase,
	node flow.Node,
	path scope.DataPath,
	loopCounter int,
) {
	details := map[string]string{
		observability.AttrScopePath: string(path),
	}

	// a looped composite carries its iteration ordinal so each Standard-Loop
	// pass is individually observable (SRD-054 FR-11); a non-looped scope passes
	// -1 to omit it.
	if loopCounter >= 0 {
		details[observability.AttrLoopCounter] = strconv.Itoa(loopCounter)
	}

	ls.inst.report(observability.Fact{
		Kind:     observability.KindScope,
		Phase:    phase,
		NodeID:   node.ID(),
		NodeName: node.Name(),
		Details:  details,
	})
}

// scopeLoopCounter returns the host's Standard-Loop iteration ordinal for a
// looped composite, or -1 when the node is not looped (so its scope facts omit
// the attribute).
func scopeLoopCounter(node flow.Node, host *track) int {
	// a looped composite that drives its own iteration publishes its pass ordinal
	// to the iteration scope facts (SRD-054 FR-11, SRD-055 FR-13): a Standard Loop
	// or a sequential Multi-Instance (both decorator-driven, §2.12).
	if drivesOwnIteration(node) {
		return host.loopCounterSnap()
	}

	return -1
}

// scopeFactOrdinal is the ordinal a scope's lifecycle fact carries.
//
// A FANNED-OUT instance reports its OWN (SRD-056.A FR-14), which is the one
// on its entry: the host's loopCounter is shared by all N and stands still
// for the whole fan-out, so it cannot name any of them. Every serial scope
// reads the host's pass counter as before.
//
// The two facts a scope emits — Completed and Canceled — ask this one
// question, and asked separately they drifted once already: the open side
// learned the instance ordinal in this milestone while both close sides
// still reported the host's.
func scopeFactOrdinal(entry *scopeEntry) int {
	if entry.instance {
		return entry.ordinal
	}

	return scopeLoopCounter(entry.node, entry.host)
}
