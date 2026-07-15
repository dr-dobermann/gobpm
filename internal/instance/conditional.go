package instance

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// condWatch is one armed conditional subscription, loop-owned (SRD-048 FR-8,
// ADR-006 v.3 §2.7): track is the parked catch/EBG track and node its event
// node. deps are the condition's declared read paths, extracted once at arm
// (data.DependencyLister; nil = may read anything → re-evaluate on every
// non-empty commit). last is the edge state: the normative false→true rule
// (BPMN Table 10.84) fires only on a transition, so the last observed value
// is kept per subscription.
type condWatch struct {
	track *track
	node  flow.Node
	def   *events.ConditionalEventDefinition
	deps  []string
	last  bool
}

// conditionalDefs filters the Conditional definitions out of a catch node's
// definition list, preserving their declaration order — the loop arms them in
// exactly this order, and arming order is the multi-fire contract (SRD-048
// FR-8/FR-11).
func conditionalDefs(
	defs []flow.EventDefinition,
) []*events.ConditionalEventDefinition {
	var cc []*events.ConditionalEventDefinition

	for _, d := range defs {
		if ced, ok := d.(*events.ConditionalEventDefinition); ok {
			cc = append(cc, ced)
		}
	}

	return cc
}

// condDeps extracts the declared dependency paths of a conditional
// definition's condition (data.DependencyLister), or nil when the expression
// declares nothing — the always-re-evaluate fallback (ADR-006 v.3 §2.7).
func condDeps(def *events.ConditionalEventDefinition) []string {
	if dl, ok := def.Condition().(data.DependencyLister); ok {
		return dl.Dependencies()
	}

	return nil
}

// condDue reports whether a commit's changed paths make a subscription due
// for re-evaluation — the uniform rule (SRD-048 FR-11): no dependency
// statement → always due; otherwise due when any declared path overlaps any
// changed path (segment-prefix, data.PathsOverlap). A malformed path — both
// sides are pre-validated, so this is defensive — fails toward re-evaluation,
// never toward a silent skip.
func condDue(deps []string, changes []data.Change) bool {
	if len(deps) == 0 {
		return true
	}

	for _, dep := range deps {
		for _, c := range changes {
			if ov, err := data.PathsOverlap(dep, c.Path); err != nil || ov {
				return true
			}
		}
	}

	return false
}

// signalDataCommit emits the commit-diff to the instance loop (evDataCommit,
// SRD-048 FR-10) after a node's frame commit produced changes. Gated on the
// snapshot's precomputed HasConditionals — a conditional-free process never
// pays the emit (NFR-1) — and, like the evWaiting emit, on the Active state
// (before the loop runs nothing is armed; after stop nothing fires). Runs on
// the track goroutine.
func (t *track) signalDataCommit(node flow.Node, changes []data.Change) {
	if len(changes) == 0 || !t.instance.s.HasConditionals ||
		t.instance.State() != Active {
		return
	}

	t.instance.emit(trackEvent{
		kind:    evDataCommit,
		track:   t,
		node:    node,
		changes: changes,
	})
}

// armConditionals arms every conditional definition the parked track carries
// (SRD-048 FR-8/FR-9): each becomes a condWatch appended in declaration order,
// then arm-time evaluated — a condition already true fires immediately
// (ADR-006 v.3 §2.7), which flips the track out of waiting and tears its
// entries down, so arming stops there. The subscribed node is read from the
// loop-owned position view, never from the track cross-goroutine. Runs on the
// loop goroutine.
func (ls *loopState) armConditionals(ctx context.Context, t *track) {
	for _, def := range t.condDefs {
		w := &condWatch{
			track: t,
			node:  ls.position[t.ID()],
			def:   def,
			deps:  condDeps(def),
		}

		ls.conds = append(ls.conds, w)

		ls.inst.report(observability.Fact{
			Kind:     observability.KindEventFlow,
			Phase:    observability.PhaseRegistered,
			NodeID:   nodeIDOf(w.node),
			NodeName: nodeNameOf(w.node),
			Details: map[string]string{
				observability.AttrEventDefinitionID: def.ID(),
			},
		})

		val, ok := ls.evalCondWatch(ctx, w)
		if !ok {
			return // evaluation failure — the instance is stopping
		}

		w.last = val

		if val {
			ls.fireCondWatch(ctx, w)

			return // delivered — the track's remaining entries are gone
		}
	}
}

// sweepConditionals re-evaluates the armed conditionals against one committed
// change set — the uniform per-subscription rule with the false→true edge
// (SRD-048 FR-11, ADR-006 v.3 §2.7). One commit, one sweep: every due
// subscription is evaluated against the current committed scope and its edge
// state advanced; the resulting fires apply in arming order, and a disarming
// fire (a delivered catch flips its track, tearing its entries down) voids
// later-collected fires whose entry is gone. Runs on the loop goroutine.
func (ls *loopState) sweepConditionals(ctx context.Context, changes []data.Change) {
	if ls.stopping || len(ls.conds) == 0 {
		return
	}

	fires := make([]*condWatch, 0, len(ls.conds))

	for _, w := range ls.conds {
		if !condDue(w.deps, changes) {
			continue
		}

		val, ok := ls.evalCondWatch(ctx, w)
		if !ok {
			return // evaluation failure — the instance is stopping
		}

		if !w.last && val {
			fires = append(fires, w)
		}

		w.last = val
	}

	for _, w := range fires {
		if !ls.condArmed(w) {
			// a disarming fire earlier in this sweep tore this entry down —
			// the delivery is void (ADR-006 v.3 §2.7 multi-fire ordering).
			ls.inst.report(observability.Fact{
				Kind:     observability.KindEventFlow,
				Phase:    observability.PhaseDropped,
				NodeID:   nodeIDOf(w.node),
				NodeName: nodeNameOf(w.node),
				Details: map[string]string{
					observability.AttrEventDefinitionID: w.def.ID(),
				},
			})

			continue
		}

		ls.fireCondWatch(ctx, w)
	}
}

// evalCondWatch evaluates w's condition over the instance's committed scope:
// a transient root frame wrapped as the execution environment gives the
// expression engine its data source (the authorizeTask idiom), discarded
// after. A failure — the frame, the evaluation, or a non-bool result (guarded
// at model build; defensive here) — fails the instance (fail + stopAll): a
// condition the engine cannot evaluate leaves the model's declared wait
// meaningless, the fail-fast class. Returns ok=false then. Runs on the loop
// goroutine.
func (ls *loopState) evalCondWatch(
	ctx context.Context,
	w *condWatch,
) (val, ok bool) {
	res, err := ls.evalCondition(ctx, w.def)
	if err != nil {
		ls.inst.fail(errs.New(
			errs.M("conditional evaluation failed"),
			errs.C(errorClass, errs.OperationFailed),
			errs.D("node_id", nodeIDOf(w.node)),
			errs.D("event_definition_id", w.def.ID()),
			errs.E(err)))
		ls.stopAll()

		return false, false
	}

	return res, true
}

// evalCondition runs one conditional definition's expression over a transient
// root frame and returns its boolean result. Split from evalCondWatch so the
// failure policy (fail the instance) stays in one place while the mechanics
// stay testable.
func (ls *loopState) evalCondition(
	ctx context.Context,
	def *events.ConditionalEventDefinition,
) (bool, error) {
	frame, err := ls.inst.sc.openFrame("cond-eval", def.ID())
	if err != nil {
		return false, err
	}
	defer frame.Discard()

	res, err := ls.inst.ExpressionEngine().Evaluate(
		ctx, def.Condition(), newExecEnv(ls.inst, frame))
	if err != nil {
		return false, err
	}

	b, ok := res.Get(ctx).(bool)
	if !ok {
		return false, errs.New(
			errs.M("condition evaluated to a non-boolean value"),
			errs.C(errorClass, errs.TypeCastingError),
			errs.D("event_definition_id", def.ID()))
	}

	return b, nil
}

// fireCondWatch delivers a fired conditional to its parked track through the
// standard parked-dispatch contract (SRD-048 FR-14): dispatchToParked drops
// the delivery if the track is no longer parked (a losing deferred-choice
// arm), and on success flips it out of waiting — which tears down the track's
// conditional entries via flipNotParked → clearConds. Runs on the loop
// goroutine.
func (ls *loopState) fireCondWatch(ctx context.Context, w *condWatch) {
	ls.inst.report(observability.Fact{
		Kind:     observability.KindEventFlow,
		Phase:    observability.PhaseFired,
		NodeID:   nodeIDOf(w.node),
		NodeName: nodeNameOf(w.node),
		Details: map[string]string{
			observability.AttrEventDefinitionID: w.def.ID(),
		},
	})

	ls.dispatchToParked(ctx, trackEvent{
		kind:  evDeliver,
		track: w.track,
		eDef:  w.def,
	})
}

// condArmed reports whether w is still in the armed registry — a fire
// collected earlier in a sweep may have torn it down (the multi-fire voiding
// rule). Pointer identity; the registry is small (armed subscriptions of one
// instance).
func (ls *loopState) condArmed(w *condWatch) bool {
	for _, c := range ls.conds {
		if c == w {
			return true
		}
	}

	return false
}

// clearConds removes every armed conditional belonging to tr — on delivery
// (via flipNotParked), on track end/failure, and on instance teardown its
// subscriptions must not outlive the parked episode (SRD-048 FR-8). Runs on
// the loop goroutine.
func (ls *loopState) clearConds(tr *track) {
	if len(ls.conds) == 0 {
		return
	}

	kept := ls.conds[:0]

	for _, w := range ls.conds {
		if w.track != tr {
			kept = append(kept, w)
		}
	}

	// nil out the dropped tail so freed entries don't linger reachable.
	for i := len(kept); i < len(ls.conds); i++ {
		ls.conds[i] = nil
	}

	ls.conds = kept
}

// nodeNameOf returns n.Name(), or noneLabel for a nil node — the Name peer of
// nodeIDOf for log/fact lines reading the loop-owned position map.
func nodeNameOf(n flow.Node) string {
	if n == nil {
		return noneLabel
	}

	return n.Name()
}
