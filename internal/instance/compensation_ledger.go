package instance

import (
	"strconv"

	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// The completion ledger (ADR-026 §2.1, SRD-059 FR-3): each open scope's ordered
// record of the compensable activities that completed inside it, with the data
// snapshot captured at each Completed (FR-4). Loop-owned — appended on the loop
// goroutine at evMoved (a leaf) and completeScope (a composite), folded
// child→parent at scope completion, discarded when the enclosing scope
// finishes. Activities that never completed never enter — the presumed-abort
// principle. Models without compensation handlers allocate nothing (NFR-2).

// ledgerEntry is one compensable completion.
type ledgerEntry struct {
	activityID   string
	activityName string
	// handlerID names the compensation handler: the boundary's linked
	// isForCompensation activity, or the compensation Event Sub-Process node
	// (handlerEventSub). Empty for a handler-less Sub-Process entry that only
	// carries folded children (a targeted throw may address one of them).
	handlerID   string
	handlerName string
	// snapshot is the value-copy of the data visible at the activity's scope
	// path at its Completed (FR-4) — the handler's future read surface.
	snapshot []data.Data
	// folded is a completed child Sub-Process's ledger, reparented here at the
	// child scope's completion (ADR-026 §2.1).
	folded []*ledgerEntry
	// ordinal is the entry's 0-based completion order within its scope — the
	// reverse-compensation order's authority (ADR-026 §2.4).
	ordinal         int
	handlerEventSub bool
}

// compensationBoundaryHandlerOf returns the handler activity linked by the
// node's Compensation boundary, or nil — the leaf compensable test.
func compensationBoundaryHandlerOf(node flow.Node) flow.ActivityNode {
	host, ok := node.(boundaryHoster)
	if !ok {
		return nil
	}

	for _, en := range host.BoundaryEvents() {
		bev, ok := en.(flow.BoundaryEvent)
		if !ok {
			continue
		}

		for _, d := range bev.Definitions() {
			if d.Type() != flow.TriggerCompensation {
				continue
			}

			if ch, ok := en.(interface {
				CompensationHandler() flow.ActivityNode
			}); ok {
				return ch.CompensationHandler()
			}
		}
	}

	return nil
}

// opensChildScope reports whether the node completes through the scope
// machinery (an embedded Sub-Process) or runs a child instance (a Call
// Activity) — both are excluded from the evMoved leaf hook: a Sub-Process is
// ledgered at completeScope, and a Call Activity never enters a ledger
// (ADR-026 §2.9, cross-instance compensation is out of scope).
func opensChildScope(node flow.Node) bool {
	if _, ok := node.(interface{ IsEventSubProcess() bool }); ok {
		return true
	}

	if _, ok := node.(interface{ CalledKey() string }); ok {
		return true
	}

	return false
}

// recordLeafCompletion appends a ledger entry for a departed leaf activity
// with a Compensation boundary (the evMoved hook: moving off a node is its
// successful completion — a failed node arrives as evFailed and a canceled one
// never moves, so this hook is inherently the presumed-abort filter).
func (ls *loopState) recordLeafCompletion(t *track, departed flow.Node) {
	if opensChildScope(departed) {
		return
	}

	h := compensationBoundaryHandlerOf(departed)
	if h == nil {
		return
	}

	ls.appendLedgerEntry(t.scopePath, &ledgerEntry{
		activityID:   departed.ID(),
		activityName: departed.Name(),
		handlerID:    h.ID(),
		handlerName:  h.Name(),
	})
}

// recordScopeCompletion ledgers a completing composite scope (the
// completeScope hook, BEFORE the data plane closes — the last point its data
// is readable): the Sub-Process becomes the parent scope's entry when it has
// its own handler (a Compensation boundary or a compensation Event
// Sub-Process, FR-7) or when its child ledger is non-empty (folded entries
// stay addressable by a targeted throw). MI/loop composite scopes are skipped —
// their compensation rides the ADR-025 §2.10 deferral.
func (ls *loopState) recordScopeCompletion(
	path scope.DataPath,
	entry *scopeEntry,
) {
	if entry.group != nil || compositeIteratorOf(entry.node) != nil {
		return
	}

	child := ls.ledgers[path]
	delete(ls.ledgers, path)

	le := &ledgerEntry{
		activityID:   entry.node.ID(),
		activityName: entry.node.Name(),
		folded:       child,
	}

	if w := ls.compensationHandlerAt(path); w != nil {
		le.handlerID = w.handler.ID()
		le.handlerName = w.handler.Name()
		le.handlerEventSub = true
	} else if h := compensationBoundaryHandlerOf(entry.node); h != nil {
		le.handlerID = h.ID()
		le.handlerName = h.Name()
	} else if len(child) == 0 {
		return // not compensable, nothing folded — no entry.
	}

	ls.appendLedgerEntry(entry.parent, le)

	if len(child) > 0 {
		ls.inst.report(observability.Fact{
			Kind:     observability.KindCompensation,
			Phase:    observability.PhaseFolded,
			NodeID:   entry.node.ID(),
			NodeName: entry.node.Name(),
			Details: map[string]string{
				observability.AttrScopePath: string(entry.parent),
				observability.AttrOrdinal:   strconv.Itoa(le.ordinal),
				"folded_entries":            strconv.Itoa(len(child)),
			},
		})
	}
}

// compensationHandlerAt returns the scope's armed compensation Event
// Sub-Process watch, or nil (FR-7: recorded at fold time, never hub-armed).
func (ls *loopState) compensationHandlerAt(
	path scope.DataPath,
) *scopeHandlerWatch {
	for _, w := range ls.scopeHandlers[path] {
		if w.def.Type() == flow.TriggerCompensation {
			return w
		}
	}

	return nil
}

// appendLedgerEntry snapshots the visible data at path, assigns the completion
// ordinal, appends the entry, and reports it Eligible (NFR-3). A snapshot
// failure is an invariant violation — the entry's future handler could not be
// guaranteed its read surface — so it fails the instance loudly.
func (ls *loopState) appendLedgerEntry(path scope.DataPath, le *ledgerEntry) {
	snap, err := ls.inst.sc.plane.SnapshotAt(path)
	if err != nil {
		ls.inst.fail(errs.New(
			errs.M("couldn't snapshot %q for the compensation ledger at %q",
				le.activityName, string(path)),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err)))
		ls.stopAll()

		return
	}

	le.snapshot = snap
	le.ordinal = len(ls.ledgers[path])
	ls.ledgers[path] = append(ls.ledgers[path], le)

	ls.inst.report(observability.Fact{
		Kind:     observability.KindCompensation,
		Phase:    observability.PhaseEligible,
		NodeID:   le.activityID,
		NodeName: le.activityName,
		Details: map[string]string{
			observability.AttrScopePath: string(path),
			observability.AttrOrdinal:   strconv.Itoa(le.ordinal),
		},
	})
}

// discardLedgers drops the ledgers of path and every scope under it — the
// normal end of the eligibility window (the enclosing scope finished or was
// canceled; ADR-006 §2.3). Every never-compensated entry is reported Discarded
// so the observer stream accounts for where it went (NFR-3).
func (ls *loopState) discardLedgers(path scope.DataPath) {
	for p, book := range ls.ledgers {
		if !underScope(p, path) {
			continue
		}

		for _, le := range book {
			ls.inst.report(observability.Fact{
				Kind:     observability.KindCompensation,
				Phase:    observability.PhaseDiscarded,
				NodeID:   le.activityID,
				NodeName: le.activityName,
				Details: map[string]string{
					observability.AttrScopePath: string(p),
					observability.AttrOrdinal:   strconv.Itoa(le.ordinal),
				},
			})
		}

		delete(ls.ledgers, p)
	}
}
