package instance

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
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

// miState is a sequential Multi-Instance host's loop-owned iteration state
// (SRD-055): the instance count fixed at activation, the input collection (nil
// for a cardinality-driven Multi-Instance), the per-instance item name, and the
// running count of completed instances. Read and written only on the loop
// goroutine; nil for a non-MI host.
type miState struct {
	collection        data.Collection
	inputItem         string
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
// instances) or an error; a parallel Multi-Instance is rejected here until
// SRD-056.
func (it miIterator) firstOpen(
	ctx context.Context, _ *loopState, host *track, node flow.Node,
) (bool, error) {
	if !it.mi.IsSequential() {
		return false, errs.New(
			errs.M("parallel Multi-Instance is not yet implemented (SRD-056)"),
			errs.C(errorClass, errs.OperationFailed),
			errs.D("node_id", node.ID()))
	}

	n, col, err := it.resolveActivation(ctx, host, node)
	if err != nil {
		return false, err
	}

	host.miState = &miState{
		collection:        col,
		inputItem:         it.mi.InputDataItem(),
		numberOfInstances: n,
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

// afterDrain advances to the next instance while the count is not reached,
// re-opening the child scope for another pass; otherwise the activity completes.
func (it miIterator) afterDrain(
	ctx context.Context, _ *loopState, host *track, _ flow.Node,
) (bool, error) {
	st := host.miState
	st.completed++
	host.loopCounter++

	if host.loopCounter >= st.numberOfInstances {
		host.loopCounter = 0
		host.miState = nil

		return false, nil
	}

	if err := it.bindInstance(ctx, host, host.loopCounter); err != nil {
		return false, err
	}

	return true, nil
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
