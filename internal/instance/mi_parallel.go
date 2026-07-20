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

// miGroup coordinates a parallel Multi-Instance activity's N concurrent instance
// scopes (SRD-056.A): the shared host, the node, the frozen instance count, and
// the set of still-open instance paths (path → 0-based ordinal). It is the
// N-of-N barrier the per-scope scopeEntry model cannot express — the host
// resumes only when the last instance drains. Loop-goroutine-owned; M3 extends it
// with the completed / terminated counters and the completionCondition.
type miGroup struct {
	host       *track
	node       flow.Node
	mi         multiInstance
	collection data.Collection    // nil for a cardinality-driven Multi-Instance
	staging    *values.Array[any] // pre-sized to N; nil when no output is assembled
	open       map[scope.DataPath]int
	inputItem  string
	outputRef  string
	outputItem string
	n          int
	completed  int
	terminated int
}

// fanOutParallelMI opens all N instance scopes of a parallel Multi-Instance host
// at activation, each at a distinct segment `sp-<id>-i`, and parks the host once
// for the whole group (ADR-025 §2.5). N is fixed here (`resolveActivation`,
// reused). Because it runs to completion on the loop goroutine, every instance
// is registered in the group before any drain is processed — so a fast instance
// cannot resume the host before the fan-out finishes. N ≤ 0 completes the
// activity with zero instances.
func (ls *loopState) fanOutParallelMI(
	ctx context.Context, host *track, node flow.Node, sh scopeHost,
) {
	mi := multiInstanceOf(node)

	n, col, err := miIterator{mi: mi}.resolveActivation(ctx, host, node)
	if err != nil {
		ls.inst.fail(err)
		ls.stopAll()

		return
	}

	// the host parks once for the whole fan-out; it resumes when the group's
	// last instance drains (or immediately, with zero instances).
	ls.waiting[host.ID()] = struct{}{}

	if n <= 0 {
		ls.dispatchToParked(ctx, trackEvent{
			kind:  evDeliver,
			track: host,
			eDef:  newScopeDone(),
		})

		return
	}

	grp := &miGroup{
		host:       host,
		node:       node,
		mi:         mi,
		collection: col,
		open:       make(map[scope.DataPath]int, n),
		inputItem:  mi.InputDataItem(),
		outputRef:  mi.LoopDataOutputRef(),
		outputItem: mi.OutputDataItem(),
		n:          n,
	}

	// an output-assembling Multi-Instance stages into a slice PRE-SIZED to N, so
	// an instance completing out of order writes its slot by ordinal (SetAt is a
	// replace, not an append); a canceled slot keeps its pre-run nil (§2.7).
	if grp.outputRef != "" {
		grp.staging = values.NewArray[any](make([]any, n)...)
	}

	ls.miGroups[host.ID()] = grp

	for i := 0; i < n; i++ {
		if err := ls.openParallelInstance(ctx, grp, host, node, sh, i); err != nil {
			ls.inst.fail(err)
			ls.stopAll()

			return
		}
	}
}

// openParallelInstance opens instance i's distinct scope (segment `sp-<id>-i`),
// registers it in the group, publishes its Opened fact with its OWN ordinal
// (FR-11, not the shared host.loopCounter), and seeds + arms the inner tracks.
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

// parallelInstanceDrained records one instance scope of a parallel
// Multi-Instance as drained; when the group's last instance completes it
// publishes the assembled output collection once (the visibility barrier),
// resumes the shared host, and drops the group (SRD-056.A). It returns an error
// the caller faults on. Runs on the loop goroutine (from completeScope).
func (ls *loopState) parallelInstanceDrained(
	ctx context.Context, path scope.DataPath, entry *scopeEntry,
) error {
	grp := entry.group
	delete(grp.open, path)
	grp.completed++

	// publish the running §2.9 attributes at the host scope for the
	// completionCondition (and the body) to resolve by name.
	if err := ls.bindParallelCounters(grp); err != nil {
		return err
	}

	done := len(grp.open) == 0

	// completionCondition true → the activity is done now: cancel the still-open
	// instances as a unit (§2.7).
	if !done && grp.mi.CompletionCondition() != nil {
		met, err := miIterator{mi: grp.mi}.
			evalCompletion(ctx, grp.host, grp.node)
		if err != nil {
			return err
		}

		if met {
			ls.cancelRemainingInstances(grp)

			done = true
		}
	}

	if !done {
		return nil
	}

	if grp.staging != nil {
		if err := ls.inst.sc.bindValueAt(
			grp.host.scopePath, grp.outputRef, grp.staging); err != nil {
			return err
		}
	}

	delete(ls.miGroups, grp.host.ID())

	ls.dispatchToParked(ctx, trackEvent{
		kind:  evDeliver,
		track: grp.host,
		eDef:  newScopeDone(),
	})

	return nil
}

// bindParallelCounters publishes the §2.9 runtime attributes at the host scope:
// the frozen instance count, the still-running count, and the completed /
// terminated counts as they progress (SRD-056.A FR-9).
func (ls *loopState) bindParallelCounters(grp *miGroup) error {
	binds := []miBinding{
		{name: "numberOfInstances", value: grp.n},
		{name: "numberOfActiveInstances", value: len(grp.open)},
		{name: "numberOfCompletedInstances", value: grp.completed},
		{name: "numberOfTerminatedInstances", value: grp.terminated},
	}

	for _, b := range binds {
		if err := ls.inst.sc.bindDataItemAt(
			grp.host.scopePath, b.name, b.value); err != nil {
			return err
		}
	}

	return nil
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

// cancelRemainingInstances tears down the group's still-open instance scopes
// because a completionCondition fired (§2.7) and counts them terminated. Each
// canceled instance keeps its pre-run output slot (its nil).
func (ls *loopState) cancelRemainingInstances(grp *miGroup) {
	grp.terminated += ls.cancelOpenInstances(grp)
}

// cancelParallelGroup tears down all of a parallel Multi-Instance's open
// instance scopes and drops the group — the companion for an interrupting
// boundary firing on the fanned-out host (SRD-056.A FR-10). Unlike a
// completionCondition (which completes the activity), the host is itself being
// canceled, so the group is abandoned rather than resumed.
func (ls *loopState) cancelParallelGroup(grp *miGroup) {
	ls.cancelOpenInstances(grp)
	delete(ls.miGroups, grp.host.ID())
}
