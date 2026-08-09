package instance

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// runLeafMISequential drives a sequential Multi-Instance LEAF activity
// in place on the host's own runner (SRD-086 FR-1, ADR-025 v.2 §2.2):
// per pass it binds the split item and the 0-based loopCounter at the
// host scope (the walk-up the activity reads by name), re-runs
// executeNode — whose fresh per-pass frame IS the iteration isolation,
// the runStandardLoop mechanism — captures the declared output item
// into the staging slot, and evaluates the completionCondition. On
// exit the assembled output publishes once and the single outgoing
// flow is followed once. Before this decorator existed, a leaf MI fell
// through executeStep to a SINGLE executeNode — the silent single-run
// SRD-086 §1 documents.
func (t *track) runLeafMISequential(
	ctx context.Context, step *stepInfo, mi multiInstance,
) ([]*flow.SequenceFlow, error) {
	it := miIterator{mi: mi}

	n, start, err := t.prepareSequential(ctx, it, mi, step)
	if err != nil {
		return nil, err
	}

	// N <= 0 runs zero passes — the activity itself does not execute,
	// and the token leaves via the declared outgoing flow.
	if n <= 0 {
		t.miState = nil

		return step.node.Outgoing(), nil
	}

	var nextFlows []*flow.SequenceFlow

	for i := start; i < n; i++ {
		flows, stop, err := t.runLeafPass(ctx, it, mi, step, i, n)
		if err != nil {
			return nil, err
		}

		nextFlows = flows

		if stop {
			break
		}
	}

	if err := it.publishOutput(t); err != nil {
		return nil, err
	}

	t.miState = nil
	t.setLoopCounter(0)

	// a condition-stopped (or zero-flow) run still leaves via the
	// activity's declared outgoing flow, exactly once.
	if nextFlows == nil {
		nextFlows = step.node.Outgoing()
	}

	return nextFlows, nil
}

// runLeafPass executes ONE sequential leaf pass (SRD-086 FR-1): bind,
// execute in a fresh frame, capture the output, post the pass to the
// loop's iteration mirror (the checkpoint's position — a leaf opens no
// scope, so the record rides this roundtrip instead of the drain),
// evaluate the completionCondition with pass-start counts, and throw
// the behavior event. stop reports a fired condition, posted to the
// mirror the same way the composite's is (SRD-082 FR-2).
func (t *track) runLeafPass(
	ctx context.Context, it miIterator, mi multiInstance,
	step *stepInfo, i, n int,
) ([]*flow.SequenceFlow, bool, error) {
	t.setLoopCounter(i)

	// bind loopCounter, the §2.9 counts and inputItem=collection[i] at
	// the host scope — the activity's frame resolves them by walk-up,
	// exactly as a seeded composite body does.
	if err := it.bindInstance(ctx, t, i); err != nil {
		return nil, false, err
	}

	// re-arm the step for another execution (the runStandardLoop
	// idiom): finalizeNodeExecution ended the previous pass.
	step.state = StepCreated

	flows, err := t.executeNode(ctx, step)
	if err != nil {
		return nil, false, err
	}

	if err := t.captureLeafOutput(ctx, i); err != nil {
		return nil, false, err
	}

	t.miState.completed++

	if _, err := t.instance.scopeExchange(ctx, scopeRequest{
		op: scopeLeafPass, host: t, n: t.miState.completed,
	}); err != nil {
		return nil, false, err
	}

	stop := false

	if t.miState.completed < n && mi.CompletionCondition() != nil {
		met, cerr := it.evalCompletion(ctx, t, step.node)
		if cerr != nil {
			return nil, false, cerr
		}

		stop = met
	}

	// the behavior event carries the CURRENT §2.9 counts (SRD-056.B):
	// republish post-drain, then throw.
	if err := t.bindMICounters(n, t.miState.completed, 0); err != nil {
		return nil, false, err
	}

	if err := t.throwMIBehavior(
		ctx, mi, step.node, t.miState.completed); err != nil {
		return nil, false, err
	}

	if stop {
		if _, err := t.instance.scopeExchange(ctx,
			scopeRequest{op: scopeNote, host: t}); err != nil {
			return nil, false, err
		}
	}

	return flows, stop, nil
}

// captureLeafOutput reads the pass's declared output item from the
// host scope — where the leaf's UploadData just committed it — into
// the staging slot for ordinal i (SRD-086 FR-1); a no-op when the
// activity assembles no output.
func (t *track) captureLeafOutput(ctx context.Context, i int) error {
	st := t.miState
	if st == nil || st.staging == nil {
		return nil
	}

	d, err := t.instance.sc.plane.GetData(t.scopePath, st.outputItem)
	if err != nil {
		return err
	}

	return st.staging.SetAt(ctx, i, d.Value().Get(ctx))
}
