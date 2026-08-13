package instance

import (
	"context"

	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// scopeOp is the operation a scopeRequest asks the single-writer loop to perform
// for an off-loop iteration decorator (ADR-025 v.2 §2.12). scopeOpen is the
// serial open (Standard Loop / sequential MI, one child scope per pass); the
// remaining ops drive a PARALLEL Multi-Instance's fan-out-then-await-all barrier
// (SRD-056.A), whose N concurrent drains the cap-1 runner park cannot take at
// once — so the loop delivers them one at a time through the re-arm handshake.
type scopeOp int

const (
	// scopeOpen opens one child scope for a serial pass and parks the host for
	// its drain (Standard Loop, sequential MI).
	scopeOpen scopeOp = iota
	// scopeFanOut opens all N instance scopes of a parallel Multi-Instance,
	// builds the loop-owned group barrier, and marks the host waiting.
	scopeFanOut
	// scopeReArm re-marks the host waiting and delivers the next queued drain —
	// the handshake step that serializes N concurrent drains onto the cap-1 park.
	scopeReArm
	// scopeComplete finalizes the group: optionally cancels the still-open
	// instances (a completionCondition fired), publishes the assembled output,
	// and drops the group.
	scopeComplete
	// scopeNote informs the loop's iteration mirror that the runner's
	// completionCondition fired (SRD-082 FR-2) — the one decorator
	// decision the loop cannot observe from the open/drain protocol.
	scopeNote
	// scopeLeafPass advances the loop's iteration mirror for a
	// SEQUENTIAL LEAF Multi-Instance (SRD-086 FR-5): a leaf opens no
	// scope, so the mirror can't ride the open/drain protocol — the
	// runner posts each completed pass instead (n carries the count);
	// the roundtrip is the fence that makes the miState reads safe.
	scopeLeafPass
	// scopeReAttach is a RESTORED parallel runner re-joining its adopted
	// group (SRD-082 FR-4): the loop lifts the entries' awaitAttach
	// holds — the roundtrip is the fence — and completes any drains
	// that arrived early.
	scopeReAttach
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
// (SRD-054 §2.12 / FR-8a; SRD-056.A for the parallel ops): host is the composite
// host track, node is the composite node, and reply carries the loop's verdict
// back to the decorator's runner goroutine. For a serial open the pass ordinal is
// bound as loopCounter by the decorator itself, off the loop (§4.6) — a plane
// write, mutex-safe like the leaf loop's bind — so it is set before the
// continuation test reads it and before the scope opens. n / col carry a parallel
// fan-out's instance count and input collection; cancel asks scopeComplete to tear
// down the still-open instances first.
type scopeRequest struct {
	col   data.Collection
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
	// insts carries a LEAF decorator's executor set to the loop's
	// iteration mirror on a scopeLeafPass (SRD-090.A FR-6): a leaf opens
	// no scope and spawns no track, so this post is the ONLY thing that
	// can tell the capture which instances are live.
	insts []checkpoint.IterationInstance
	// binds are the per-instance data items published at the CHILD scope
	// before its body is seeded — the 0-based loopCounter, and the split
	// input item when the iteration is collection-driven. Concurrency-safe
	// where the sequential slice's host-scope bind is not, because each
	// instance writes only its own scope.
	binds []miBinding
	op    scopeOp
	n     int
	// ordinal is the instance's own 0-based index, reported as its Opened
	// fact (FR-14) rather than the host's shared loopCounter. Read only
	// when segment is set, since that is what marks a per-instance open.
	ordinal int
	cancel  bool
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

// scopeReply is the loop's answer to a scopeRequest: the opened child path (open),
// the count of instances torn down (scopeComplete with cancel), or an error the
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
// opened path (a parallel scopeComplete reads the terminated count) can read it.
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
	case scopeFanOut:
		ls.handleFanOut(ctx, req)
	case scopeReArm:
		ls.handleReArm(ctx, req)
	case scopeComplete:
		ls.handleComplete(req)
	case scopeNote:
		if m, ok := ls.iter[req.host.ID()]; ok {
			m.conditionMet = true
		}

		req.reply <- scopeReply{}
	case scopeLeafPass:
		m := ls.ensureIterMirror(req.host, req.node)
		m.completed = req.n
		m.instances = req.insts

		req.reply <- scopeReply{}

		// a completed leaf pass IS the observable transition (ADR-033
		// §2.2): the whole N-pass run is one step execution emitting no
		// track events, so without this the position never persists
		// (SRD-086 FR-5).
		ls.checkpointNow(ctx)
	case scopeReAttach:
		ls.handleReAttach(ctx, req)
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
func (ls *loopState) handleScopeOpen(ctx context.Context, req scopeRequest) {
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

	// the executor names the segment when it is one of N fanned-out
	// instances; otherwise the node's own is the segment, unchanged.
	seg := req.segment
	if seg == "" {
		seg = scopeSegment(req.node)
	}

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
		ls.waiting[req.host.ID()] = struct{}{}

		if drivesOwnIteration(req.node) {
			ls.ensureIterMirror(req.host, req.node)
		}

		entry.awaitAttach = false

		// the re-attaching instance is a NEW executor over an old scope, so
		// the entry adopts its channel: the restored scope was rebuilt by
		// the loop and has none of its own (SRD-090.A M3b).
		entry.drain = req.drain

		req.reply <- scopeReply{scopePath: child}

		// a drain that arrived before the re-attach completes now — the
		// roundtrip above is the fence that makes the host state
		// loop-readable (SRD-082 FR-3).
		if entry.drainPending {
			entry.drainPending = false
			ls.completeScope(ctx, child, entry)
		}

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

	// the host is recorded parked-and-undelivered (the onScopeOpen
	// discipline) — a scope this host opened is outstanding, which is what
	// keeps the loop from treating it as idle. The DRAIN itself goes to the
	// instance that opened the scope, over the channel on the entry.
	ls.waiting[req.host.ID()] = struct{}{}
	ls.scopes[child] = &scopeEntry{
		host:    req.host,
		node:    req.node,
		parent:  req.host.scopePath,
		drain:   req.drain,
		capture: req.capture,
	}

	// mirror the decorator's position for the capture (SRD-082 FR-2);
	// the runner is parked in its roundtrip, so the reads are fenced.
	if drivesOwnIteration(req.node) {
		ls.ensureIterMirror(req.host, req.node)
	}

	// instance i's per-pass data, published at its OWN scope before the
	// body is seeded, so the body reads it by name.
	for _, b := range req.binds {
		if err := ls.inst.sc.bindDataItemAt(child, b.name, b.value); err != nil {
			req.reply <- scopeReply{err: err}

			return
		}
	}

	// a fanned-out instance reports its OWN ordinal, not the host's shared
	// loopCounter (SRD-056.A FR-14); a sequential pass has no ordinal of
	// its own and the host's counter is the position.
	ord := scopeLoopCounter(req.node, req.host)
	if req.segment != "" {
		ord = req.ordinal
	}

	ls.reportScope(observability.PhaseOpened, req.node, child, ord)
	ls.seedScope(ctx, sh, child)
	ls.armScopeHandlers(ctx, sh.Nodes(), child)

	req.reply <- scopeReply{scopePath: child}
}

// awaitScopeDrained parks the decorator's runner on evtCh for the pass's scope
// drain — the loop delivers a scopeDone the same way it resumes any parked
// composite host (dispatchToParked). It honors ctx cancellation and the loop
// closing evtCh on stop, so a mid-pass interrupt/terminate unblocks the decorator
// (SRD-054 NFR-4).
func (t *track) awaitScopeDrained(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case _, ok := <-t.evtCh:
		if !ok {
			return context.Canceled
		}

		return nil
	}
}
