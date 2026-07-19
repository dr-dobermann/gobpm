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
// (SRD-055): the instance count fixed at activation. Read and written only on
// the loop goroutine; nil for a non-MI host.
type miState struct {
	numberOfInstances int
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
	ctx context.Context, ls *loopState, host *track, node flow.Node,
) (bool, error) {
	if !it.mi.IsSequential() {
		return false, errs.New(
			errs.M("parallel Multi-Instance is not yet implemented (SRD-056)"),
			errs.C(errorClass, errs.OperationFailed),
			errs.D("node_id", node.ID()))
	}

	n, err := it.resolveCardinality(ctx, host, node)
	if err != nil {
		return false, err
	}

	host.miState = &miState{numberOfInstances: n}

	if err := ls.inst.sc.bindLoopCounterAt(host.scopePath, 0); err != nil {
		return false, err
	}

	// N <= 0 runs zero instances — resume the host without opening a scope.
	return n > 0, nil
}

// afterDrain advances to the next instance while the count is not reached,
// re-opening the child scope for another pass; otherwise the activity completes.
func (it miIterator) afterDrain(
	_ context.Context, ls *loopState, host *track, _ flow.Node,
) (bool, error) {
	host.loopCounter++

	if host.loopCounter >= host.miState.numberOfInstances {
		host.loopCounter = 0
		host.miState = nil

		return false, nil
	}

	if err := ls.inst.sc.bindLoopCounterAt(
		host.scopePath, host.loopCounter); err != nil {
		return false, err
	}

	return true, nil
}

// resolveCardinality computes the instance count once (§13.3.7): the integer
// loopCardinality expression when present, otherwise the size of the
// loopDataInputRef collection.
func (it miIterator) resolveCardinality(
	ctx context.Context, host *track, node flow.Node,
) (int, error) {
	if expr := it.mi.LoopCardinality(); expr != nil {
		frame, err := host.instance.sc.openFrameAt(
			"mi-card", node.ID(), host.scopePath)
		if err != nil {
			return 0, err
		}
		defer frame.Discard()

		res, err := host.instance.ExpressionEngine().Evaluate(
			ctx, expr, newExecEnv(host.instance, frame, nil))
		if err != nil {
			return 0, err
		}

		n, ok := res.Get(ctx).(int)
		if !ok {
			return 0, errs.New(
				errs.M("Multi-Instance cardinality evaluated to a "+
					"non-integer value"),
				errs.C(errorClass, errs.TypeCastingError),
				errs.D("node_id", node.ID()))
		}

		return n, nil
	}

	d, err := host.instance.sc.plane.GetData(
		host.scopePath, it.mi.LoopDataInputRef())
	if err != nil {
		return 0, err
	}

	col, ok := d.Value().(data.Collection)
	if !ok {
		return 0, errs.New(
			errs.M("Multi-Instance loopDataInputRef %q is not a collection",
				it.mi.LoopDataInputRef()),
			errs.C(errorClass, errs.TypeCastingError))
	}

	return col.Count(), nil
}
