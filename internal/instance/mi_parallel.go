package instance

import (
	"context"
	"strconv"

	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// miGroup is the loop-owned barrier substrate for a parallel Multi-Instance
// activity's N concurrent instance scopes (SRD-056.A): the shared host, the node,
// the input collection (nil for a cardinality-driven MI), the pre-sized staging
// collection, the still-open instance paths (path → 0-based ordinal), the output
// names, the frozen N, and the count of drains that arrived while the host runner
// was busy and could not be delivered yet. It is the N-of-N barrier the per-scope
// scopeEntry model cannot express. Loop-goroutine-owned; the off-loop decorator
// (runMIParallel) drives the counting / completion / cancellation policy and reads
// no group field mid-flight — it counts delivered drains locally and learns the
// terminated count from the scopeComplete reply.
type miGroup struct {
	host       *track
	node       flow.Node
	collection data.Collection
	staging    *values.Array[any]
	open       map[scope.DataPath]int
	inputItem  string
	outputRef  string
	outputItem string
	n          int
	pending    int
}

// runMIParallel drives a parallel Multi-Instance composite from the host's own
// runner goroutine — the off-loop iteration decorator (SRD-056.A, ADR-025 v.2
// §2.12), the fan-out-then-await-all sibling of runMISequential. It resolves N once
// off the loop, asks the loop to fan out N distinct instance scopes (scopeFanOut),
// then runs the N-of-N barrier: each delivered drain advances the completed count,
// binds the §2.9 attributes, and evaluates the completionCondition; a true condition
// asks the loop to cancel the remainder (scopeComplete cancel), otherwise the runner
// re-arms (scopeReArm) for the next drain and completes when all N have drained. The
// loop performs the single-writer mutations — open N, capture each output before its
// scope closes, cancel, publish — and delivers the concurrent drains one at a time
// through the cap-1 re-arm handshake (§4.2). On exit it follows the composite's
// single outgoing flow once.
func (t *track) runMIParallel(
	ctx context.Context, step *stepInfo, mi multiInstance,
) ([]*flow.SequenceFlow, error) {
	n, col, err := miIterator{mi: mi}.resolveActivation(ctx, t, step.node)
	if err != nil {
		return nil, err
	}

	// N <= 0 runs zero instances — follow the outgoing flow once, no fan-out, no
	// group, no publish.
	if n <= 0 {
		return t.executeNode(ctx, step)
	}

	// ask the loop to open all N instance scopes and build the group barrier; it
	// marks the host waiting before replying, so the first drain dispatches.
	if _, err := t.instance.scopeRoundtrip(ctx, scopeRequest{
		op: scopeFanOut, host: t, node: step.node, n: n, col: col,
	}); err != nil {
		return nil, err
	}

	for completed := 0; ; {
		if err := t.awaitScopeDrained(ctx); err != nil {
			return nil, err
		}

		completed++

		done, err := t.parallelBarrierStep(ctx, step, mi, n, completed)
		if err != nil {
			return nil, err
		}

		if done {
			break
		}
	}

	return t.executeNode(ctx, step)
}

// parallelBarrierStep processes one delivered instance drain of a parallel
// Multi-Instance's N-of-N barrier (SRD-056.A): it binds the §2.9 attributes off the
// loop, evaluates the completionCondition, and drives the loop-side finalize/re-arm.
// It reports done when the activity completes — the completionCondition fired (the
// loop cancels the remainder + publishes) or all N have drained (the loop publishes)
// — otherwise it re-arms for the next drain.
func (t *track) parallelBarrierStep(
	ctx context.Context, step *stepInfo, mi multiInstance, n, completed int,
) (bool, error) {
	// the delivered count is authoritative (delivery is 1:1 with completion, in
	// order), so the condition and the body see the running §2.9 attributes.
	if err := t.bindMICounters(n, completed, 0); err != nil {
		return false, err
	}

	// completionCondition true → the activity is done now: ask the loop to cancel
	// the still-open instances, publish, and drop the group (§4.5).
	if completed < n && mi.CompletionCondition() != nil {
		met, err := miIterator{mi: mi}.evalCompletion(ctx, t, step.node)
		if err != nil {
			return false, err
		}

		if met {
			r, err := t.instance.scopeExchange(ctx, scopeRequest{
				op: scopeComplete, host: t, node: step.node, cancel: true,
			})
			if err != nil {
				return false, err
			}

			return true, t.bindMICounters(n, completed, r.terminated)
		}
	}

	// all N drained with no early completion — publish and drop the group.
	if completed == n {
		_, err := t.instance.scopeRoundtrip(ctx, scopeRequest{
			op: scopeComplete, host: t, node: step.node,
		})

		return true, err
	}

	// re-arm for the next drain: the loop re-marks the host waiting and delivers
	// the next queued drain, if any (§4.2).
	_, err := t.instance.scopeRoundtrip(ctx, scopeRequest{
		op: scopeReArm, host: t, node: step.node,
	})

	return false, err
}

// bindMICounters publishes the §2.9 runtime attributes at the host scope off the
// loop (SRD-056.A FR-12) — the frozen instance count, the still-running count
// (n − completed − terminated), and the completed / terminated counts. Called by
// the decorator after each delivered drain, before it evaluates the
// completionCondition. A mutex-safe plane write, like the sequential slice's
// off-loop binds.
func (t *track) bindMICounters(n, completed, terminated int) error {
	binds := []miBinding{
		{name: "numberOfInstances", value: n},
		{name: "numberOfActiveInstances", value: n - completed - terminated},
		{name: "numberOfCompletedInstances", value: completed},
		{name: "numberOfTerminatedInstances", value: terminated},
	}

	for _, b := range binds {
		if err := t.instance.sc.bindDataItemAt(
			t.scopePath, b.name, b.value); err != nil {
			return err
		}
	}

	return nil
}

// handleFanOut opens all N instance scopes of a parallel Multi-Instance on the loop
// goroutine, builds the loop-owned group barrier, marks the host waiting for the
// whole group, and replies to the decorator (SRD-056.A FR-3). Because it runs to
// completion before the loop processes any drain, every instance is registered
// before the first drain — so a fast instance cannot resume the host before the
// fan-out finishes (§4.6). A partial-open error leaves cleanup to the runner's fault
// (stopAll cancels the subtree).
func (ls *loopState) handleFanOut(ctx context.Context, req scopeRequest) {
	sh, ok := req.node.(scopeHost)
	if !ok {
		// checkNodeType only routes scopeHost nodes to the decorator; a mismatch
		// is a corrupt graph.
		req.reply <- scopeReply{err: errs.New(
			errs.M("scope fan-out requested for a non-composite node %q",
				req.node.ID()),
			errs.C(errorClass, errs.TypeCastingError))}

		return
	}

	mi := multiInstanceOf(req.node)

	// the host parks once for the whole fan-out; it resumes as each instance
	// drains, delivered one at a time via the re-arm handshake.
	ls.waiting[req.host.ID()] = struct{}{}

	grp := &miGroup{
		host:       req.host,
		node:       req.node,
		collection: req.col,
		open:       make(map[scope.DataPath]int, req.n),
		inputItem:  mi.InputDataItem(),
		outputRef:  mi.LoopDataOutputRef(),
		outputItem: mi.OutputDataItem(),
		n:          req.n,
	}

	// an output-assembling Multi-Instance stages into a slice PRE-SIZED to N, so an
	// instance completing out of order writes its slot by ordinal (SetAt is a
	// replace, not an append); a canceled slot keeps its pre-run nil (§2.7).
	if grp.outputRef != "" {
		grp.staging = values.NewArray[any](make([]any, req.n)...)
	}

	ls.miGroups[req.host.ID()] = grp

	for i := 0; i < req.n; i++ {
		if err := ls.openParallelInstance(
			ctx, grp, req.host, req.node, sh, i); err != nil {
			req.reply <- scopeReply{err: err}

			return
		}
	}

	req.reply <- scopeReply{}
}

// handleReArm re-marks the host waiting and delivers the next queued drain, if any
// accumulated while the runner was busy (SRD-056.A FR-6) — the handshake step that
// serializes N concurrent drains onto the cap-1 park. If the group is already gone
// (a boundary tore it down), it just replies so the runner unblocks. Runs on the
// loop goroutine.
func (ls *loopState) handleReArm(ctx context.Context, req scopeRequest) {
	grp, ok := ls.miGroups[req.host.ID()]
	if !ok {
		req.reply <- scopeReply{}

		return
	}

	ls.waiting[req.host.ID()] = struct{}{}

	if grp.pending > 0 {
		grp.pending--
		ls.dispatchToParked(ctx, trackEvent{
			kind:  evDeliver,
			track: req.host,
			eDef:  newScopeDone(),
		})
	}

	req.reply <- scopeReply{}
}

// handleComplete finalizes a parallel Multi-Instance group (SRD-056.A FR-10/FR-11):
// when the request carries cancel it tears down every still-open instance scope as a
// unit (a completionCondition fired, §4.5) and reports the terminated count; it then
// publishes the assembled staging collection once at the host scope (the visibility
// barrier) and drops the group. Runs on the loop goroutine.
func (ls *loopState) handleComplete(req scopeRequest) {
	grp, ok := ls.miGroups[req.host.ID()]
	if !ok {
		req.reply <- scopeReply{}

		return
	}

	terminated := 0
	if req.cancel {
		terminated = ls.cancelOpenInstances(grp)
	}

	if grp.staging != nil {
		if err := ls.inst.sc.bindValueAt(
			grp.host.scopePath, grp.outputRef, grp.staging); err != nil {
			req.reply <- scopeReply{err: err}

			return
		}
	}

	delete(ls.miGroups, req.host.ID())

	req.reply <- scopeReply{terminated: terminated}
}

// openParallelInstance opens instance i's distinct scope (segment `sp-<id>-i`),
// registers it in the group, publishes its Opened fact with its OWN ordinal
// (FR-14, not the shared host.loopCounter), and seeds + arms the inner tracks.
// It returns an error the caller faults on.
func (ls *loopState) openParallelInstance(
	ctx context.Context, grp *miGroup, host *track, node flow.Node,
	sh scopeHost, i int,
) error {
	child, err := host.scopePath.Append(scopeSegment(node) + "-" + strconv.Itoa(i))
	if err != nil {
		return err
	}

	if err := ls.inst.sc.plane.OpenScope(child); err != nil {
		return errs.New(
			errs.M("couldn't open parallel instance scope %q for %q",
				string(child), node.ID()),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err))
	}

	entry := &scopeEntry{
		host:    host,
		group:   grp,
		node:    node,
		parent:  host.scopePath,
		ordinal: i,
	}
	ls.scopes[child] = entry
	grp.open[child] = i

	ls.reportScope(observability.PhaseOpened, node, child, i)

	// bind instance i's per-pass data at its OWN scope (concurrency-safe, unlike
	// the sequential slice's host-scope bind): the 0-based loopCounter and, when
	// collection-driven, the split input item. Bound before the body is seeded so
	// it reads them by name.
	binds := []miBinding{{name: "loopCounter", value: i}}
	if grp.collection != nil {
		elem, err := grp.collection.GetAt(ctx, i)
		if err != nil {
			return err
		}

		binds = append(binds, miBinding{name: grp.inputItem, value: elem})
	}

	for _, b := range binds {
		if err := ls.inst.sc.bindDataItemAt(child, b.name, b.value); err != nil {
			return err
		}
	}

	ls.seedScope(ctx, sh, child)
	ls.armScopeHandlers(ctx, sh.Nodes(), child)

	return nil
}

// captureParallelOutput reads a completing instance's output item from its
// draining child scope into the group's private staging slot (keyed by the
// instance ordinal, so out-of-order completion still lands positionally). A
// no-op when the activity assembles no output. Runs on the loop goroutine from
// completeScope, before the child scope closes.
func (ls *loopState) captureParallelOutput(
	ctx context.Context, entry *scopeEntry, path scope.DataPath,
) error {
	grp := entry.group
	if grp.staging == nil {
		return nil
	}

	d, err := ls.inst.sc.plane.GetData(path, grp.outputItem)
	if err != nil {
		return err
	}

	return grp.staging.SetAt(ctx, entry.ordinal, d.Value().Get(ctx))
}

// cancelOpenInstances cancels every still-open instance scope of the group as a
// unit (ADR-018 mechanism) and clears the open set, returning the count.
func (ls *loopState) cancelOpenInstances(grp *miGroup) int {
	paths := make([]scope.DataPath, 0, len(grp.open))
	for p := range grp.open {
		paths = append(paths, p)
	}

	for _, p := range paths {
		ls.cancelScope(p, observability.PhaseCanceled)
	}

	grp.open = map[scope.DataPath]int{}

	return len(paths)
}

// cancelParallelGroup tears down all of a parallel Multi-Instance's open
// instance scopes and drops the group — the companion for an interrupting
// boundary firing on the fanned-out host (SRD-056.A FR-13). Unlike a
// completionCondition (which completes the activity), the host is itself being
// canceled, so the group is abandoned rather than resumed.
func (ls *loopState) cancelParallelGroup(grp *miGroup) {
	ls.cancelOpenInstances(grp)
	delete(ls.miGroups, grp.host.ID())
}
