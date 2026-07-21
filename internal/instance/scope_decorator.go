package instance

import (
	"context"

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
	col    data.Collection
	host   *track
	node   flow.Node
	reply  chan scopeReply
	op     scopeOp
	n      int
	cancel bool
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

	child, err := req.host.scopePath.Append(scopeSegment(req.node))
	if err != nil {
		req.reply <- scopeReply{err: err}

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

	// the host parked on its evtCh for this pass's drain — record it
	// parked-and-undelivered so the drain's synthetic completion can dispatch to
	// it (the onScopeOpen discipline).
	ls.waiting[req.host.ID()] = struct{}{}
	ls.scopes[child] = &scopeEntry{
		host:   req.host,
		node:   req.node,
		parent: req.host.scopePath,
	}

	ls.reportScope(observability.PhaseOpened, req.node, child,
		scopeLoopCounter(req.node, req.host))
	ls.seedScope(ctx, sh, child)
	ls.armScopeHandlers(ctx, sh.Nodes(), child)

	req.reply <- scopeReply{scopePath: child}
}

// runCompositeLoop drives a looped composite Standard Loop from the host's own
// runner goroutine — the off-loop iteration decorator (SRD-054 FR-8, ADR-025 v.2
// §2.12). Each pass it binds the ordinal (off the loop, like the leaf loop), tests
// the continuation, requests the loop to open the child scope, and parks for the
// drain; on exit it selects the composite's single outgoing flow once. This
// replaces the loop-goroutine-driven firstOpen/afterDrain seam for a Standard-Loop
// composite; the loop stays the sole writer of the scope (it only responds to the
// open request and delivers the drain).
func (t *track) runCompositeLoop(
	ctx context.Context, step *stepInfo, sl standardLoop,
) ([]*flow.SequenceFlow, error) {
	for pass := 0; ; pass++ {
		// publish the 0-based ordinal (track field + host-scope datum) so the
		// condition and the body resolve it by name via walk-up, and it survives
		// the child close for the next pass's test (§4.6). Off the loop — a
		// plane write, mutex-safe, mirroring runStandardLoop's leaf bind.
		t.loopCounter = pass
		if err := t.instance.sc.bindLoopCounterAt(t.scopePath, pass); err != nil {
			return nil, err
		}

		// pre-tested (while) tests every pass; post-tested (do-while) skips the
		// first — one test site, matching runStandardLoop.
		if sl.TestBefore() || pass > 0 {
			cont, err := t.evalLoopCond(ctx, step.node, sl)
			if err != nil {
				return nil, err
			}

			if !cont {
				break
			}
		}

		// ask the loop to open the child scope for this pass and block for the
		// acknowledgement (single-writer), then park for the scope's drain — the
		// loop delivers scopeDone on evtCh, as for any composite host.
		if _, err := t.instance.scopeRoundtrip(ctx,
			scopeRequest{op: scopeOpen, host: t, node: step.node}); err != nil {
			return nil, err
		}

		if err := t.awaitScopeDrained(ctx); err != nil {
			return nil, err
		}

		if m, ok := sl.LoopMaximum(); ok && pass+1 >= m {
			break
		}
	}

	// the loop finished — follow the composite's outgoing once (SubProcess.Exec
	// selects it; the body already ran through the scope, so this only routes the
	// token onward), mirroring runStandardLoop's single post-loop exit.
	return t.executeNode(ctx, step)
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
