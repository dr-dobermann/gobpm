package instance

import (
	"context"

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

// firstOpen resolves the instance count once at activation, freezes it on the
// host, and publishes the 0-based ordinal. It returns open=false (zero
// instances) or an error. Only a sequential Multi-Instance reaches this path —
// compositeIteratorOf excludes a parallel MI, which the parallel driver owns
// (SRD-056.A).
func (it miIterator) firstOpen(
	ctx context.Context, _ *loopState, host *track, node flow.Node,
) (bool, error) {
	n, col, err := it.resolveActivation(ctx, host, node)
	if err != nil {
		return false, err
	}

	host.miState = &miState{
		collection:        col,
		inputItem:         it.mi.InputDataItem(),
		outputRef:         it.mi.LoopDataOutputRef(),
		outputItem:        it.mi.OutputDataItem(),
		numberOfInstances: n,
	}

	// an output-assembling Multi-Instance stages each instance's item privately
	// and publishes the collection once, at completion (the visibility barrier).
	if host.miState.outputRef != "" {
		host.miState.staging = values.NewArray[any]()
	}

	// N <= 0 runs zero instances — resume the host without opening a scope.
	if n <= 0 {
		return false, nil
	}

	if err := it.bindInstance(ctx, host, 0); err != nil {
		return false, err
	}

	return true, nil
}

// beforeClose captures the just-completed instance's output item from the
// draining child scope into the private staging collection (SRD-055), keyed by
// the instance ordinal. A no-op when the Multi-Instance assembles no output.
func (it miIterator) beforeClose(
	ctx context.Context, host *track, childPath scope.DataPath,
) error {
	st := host.miState
	if st == nil || st.staging == nil {
		return nil
	}

	d, err := host.instance.sc.plane.GetData(childPath, st.outputItem)
	if err != nil {
		return err
	}

	return st.staging.SetAt(ctx, host.loopCounter, d.Value().Get(ctx))
}

// afterDrain advances to the next instance while the count is not reached and
// the completionCondition (if any) does not stop launching; otherwise it
// publishes the assembled output collection once (the visibility barrier) and
// the activity completes.
func (it miIterator) afterDrain(
	ctx context.Context, _ *loopState, host *track, node flow.Node,
) (bool, error) {
	st := host.miState
	st.completed++

	done := st.completed >= st.numberOfInstances
	if !done && it.mi.CompletionCondition() != nil {
		met, err := it.evalCompletion(ctx, host, node)
		if err != nil {
			return false, err
		}

		done = met
	}

	if done {
		if err := it.publishOutput(host); err != nil {
			return false, err
		}

		host.loopCounter = 0
		host.miState = nil

		return false, nil
	}

	host.loopCounter++

	if err := it.bindInstance(ctx, host, host.loopCounter); err != nil {
		return false, err
	}

	return true, nil
}

// evalCompletion evaluates the boolean completionCondition at the host scope
// (where the runtime attributes are published), the evalLoopCond-shaped path. A
// non-boolean result is a modeling error surfaced to the caller.
func (it miIterator) evalCompletion(
	ctx context.Context, host *track, node flow.Node,
) (bool, error) {
	frame, err := host.instance.sc.openFrameAt(
		"mi-completion", node.ID(), host.scopePath)
	if err != nil {
		return false, err
	}
	defer frame.Discard()

	res, err := host.instance.ExpressionEngine().Evaluate(
		ctx, it.mi.CompletionCondition(),
		newExecEnv(host.instance, frame, nil))
	if err != nil {
		return false, err
	}

	met, ok := res.Get(ctx).(bool)
	if !ok {
		return false, errs.New(
			errs.M("Multi-Instance completionCondition evaluated to a "+
				"non-boolean value"),
			errs.C(errorClass, errs.TypeCastingError),
			errs.D("node_id", node.ID()))
	}

	return met, nil
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
				errs.D("node_id", node.ID()))
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

// drivesOwnIteration reports whether a looped composite drives its OWN iteration
// off the loop (the iteration decorator, ADR-025 v.2 §2.12): a Standard-Loop
// composite (runCompositeLoop) or a SEQUENTIAL Multi-Instance composite
// (runMISequential). A parallel Multi-Instance (fan-out driver) and a plain
// composite do NOT — they park for the loop-driven scope re-entry.
func drivesOwnIteration(node flow.Node) bool {
	if standardLoopOf(node) != nil {
		return true
	}

	mi := multiInstanceOf(node)

	return mi != nil && mi.IsSequential()
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

	n, col, err := it.resolveActivation(ctx, t, step.node)
	if err != nil {
		return nil, err
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
	}

	// N <= 0 runs zero instances — follow the outgoing flow once, no scope, no
	// publish (staging is unallocated).
	if n <= 0 {
		t.miState = nil

		return t.executeNode(ctx, step)
	}

	for i := 0; i < n; i++ {
		t.loopCounter = i

		// split the per-instance data at the host scope BEFORE the open, off the
		// loop (loopCounter=i, numberOf* attrs, inputItem=collection[i]); the seeded
		// body reads them by walk-up.
		if err := it.bindInstance(ctx, t, i); err != nil {
			return nil, err
		}

		if _, err := t.instance.scopeRoundtrip(ctx,
			scopeRequest{host: t, node: step.node}); err != nil {
			return nil, err
		}

		if err := t.awaitScopeDrained(ctx); err != nil {
			return nil, err
		}

		// advance the completion count, then test the completionCondition against
		// the attributes bound at THIS pass's start (not rebound) — the exact value
		// the loop-driven afterDrain exposed (§4.3). completed >= n stops via the
		// loop's natural exit; a true condition stops early ("stop launching").
		t.miState.completed++
		if t.miState.completed < n && mi.CompletionCondition() != nil {
			met, err := it.evalCompletion(ctx, t, step.node)
			if err != nil {
				return nil, err
			}

			if met {
				break
			}
		}
	}

	if err := it.publishOutput(t); err != nil {
		return nil, err
	}

	t.miState = nil
	t.loopCounter = 0

	return t.executeNode(ctx, step)
}
