package instance

import (
	"context"
	"strconv"

	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// miGroup coordinates a parallel Multi-Instance activity's N concurrent instance
// scopes (SRD-056.A): the shared host, the node, the frozen instance count, and
// the set of still-open instance paths (path → 0-based ordinal). It is the
// N-of-N barrier the per-scope scopeEntry model cannot express — the host
// resumes only when the last instance drains. Loop-goroutine-owned; M2/M3 extend
// it with the output staging and the completed / terminated counters.
type miGroup struct {
	host *track
	node flow.Node
	open map[scope.DataPath]int
	n    int
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
	n, _, err := miIterator{mi: multiInstanceOf(node)}.
		resolveActivation(ctx, host, node)
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
		host: host,
		node: node,
		open: make(map[scope.DataPath]int, n),
		n:    n,
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

	ls.seedScope(ctx, sh, child)
	ls.armScopeHandlers(ctx, sh.Nodes(), child)

	return nil
}

// parallelInstanceDrained records one instance scope of a parallel
// Multi-Instance as drained; when the group's last instance completes it resumes
// the shared host with the synthetic completion and drops the group (SRD-056.A).
// Runs on the loop goroutine (from completeScope).
func (ls *loopState) parallelInstanceDrained(
	ctx context.Context, path scope.DataPath, entry *scopeEntry,
) {
	grp := entry.group
	delete(grp.open, path)

	if len(grp.open) > 0 {
		return
	}

	delete(ls.miGroups, grp.host.ID())

	ls.dispatchToParked(ctx, trackEvent{
		kind:  evDeliver,
		track: grp.host,
		eDef:  newScopeDone(),
	})
}
