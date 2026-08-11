package instance

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// runLeafPass executes ONE instance of a sequential leaf activity, through
// the executor the decorator holds for it (SRD-088.A M2). Originally
// SRD-086 FR-1's pass: bind,
// execute in a fresh frame, capture the output, post the pass to the
// loop's iteration mirror (the checkpoint's position — a leaf opens no
// scope, so the record rides this roundtrip instead of the drain),
// evaluate the completionCondition with pass-start counts, and throw
// the behavior event. stop reports a fired condition, posted to the
// mirror the same way the composite's is (SRD-082 FR-2).
func (t *track) runLeafPass(
	ctx context.Context, it miIterator, mi multiInstance,
	step *stepInfo, i, n int, inst activityExec,
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

	flows, err := inst.run(ctx)
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
