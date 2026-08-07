package instance

import (
	"context"
	"encoding/json"

	"github.com/dr-dobermann/gobpm/pkg/observability"

	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"

	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// multiInstance is the runtime capability a Multi-Instance marker exposes
// (ADR-025 §2.4–§2.9, BPMN §13.3.7). Recognizing it by capability — not by a
// concrete type — keeps the runtime model-agnostic and, because the method set
// differs from standardLoop, makes the two detectors mutually exclusive.
type multiInstance interface {
	IsSequential() bool
	LoopCardinality() data.FormalExpression
	CompletionCondition() data.FormalExpression
	LoopDataInputRef() string
	LoopDataOutputRef() string
	InputDataItem() string
	OutputDataItem() string
	Behavior() activities.MultiInstanceBehavior
	NoneBehaviorEvent() flow.EventDefinition
	OneBehaviorEvent() flow.EventDefinition
	ComplexBehavior() []*activities.ComplexBehaviorDefinition
}

// multiInstanceOf reports the node's Multi-Instance characteristics, or nil when
// the node is not a Multi-Instance activity.
func multiInstanceOf(node flow.Node) multiInstance {
	lch, ok := node.(interface {
		LoopCharacteristics() activities.LoopCharacteristics
	})
	if !ok {
		return nil
	}

	mi, ok := lch.LoopCharacteristics().(multiInstance)
	if !ok {
		return nil
	}

	return mi
}

// miState is a sequential Multi-Instance host's iteration state (SRD-055): the
// instance count fixed at activation, the input collection (nil for a
// cardinality-driven Multi-Instance), the per-instance item name, the private
// output staging collection assembled across instances (nil when the activity
// assembles no output), the output ref/item names, and the running count of
// completed instances. Owned by the host RUNNER goroutine (runMISequential drives
// it off the loop, ADR-025 v.2 §2.12), with ONE deliberate cross-goroutine field:
// `staging` receives a per-pass `SetAt` from the loop (captureSequentialOutput /
// the beforeClose capture, which must read the child scope before it closes) —
// fenced by the scopeDone-on-evtCh edge and safe because MI is sequential (one
// slot in flight, the publish read after the last drain). nil for a non-MI host.
type miState struct {
	collection        data.Collection
	staging           *values.Array[any]
	inputItem         string
	outputRef         string
	outputItem        string
	numberOfInstances int
	completed         int
}

// miBinding is one named per-pass datum a Multi-Instance publishes at the host
// scope for the body to resolve by name.
type miBinding struct {
	value any
	name  string
}

// miIterator is the composite-iteration strategy for a Multi-Instance activity:
// it re-opens the host's child scope a fixed number of times (§13.3.7). This
// slice drives the sequential shape; the per-instance data mediator and the
// completion condition land in later milestones.
type miIterator struct {
	mi multiInstance
}

// captureSequentialOutput reads a draining sequential-MI instance's output item
// from its child scope into the private staging collection (SRD-055 FR-9), keyed
// by the instance ordinal — the one loop-side step of the off-loop decorator, run
// from completeScope before the child scope closes (§4.2). A no-op for a non-MI
// scope or a Multi-Instance that assembles no output. Runs on the loop goroutine.
func (ls *loopState) captureSequentialOutput(
	ctx context.Context, entry *scopeEntry, childPath scope.DataPath,
) error {
	st := entry.host.miState
	if st == nil || st.staging == nil {
		return nil
	}

	d, err := ls.inst.sc.plane.GetData(childPath, st.outputItem)
	if err != nil {
		return err
	}

	return st.staging.SetAt(ctx, entry.host.loopCounterSnap(), d.Value().Get(ctx))
}

// evalCompletion evaluates the boolean completionCondition at the host scope
// (where the runtime attributes are published). A non-boolean result is a modeling
// error surfaced to the caller.
func (it miIterator) evalCompletion(
	ctx context.Context, host *track, node flow.Node,
) (bool, error) {
	return host.evalBoolAtHost(
		ctx, "completionCondition", node.ID(), it.mi.CompletionCondition())
}

// evalBoolAtHost evaluates a boolean expression against a transient frame at the
// host scope (where the §2.9 attributes are published) — the shape shared by the
// completionCondition and a Complex behavior condition (SRD-056.B). `what` names
// the expression in a non-boolean error; a non-boolean result is a modeling error.
func (t *track) evalBoolAtHost(
	ctx context.Context, what, nodeID string, expr data.FormalExpression,
) (bool, error) {
	frame, err := t.instance.sc.openFrameAt(what, nodeID, t.scopePath)
	if err != nil {
		return false, err
	}
	defer frame.Discard()

	res, err := t.instance.ExpressionEngine().Evaluate(
		ctx, expr, newExecEnv(t.instance, frame, nil))
	if err != nil {
		return false, err
	}

	b, ok := res.Get(ctx).(bool)
	if !ok {
		return false, errs.New(
			errs.M("Multi-Instance %s evaluated to a non-boolean value", what),
			errs.C(errorClass, errs.TypeCastingError),
			errs.D(observability.AttrNodeID, nodeID))
	}

	return b, nil
}

// publishOutput commits the staged output collection at the host scope under the
// loopDataOutputRef name — the single visibility barrier that makes the assembled
// result readable only once every instance has contributed. A no-op when the
// activity assembles no output.
func (it miIterator) publishOutput(host *track) error {
	st := host.miState
	if st.staging == nil {
		return nil
	}

	return host.instance.sc.bindValueAt(
		host.scopePath, st.outputRef, st.staging)
}

// resolveActivation computes the instance count once (§13.3.7): the integer
// loopCardinality expression when present, otherwise the size of the
// loopDataInputRef collection. The collection is returned (nil for a
// cardinality-driven Multi-Instance) so the per-instance item split reuses it
// without a second lookup.
func (it miIterator) resolveActivation(
	ctx context.Context, host *track, node flow.Node,
) (int, data.Collection, error) {
	if expr := it.mi.LoopCardinality(); expr != nil {
		frame, err := host.instance.sc.openFrameAt(
			"mi-card", node.ID(), host.scopePath)
		if err != nil {
			return 0, nil, err
		}
		defer frame.Discard()

		res, err := host.instance.ExpressionEngine().Evaluate(
			ctx, expr, newExecEnv(host.instance, frame, nil))
		if err != nil {
			return 0, nil, err
		}

		n, ok := res.Get(ctx).(int)
		if !ok {
			return 0, nil, errs.New(
				errs.M("Multi-Instance cardinality evaluated to a "+
					"non-integer value"),
				errs.C(errorClass, errs.TypeCastingError),
				errs.D(observability.AttrNodeID, node.ID()))
		}

		return n, nil, nil
	}

	d, err := host.instance.sc.plane.GetData(
		host.scopePath, it.mi.LoopDataInputRef())
	if err != nil {
		return 0, nil, err
	}

	col, ok := d.Value().(data.Collection)
	if !ok {
		return 0, nil, errs.New(
			errs.M("Multi-Instance loopDataInputRef %q is not a collection",
				it.mi.LoopDataInputRef()),
			errs.C(errorClass, errs.TypeCastingError))
	}

	return col.Count(), col, nil
}

// bindInstance publishes instance i's per-pass data at the host (enclosing)
// scope, where the body resolves it by name via walk-up (SRD-055): the 0-based
// loopCounter, the §13.3.7 runtime attributes, and — when the Multi-Instance is
// collection-driven — the per-instance item split from element i. Binding at
// the enclosing scope is safe for a sequential Multi-Instance (one instance
// runs at a time, each pass rebinds before opening) and mirrors the loopCounter
// and the Event-Sub-Process payload precedent.
func (it miIterator) bindInstance(
	ctx context.Context, host *track, i int,
) error {
	st := host.miState

	binds := []miBinding{
		{name: "loopCounter", value: i},
		{name: "numberOfInstances", value: st.numberOfInstances},
		{name: "numberOfActiveInstances", value: 1},
		{name: "numberOfCompletedInstances", value: st.completed},
	}

	if st.collection != nil {
		elem, err := st.collection.GetAt(ctx, i)
		if err != nil {
			return err
		}

		binds = append(binds, miBinding{name: st.inputItem, value: elem})
	}

	for _, b := range binds {
		if err := host.instance.sc.bindDataItemAt(
			host.scopePath, b.name, b.value); err != nil {
			return err
		}
	}

	return nil
}

// prepareSequential resolves the activation, builds the host's miState
// (staging included) and applies a restored seed (SRD-082 FR-3): the
// recorded N is the frozen activation count (§13.3.7) — the cardinality
// expression must not re-resolve to a different bound after the restart
// — and the recorded staging returns the completed passes' outputs. It
// returns the frozen instance count and the first pass to launch.
func (t *track) prepareSequential(
	ctx context.Context, it miIterator, mi multiInstance, step *stepInfo,
) (int, int, error) {
	n, col, err := it.resolveActivation(ctx, t, step.node)
	if err != nil {
		return 0, 0, err
	}

	seed := t.miSeed
	t.miSeed = nil

	if seed != nil && seed.N > 0 {
		n = seed.N

		if col != nil && col.Count() < n {
			return 0, 0, errs.New(
				errs.M("restored Multi-Instance %q: the input collection "+
					"holds %d items, the recorded activation froze %d",
					step.node.ID(), col.Count(), n),
				errs.C(errorClass, errs.InvalidState))
		}
	}

	t.miState = &miState{
		collection:        col,
		inputItem:         mi.InputDataItem(),
		outputRef:         mi.LoopDataOutputRef(),
		outputItem:        mi.OutputDataItem(),
		numberOfInstances: n,
	}

	// an output-assembling Multi-Instance stages each instance's item privately and
	// publishes the collection once, at completion (the visibility barrier).
	if t.miState.outputRef != "" {
		t.miState.staging = values.NewArray[any]()

		if seed != nil && seed.Staging != nil {
			if serr := seedStaging(ctx, t.miState.staging,
				seed.Staging); serr != nil {
				return 0, 0, serr
			}
		}
	}

	if seed == nil || n <= 0 {
		return n, 0, nil
	}

	start, err := t.seedSequentialStart(ctx, it, mi, step, seed, n)
	if err != nil {
		return 0, 0, err
	}

	return n, start, nil
}

// seedSequentialStart applies a restored position (SRD-082 FR-3): the
// completed count stands, and the iteration's stop verdict is the
// recorded one — or, when the capture predates the runner's note, the
// SAME predicate re-evaluated over the restored data and the recorded
// counters (deterministic, so never "backwards"). It returns the first
// pass to launch: n when the iteration already stopped.
func (t *track) seedSequentialStart(
	ctx context.Context, it miIterator, mi multiInstance,
	step *stepInfo, seed *checkpoint.MIRecord, n int,
) (int, error) {
	start := seed.Completed
	t.miState.completed = start

	if start <= 0 || start >= n {
		return start, nil
	}

	stop := seed.ConditionMet

	if !stop && mi.CompletionCondition() != nil {
		if err := t.bindMICounters(n, start, 0); err != nil {
			return 0, err
		}

		met, err := it.evalCompletion(ctx, t, step.node)
		if err != nil {
			return 0, err
		}

		stop = met
	}

	if stop {
		return n, nil // completed passes stand; no further launches
	}

	return start, nil
}

// seedStaging decodes a recorded staging array into the fresh staging
// collection at its recorded positions (SRD-082 FR-3).
func seedStaging(
	ctx context.Context, staging *values.Array[any], raw json.RawMessage,
) error {
	v, err := checkpoint.DecodeValue(ctx, raw)
	if err != nil {
		return err
	}

	col, ok := v.(data.Collection)
	if !ok {
		return errs.New(
			errs.M("restored Multi-Instance staging isn't a collection"),
			errs.C(errorClass, errs.InvalidState))
	}

	for i, el := range col.GetAll(ctx) {
		// the codec wraps scalars in canonical values; the live staging
		// holds RAW elements (captureSequentialOutput stores Get's
		// result) — unwrap, so the published output stays uniform.
		if v, ok := el.(data.Value); ok {
			el = v.Get(ctx)
		}

		if err := staging.SetAt(ctx, i, el); err != nil {
			return err
		}
	}

	return nil
}

// drivesOwnIteration reports whether a looped composite drives its OWN iteration
// off the loop (the iteration decorator, ADR-025 v.2 §2.12): a Standard-Loop
// composite (runCompositeLoop) or ANY Multi-Instance composite — sequential
// (runMISequential, await-each) or parallel (runMIParallel, fan-out-then-await-all).
// A plain (non-looped) composite does NOT — it parks for the loop-driven scope
// re-entry.
func drivesOwnIteration(node flow.Node) bool {
	if standardLoopOf(node) != nil {
		return true
	}

	return multiInstanceOf(node) != nil
}

// runMISequential drives a sequential Multi-Instance composite from the host's own
// runner goroutine — the off-loop iteration decorator (SRD-055, ADR-025 v.2
// §2.12), the count-driven sibling of runCompositeLoop. It resolves N once, then
// for each instance splits the input datum off the loop, requests the child scope
// (scopeRoundtrip), parks for the drain (awaitScopeDrained — during which the loop
// captures the instance's output before the scope closes, §4.2), advances the
// completion count, and stops early when the completionCondition holds. On exit it
// publishes the assembled output once (the visibility barrier) and follows the
// composite's single outgoing flow. It reuses the miIterator's resolveActivation /
// bindInstance / evalCompletion / publishOutput, only relocating the control off
// the loop.
func (t *track) runMISequential(
	ctx context.Context, step *stepInfo, mi multiInstance,
) ([]*flow.SequenceFlow, error) {
	it := miIterator{mi: mi}

	n, start, err := t.prepareSequential(ctx, it, mi, step)
	if err != nil {
		return nil, err
	}

	// N <= 0 runs zero instances — follow the outgoing flow once, no scope, no
	// publish (staging is unallocated).
	if n <= 0 {
		t.miState = nil

		return t.executeNode(ctx, step)
	}

	for i := start; i < n; i++ {
		t.setLoopCounter(i)

		// split the per-instance data at the host scope BEFORE the open, off the
		// loop (loopCounter=i, numberOf* attrs, inputItem=collection[i]); the seeded
		// body reads them by walk-up.
		if err := it.bindInstance(ctx, t, i); err != nil {
			return nil, err
		}

		if _, err := t.instance.scopeRoundtrip(ctx,
			scopeRequest{op: scopeOpen, host: t, node: step.node}); err != nil {
			return nil, err
		}

		if err := t.awaitScopeDrained(ctx); err != nil {
			return nil, err
		}

		// advance the completion count, then test the completionCondition against
		// the attributes bound at THIS pass's start (not rebound) — the exact value
		// SRD-055 exposed. completed >= n stops via the loop's natural exit; a true
		// condition stops early ("stop launching"). Capture the decision before the
		// behavior rebind so the condition keeps its pass-start counts.
		t.miState.completed++

		stop := false
		if t.miState.completed < n && mi.CompletionCondition() != nil {
			met, err := it.evalCompletion(ctx, t, step.node)
			if err != nil {
				return nil, err
			}

			stop = met
		}

		// SRD-056.B: the behavior event carries the CURRENT §2.9 counts, so
		// republish the post-drain counts (sequential binds them at pass start),
		// then throw before the activity completes (FR-6/FR-7).
		if err := t.bindMICounters(n, t.miState.completed, 0); err != nil {
			return nil, err
		}

		if err := t.throwMIBehavior(
			ctx, mi, step.node, t.miState.completed); err != nil {
			return nil, err
		}

		if stop {
			// the loop's iteration mirror cannot observe this verdict from
			// the open/drain protocol — post it (SRD-082 FR-2).
			if _, err := t.instance.scopeExchange(ctx,
				scopeRequest{op: scopeNote, host: t}); err != nil {
				return nil, err
			}

			break
		}
	}

	if err := it.publishOutput(t); err != nil {
		return nil, err
	}

	t.miState = nil
	t.setLoopCounter(0)

	return t.executeNode(ctx, step)
}
