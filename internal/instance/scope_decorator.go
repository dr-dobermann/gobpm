package instance

import (
	"context"

	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// scopeOp is the operation a scopeRequest asks the single-writer loop to
// perform for an off-loop iteration decorator (ADR-025 §2.13). scopeOpen
// serves EVERY instance that is a child scope — a serial pass and one of N
// fanned out alike, which is what let the group barrier's four ops (fan out,
// re-arm, complete, re-attach) retire: a decorator that holds its instances
// as executors counts its own completions, and each instance waits on its own
// drain instead of queueing onto the host's single park (SRD-090.A M3b).
type scopeOp int

const (
	// scopeOpen opens one child scope for a serial pass and parks the host for
	// its drain (Standard Loop, sequential MI).
	scopeOpen scopeOp = iota
	// scopeNote informs the loop's iteration mirror that the runner's
	// completionCondition fired (SRD-082 FR-2) — the one decorator
	// decision the loop cannot observe from the open/drain protocol.
	scopeNote
	// scopeIterPost advances the loop's iteration mirror with the
	// decorator's own executor set (SRD-086 FR-5, SRD-090.A FR-6): n
	// carries the completed count, insts the per-ordinal states. The
	// roundtrip is the fence that makes the miState reads safe.
	//
	// It serves every shape the decorator drives, not just the leaf it
	// was introduced for. A leaf has no choice — it opens no scope, so
	// the mirror cannot ride the open/drain protocol at all. A parallel
	// COMPOSITE could in principle be derived from its open scopes, but
	// only between its first open and its last drain: the window before
	// the fan-out has opened anything is indistinguishable from a
	// finished activity, and it is exactly where a restore would lose
	// the set. One authority for both is the simpler contract.
	scopeIterPost
	// scopeCancelInstances tears down the still-open instance scopes of one
	// host and reports how many (SRD-090.A M3b). It replaces the group-wide
	// teardown for executor-driven instances, and it is asked by whoever
	// CANCELED rather than by the instances themselves: an instance wakes
	// from awaitDrain on a canceled context, and scopeRoundtrip honors ctx,
	// so a request sent on the way out would fail and leak the scope it
	// meant to close.
	scopeCancelInstances
)

// scopeRequest is a looped composite's off-loop iteration decorator asking the
// single-writer loop to perform a scope operation (op) for one iteration step
// (SRD-054 §2.12 / FR-8a): host is the composite host track, node is the
// composite node, and reply carries the loop's verdict back to the
// decorator's runner goroutine. For a serial open the pass ordinal is bound
// as loopCounter by the decorator itself, off the loop (§4.6) — a plane
// write, mutex-safe like the leaf loop's bind — so it is set before the
// continuation test reads it and before the scope opens.
type scopeRequest struct {
	host  *track
	node  flow.Node
	reply chan scopeReply
	// drain is the channel the requesting INSTANCE waits on for its scope's
	// completion (SRD-090.A M3b). Recorded on the entry, closed when the
	// scope drains.
	drain chan struct{}
	// capture is the cell this instance's output is read into, before its
	// scope closes. nil when the activity assembles no output.
	capture *instanceCapture
	// segment overrides the child scope's path segment. Empty means the
	// node's own `sp-<id>`, which is what a sequential pass and a plain
	// composite use; a FANNED-OUT instance passes `sp-<id>-<ordinal>` so
	// its N siblings get distinct scopes (SRD-090.A M3b). The executor
	// decides, because deriving it here would move the sequential path's
	// data paths and observability facts.
	segment string
	// insts carries the decorator's executor set to the loop's iteration
	// mirror on a scopeIterPost (SRD-090.A FR-6) — the per-ordinal states
	// the record persists, from the one component that knows them all.
	insts []checkpoint.IterationInstance
	// binds are the per-instance data items published at the CHILD scope
	// before its body is seeded — the 0-based loopCounter, and the split
	// input item when the iteration is collection-driven. Concurrency-safe
	// where the sequential slice's host-scope bind is not, because each
	// instance writes only its own scope.
	binds []miBinding
	op    scopeOp
	// n is the completed-instance count a scopeIterPost carries.
	n int
	// factOrdinal is the ordinal this scope's lifecycle facts carry, or -1
	// to omit the attribute. A fanned-out instance reports its own; a
	// serial pass reports the host's pass counter; a plain composite has
	// none. Supplied rather than derived, for the same reason as iterating.
	factOrdinal int
	// ordinal is the instance's own 0-based index, reported as its Opened
	// fact (FR-14) rather than the host's shared loopCounter. Read only
	// when segment is set, since that is what marks a per-instance open.
	ordinal int
	// iterating says this open belongs to an activity that runs its node
	// more than once, so the loop keeps a position mirror for the capture
	// (SRD-082 FR-2) and reports the pass ordinal on the scope's facts.
	//
	// The REQUESTER declares it. The loop used to ask the node — a
	// drivesOwnIteration probe on the way in — which is the shape FR-11
	// removes: a driver that tests whether a node iterates knows something
	// the decorator was supposed to own. Answering from the executor costs
	// one bool and puts the knowledge where it already lives.
	iterating bool
	// persist marks this open as an observable lifecycle transition, so the
	// loop takes a checkpoint once the scope is up (ADR-033 §2.2).
	//
	// Only the WHOLE activity's open is one: a plain composite entering its
	// body is a token's step, while a decorator's per-pass open is interior
	// to one step and persists through the position post instead. The
	// requester decides, because the loop must not ask whether the node
	// iterates (SRD-090.A FR-11).
	//
	// It replaces a persist point the mechanism used to supply by accident:
	// the loop-driven open was requested by a track EVENT, and that event
	// was on the checkpoint list — which silently gave every plain-composite
	// open a checkpoint and every decorator pass none. Checkpointing all of
	// them instead re-ran a restored sequential pass, because a document
	// captured mid-pass records the position that pass has not reached yet.
	persist bool
}

// instanceCapture is one fanned-out composite instance's output slot.
//
// A composite's output lives in a child scope that is about to close, so
// unlike a leaf's frame capture it cannot be read off the loop. It does not
// need a lock either: the loop fills this cell before closing the entry's
// drain, and the instance reads it only after that drain returns, so the
// close is the happens-before edge (SRD-090.A M3b).
type instanceCapture struct {
	value any
	// item is the output data item's name in the child scope.
	item string
	// filled stays false when the instance produced no output — that slot
	// keeps its nil, exactly as a canceled one does (SRD-056.A §2.7).
	filled bool
}

// scopeReply is the loop's answer to a scopeRequest: the opened child path,
// the count of instances torn down (scopeCancelInstances), or an error the
// decorator faults on.
type scopeReply struct {
	err        error
	scopePath  scope.DataPath
	terminated int
}

// scopeRoundtrip hands req to the loop and blocks for the reply, honoring ctx and
// instance shutdown — the runner-goroutine side of the scope protocol, cloned from
// taskRoundtrip. The decorator never touches loop-owned state directly; it waits
// only on channels the loop writes (scopeReq accept, then reply), so the wait graph
// is a DAG (decorator → loop), never a cycle (SRD-054 NFR-3/NFR-4).
func (inst *Instance) scopeRoundtrip(
	ctx context.Context,
	req scopeRequest,
) (scope.DataPath, error) {
	r, err := inst.scopeExchange(ctx, req)

	return r.scopePath, err
}

// scopeExchange is scopeRoundtrip's full-reply form: it hands req to the loop and
// returns the whole scopeReply, so a caller that needs a field other than the
// opened path (a teardown reads the terminated count) can read it.
// scopeRoundtrip is the thin path-only wrapper.
func (inst *Instance) scopeExchange(
	ctx context.Context,
	req scopeRequest,
) (scopeReply, error) {
	req.reply = make(chan scopeReply, 1)

	select {
	case inst.scopeReq <- req:
	case <-inst.loopDone:
		return scopeReply{}, errs.New(
			errs.M("instance %q is not running", inst.ID()),
			errs.C(errorClass, errs.InvalidState))
	case <-ctx.Done():
		return scopeReply{}, ctx.Err()
	}

	select {
	case r := <-req.reply:
		return r, r.err
	case <-inst.loopDone:
		return scopeReply{}, errs.New(
			errs.M("instance %q stopped before scope reply", inst.ID()),
			errs.C(errorClass, errs.InvalidState))
	case <-ctx.Done():
		return scopeReply{}, ctx.Err()
	}
}

// handleScopeRequest is the loop-side half of the scope protocol (mirroring
// handleTaskRequest): it runs on the loop goroutine and dispatches the decorator's
// request to the single-writer mutation it names. scopeOpen serves the serial
// drivers (Standard Loop, sequential MI); scopeFanOut / scopeReArm / scopeComplete
// serve a parallel Multi-Instance's off-loop barrier (SRD-056.A).
func (ls *loopState) handleScopeRequest(ctx context.Context, req scopeRequest) {
	switch req.op {
	case scopeNote:
		if m, ok := ls.iter[req.host.ID()]; ok {
			m.conditionMet = true
		}

		req.reply <- scopeReply{}
	case scopeIterPost:
		m := ls.ensureIterMirror(req.host, req.node)
		m.completed = req.n
		m.instances = req.insts

		req.reply <- scopeReply{}

		// the post IS the observable transition (ADR-033 §2.2): a whole
		// iteration is ONE step execution emitting no track events, so
		// without this the position never persists (SRD-086 FR-5). That
		// covers the activation post too — a fan-out that has decided
		// which instances to run has changed the instance's durable
		// position, whether or not any of them has finished.
		ls.checkpointNow(ctx)
	case scopeCancelInstances:
		ls.handleCancelInstances(req)
	default:
		ls.handleScopeOpen(ctx, req)
	}
}

// handleScopeOpen opens one pass's child scope on the loop goroutine and replies
// to the decorator (SRD-054 FR-8a). It performs the single-writer mutations the
// off-loop decorator must not do — open the data-plane child scope, register the
// entry, mark the host parked-for-drain, seed the inner tracks, and arm the scope
// handlers. The pass ordinal is already bound as loopCounter by the decorator (off
// the loop, §4.6) before this request, so the seeded body reads it by walk-up. Scope
// close stays on the existing drain path (completeScope), so no close request is
// needed here (§4.3).
// reattachScope re-binds a RESTORED scope to the fresh executor now asking
// for it (SRD-082 FR-3/FR-5): the scope was rebuilt at loop start and its
// inner tracks respawned, so there is nothing to open or seed — only to hand
// the instance the channel and the cell it will read its result from.
//
// Split out of handleScopeOpen when that function absorbed the retired
// loop-driven path and outgrew the complexity budget (SRD-090.A M3c).
func (ls *loopState) reattachScope(
	ctx context.Context,
	req scopeRequest,
	child scope.DataPath,
	entry *scopeEntry,
) {
	ls.waiting[req.host.ID()] = struct{}{}

	if req.iterating {
		ls.ensureIterMirror(req.host, req.node)
	}

	entry.awaitAttach = false

	// the re-attaching instance is a NEW executor over an old scope, so the
	// entry adopts its channel AND its output cell: the restored scope was
	// rebuilt by the loop and has neither of its own (SRD-090.A M3b).
	// Without the cell a resumed instance's output would be read from
	// nowhere and its slot would stay nil, which reads downstream as an
	// instance that produced nothing.
	entry.drain = req.drain
	entry.capture = req.capture

	req.reply <- scopeReply{scopePath: child}

	// a drain that arrived before the re-attach completes now — the
	// roundtrip above is the fence that makes the host state loop-readable
	// (SRD-082 FR-3).
	if entry.drainPending {
		entry.drainPending = false
		ls.completeScope(ctx, child, entry)
	}
}

// seedOpenedScope publishes into a freshly-opened child scope everything the
// body must resolve by name before it is seeded — the three seeds the merged
// open path owes, gathered here because handleScopeOpen outgrew its budget
// once it absorbed the retired loop-driven path (SRD-090.A M3c):
//
//   - a compensation event-sub handler's ledger snapshot (SRD-059 FR-4).
//     Reads inside the handler resolve child-first, so the snapshot shadows
//     the live parent data and the handler's own writes die with the scope
//     (an ADR-026 §2.5 engine note). It rides the HOST, which is why the
//     executor-driven open reaches it without the request naming it.
//   - the composite's SubProcess-level Data Objects (SRD-063 FR-4).
//   - this instance's per-pass data, published at its OWN scope so a
//     fanned-out sibling cannot overwrite it.
func (ls *loopState) seedOpenedScope(
	req scopeRequest, child scope.DataPath,
) error {
	if req.host.compScopeSeed != nil {
		if _, err := ls.inst.sc.plane.Commit(
			child, req.host.compScopeSeed...); err != nil {
			return errs.New(
				errs.M("couldn't seed compensation snapshot into %q",
					string(child)),
				errs.C(errorClass, errs.OperationFailed),
				errs.E(err))
		}
	}

	if err := seedDataObjects(ls.inst.sc.plane, req.node, child); err != nil {
		return err
	}

	for _, b := range req.binds {
		if err := ls.inst.sc.bindDataItemAt(child, b.name, b.value); err != nil {
			return err
		}
	}

	return nil
}

func (ls *loopState) handleScopeOpen(ctx context.Context, req scopeRequest) {
	// a terminating instance opens nothing (the loop-driven path returned
	// early here too). The requester is REFUSED rather than dropped: it is
	// parked in its roundtrip, and while loopDone would eventually free it,
	// an answer names the reason instead of leaving it to a race between
	// two shutdown signals.
	if ls.stopping {
		req.reply <- scopeReply{err: errs.New(
			errs.M("instance %q is terminating — scope open refused for %q",
				ls.inst.ID(), req.node.ID()),
			errs.C(errorClass, errs.InvalidState))}

		return
	}

	sh, ok := req.node.(scopeHost)
	if !ok {
		// checkNodeType only routes scopeHost nodes to the decorator; a mismatch
		// is a corrupt graph.
		req.reply <- scopeReply{err: errs.New(
			errs.M("scope open requested for a non-composite node %q",
				req.node.ID()),
			errs.C(errorClass, errs.TypeCastingError))}

		return
	}

	seg := scopeSegmentFor(req.host, req.node, req.segment)

	child, err := req.host.scopePath.Append(seg)
	if err != nil {
		req.reply <- scopeReply{err: err}

		return
	}

	// a RESTORED pass (SRD-082 FR-3/FR-5): the scope is already open —
	// its entry was derived at loop start, its inner tracks respawned as
	// initial tracks — so the runner RE-ATTACHES: no reopen, no reseed,
	// just park for the drain.
	if entry, open := ls.scopes[child]; open && entry.host == req.host {
		ls.reattachScope(ctx, req, child, entry)

		return
	}

	// a DIFFERENT host already holds this path (SRD-049 §4.4): two tokens
	// reached one composite — a gateway forked into it — and one DataPath
	// holds one scope, so the second waits. The request is queued and its
	// reply DEFERRED; completeScope serves it when the scope frees. The
	// requester is parked in its roundtrip, which already honors ctx and
	// loopDone, so a deferred reply cannot strand it.
	//
	// Recording the scope's owner (Schema 7) does not remove this: the
	// collision is between two LIVE scopes, not two readings of one name
	// (ADR-025 §2.9.3a).
	if entry, open := ls.scopes[child]; open {
		entry.queue = append(entry.queue, req)

		return
	}

	if err := ls.inst.sc.plane.OpenScope(child); err != nil {
		req.reply <- scopeReply{err: errs.New(
			errs.M("couldn't open scope %q for composite %q",
				string(child), req.node.ID()),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err))}

		return
	}

	if err := ls.seedOpenedScope(req, child); err != nil {
		req.reply <- scopeReply{err: err}

		return
	}

	// the host is recorded parked-and-undelivered (the onScopeOpen
	// discipline) — a scope this host opened is outstanding, which is what
	// keeps the loop from treating it as idle. The DRAIN itself goes to the
	// instance that opened the scope, over the channel on the entry.
	ls.waiting[req.host.ID()] = struct{}{}
	ls.scopes[child] = &scopeEntry{
		host:     req.host,
		node:     req.node,
		parent:   req.host.scopePath,
		drain:    req.drain,
		capture:  req.capture,
		instance: req.segment != "",
		ordinal:  req.ordinal,
	}

	// mirror the decorator's position for the capture (SRD-082 FR-2);
	// the runner is parked in its roundtrip, so the reads are fenced.
	if req.iterating {
		ls.ensureIterMirror(req.host, req.node)
	}

	ls.reportScope(
		observability.PhaseOpened, req.node, child, req.factOrdinal)
	ls.seedScope(ctx, sh, child)
	ls.armScopeHandlers(ctx, sh.Nodes(), child)

	// the persist point the requester asked for (req.persist): taken BEFORE
	// the reply, while the requester is still parked in its roundtrip —
	// that park is the fence which makes its state readable here, the same
	// reason the position mirror is written above.
	if req.persist {
		ls.checkpointNow(ctx)
	}

	req.reply <- scopeReply{scopePath: child}
}
