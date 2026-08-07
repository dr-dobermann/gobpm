package thresher_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/repository"
	"github.com/dr-dobermann/gobpm/pkg/repository/memrepo"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// SRD-082 T-7/T-8 — durable Call Activity children: the kill-and-resume
// re-link, the loud missing-counterpart refusals, the
// completed-while-down fast path, and the discovery separation.

// gateCondExpr is true iff gate > 0 — the parkable callee's condition,
// evaluated at (re-)arming.
func gateCondExpr(t *testing.T, gate *atomic.Int32) data.FormalExpression {
	t.Helper()

	return goexpr.Must(nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(_ context.Context, _ data.Source) (data.Value, error) {
			return values.NewVariable(gate.Load() > 0), nil
		})
}

// parkingCallee builds start → catch(gate) → end under key, ids pinned
// implicitly by sharing the SAME process object across engines.
func parkingCallee(
	t *testing.T, key string, gate *atomic.Int32,
) *process.Process {
	t.Helper()

	return parkingCalleeWith(t, key, gateCondExpr(t, gate))
}

// parkingCalleeWith is parkingCallee over an explicit condition.
func parkingCalleeWith(
	t *testing.T, key string, cond data.FormalExpression,
) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New("callee", foundation.WithID(key))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	def, err := events.NewConditionalEventDefinition(cond)
	require.NoError(t, err)

	catch, err := events.NewIntermediateCatchEvent("gate", def)
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, catch, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, catch)
	link(t, catch, end)

	return p
}

// callerOf builds start → invoke(callee) → end under key.
func callerOf(
	t *testing.T, key string, callee *process.Process,
) *process.Process {
	t.Helper()

	p, err := process.New("caller", foundation.WithID(key))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	ca, err := activities.NewCallActivity("invoke", callee.ID())
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, ca, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, ca)
	link(t, ca, end)

	return p
}

// bootCallEngine runs an engine over the shared repo in recoveryGroup
// with every given process registered before Run.
func bootCallEngine(
	t *testing.T, name string, repo repository.Repository,
	ttl time.Duration, procs ...*process.Process,
) (*thresher.Thresher, *factWatch, context.CancelFunc) {
	t.Helper()

	th, err := thresher.New(name,
		thresher.WithoutBanner(), thresher.WithoutStartupConfig(),
		thresher.WithRepository(repo),
		thresher.WithEngineGroup(recoveryGroup),
		thresher.WithLeaseTTL(ttl))
	require.NoError(t, err)

	fw := &factWatch{}
	sub := th.Observe(fw)
	t.Cleanup(sub.Cancel)

	ctx, cancel := context.WithCancel(context.Background())

	for _, p := range procs {
		_, err = th.RegisterProcess(p)
		require.NoError(t, err)
	}

	require.NoError(t, th.Run(ctx))

	return th, fw, cancel
}

// parkedCall drives engine-1 to "parent parked on the call, child
// parked on its gate", and returns the parent/child ids once both
// records are durable.
func parkedCall(
	t *testing.T, repo repository.Repository,
	parent *process.Process, callee *process.Process,
) (parentID, childID string, cancel context.CancelFunc) {
	t.Helper()

	th1, _, cancel := bootCallEngine(t, "engine-1", repo,
		80*time.Millisecond, parent, callee)

	h, err := th1.StartLatest(parent.ID())
	require.NoError(t, err)

	parentID = h.ID()

	require.Eventually(t, func() bool {
		rec, ok, _ := repo.Load(context.Background(), parentID)
		if !ok {
			return false
		}

		doc, err := checkpoint.Unmarshal(rec.Payload)
		if err != nil || len(doc.Calls) != 1 {
			return false
		}

		childID = doc.Calls[0].ChildID

		crec, cok, _ := repo.Load(context.Background(), childID)

		return cok && crec.Status == repository.StatusActive
	}, 3*time.Second, 10*time.Millisecond,
		"both ends of the call must reach the store")

	return parentID, childID, cancel
}

// TestCallKillAndResume is T-7's core: both records survive the crash,
// the recovering engine re-links the SAME child (never a duplicate),
// the child completes and the parent resumes through the re-link.
func TestCallKillAndResume(t *testing.T) {
	repo := memrepo.New()

	var gate atomic.Int32

	callee := parkingCallee(t, "cr-call-ee", &gate)
	parent := callerOf(t, "cr-call-er", callee)

	parentID, childID, cancel1 := parkedCall(t, repo, parent, callee)
	defer cancel1() // abandonment, not termination

	time.Sleep(120 * time.Millisecond) // the lease lapses
	gate.Store(1)                      // the recovered gate will open

	th2, fw2, cancel2 := bootCallEngine(t, "engine-2", repo,
		time.Minute, parent, callee)
	defer cancel2()

	require.Eventually(t, func() bool {
		rec, ok, _ := repo.Load(context.Background(), parentID)

		return ok && rec.Status == repository.StatusCompleted &&
			rec.Lease.Owner == "engine-2"
	}, 3*time.Second, 10*time.Millisecond,
		"the parent must resume through the re-link and complete")

	crec, ok, _ := repo.Load(context.Background(), childID)
	require.True(t, ok)
	require.Equal(t, repository.StatusCompleted, crec.Status)

	// no duplicate child: every call-start fact on engine-2 names the
	// RECORDED child — a re-invoke would mint a fresh id.
	for _, f := range fw2.callStarts() {
		require.Equal(t, childID,
			f.Details[observability.AttrChildInstanceID],
			"the re-link must reuse the recorded child")
	}

	_ = th2
}

// callStarts returns the recorded KindCall/Started facts.
func (fw *factWatch) callStarts() []observability.Fact {
	fw.mu.Lock()
	defer fw.mu.Unlock()

	out := []observability.Fact{}

	for _, f := range fw.facts {
		if f.Kind == observability.KindCall &&
			f.Phase == observability.PhaseStarted {
			out = append(out, f)
		}
	}

	return out
}

// TestCallRefusalsOnMissingCounterpart is T-8's loud half: a deleted
// child record fails the parent's restore; a deleted parent record
// fails the child's — the engine starts either way.
func TestCallRefusalsOnMissingCounterpart(t *testing.T) {
	t.Run("the child record is gone", func(t *testing.T) {
		repo := memrepo.New()

		var gate atomic.Int32

		callee := parkingCallee(t, "cr-nochild-ee", &gate)
		parent := callerOf(t, "cr-nochild-er", callee)

		parentID, childID, cancel1 := parkedCall(t, repo, parent, callee)
		defer cancel1()

		require.NoError(t,
			repo.Delete(context.Background(), childID))

		time.Sleep(120 * time.Millisecond)

		_, fw2, cancel2 := bootCallEngine(t, "engine-2", repo,
			time.Minute, parent, callee)
		defer cancel2()

		require.Eventually(t, func() bool {
			return fw2.saw(observability.KindInstanceState,
				observability.PhaseFailed)
		}, 3*time.Second, 10*time.Millisecond,
			"the parent's restore must fail loud on the missing child")

		rec, ok, _ := repo.Load(context.Background(), parentID)
		require.True(t, ok)
		require.NotEqual(t, repository.StatusCompleted, rec.Status,
			"the parent must not complete over a lost child")
	})

	t.Run("the parent record is gone", func(t *testing.T) {
		repo := memrepo.New()

		var gate atomic.Int32

		callee := parkingCallee(t, "cr-orphan-ee", &gate)
		parent := callerOf(t, "cr-orphan-er", callee)

		_, childID, cancel1 := parkedCall(t, repo, parent, callee)
		defer cancel1()

		parentRec := func() string {
			rec, _, _ := repo.Load(context.Background(), childID)
			doc, err := checkpoint.Unmarshal(rec.Payload)
			require.NoError(t, err)

			return doc.ParentID
		}()

		require.NoError(t,
			repo.Delete(context.Background(), parentRec))

		time.Sleep(120 * time.Millisecond)

		_, fw2, cancel2 := bootCallEngine(t, "engine-2", repo,
			time.Minute, parent, callee)
		defer cancel2()

		require.Eventually(t, func() bool {
			return fw2.saw(observability.KindInstanceState,
				observability.PhaseFailed)
		}, 3*time.Second, 10*time.Millisecond,
			"a child must never run orphaned")
	})
}

// TestCallChildCompletedWhileDown: the child's record turned terminal
// while the engine was down — the restored parent resumes through the
// already-settled handle, recovering nothing for the child.
func TestCallChildCompletedWhileDown(t *testing.T) {
	repo := memrepo.New()

	var gate atomic.Int32

	callee := parkingCallee(t, "cr-done-ee", &gate)
	parent := callerOf(t, "cr-done-er", callee)

	parentID, childID, cancel1 := parkedCall(t, repo, parent, callee)
	defer cancel1()

	// simulate "completed while down": flip the child's record terminal.
	ctx := context.Background()
	crec, ok, err := repo.Load(ctx, childID)
	require.NoError(t, err)
	require.True(t, ok)

	crec.Status = repository.StatusCompleted
	require.NoError(t, repo.Save(ctx, crec))

	time.Sleep(120 * time.Millisecond)

	_, fw2, cancel2 := bootCallEngine(t, "engine-2", repo,
		time.Minute, parent, callee)
	defer cancel2()

	require.Eventually(t, func() bool {
		rec, ok, _ := repo.Load(ctx, parentID)

		return ok && rec.Status == repository.StatusCompleted
	}, 3*time.Second, 10*time.Millisecond,
		"the parent must resume off the terminal record at once")

	require.False(t, fw2.saw(observability.KindInstanceState,
		observability.PhaseFailed))
}

// TestCallDiscoverySeparation is the review-requested separation: a
// host lists ROOTS as its processes; children come through their
// parent (SRD-082 FR-7).
func TestCallDiscoverySeparation(t *testing.T) {
	repo := memrepo.New()

	var gate atomic.Int32

	callee := parkingCallee(t, "cr-disc-ee", &gate)
	parent := callerOf(t, "cr-disc-er", callee)

	th, _, cancel := bootCallEngine(t, "engine-disc", repo,
		time.Minute, parent, callee)
	defer cancel()

	h, err := th.StartLatest(parent.ID())
	require.NoError(t, err)

	parentID := h.ID()

	var childID string

	require.Eventually(t, func() bool {
		children := th.Instances(thresher.InstancesChildren)
		if len(children) != 1 {
			return false
		}

		childID = children[0]

		return true
	}, 3*time.Second, 10*time.Millisecond)

	require.Equal(t, []string{parentID},
		th.Instances(thresher.InstancesRoots),
		"roots list the caller only")

	ch, ok := th.Instance(childID)
	require.True(t, ok)
	require.Equal(t, parentID, ch.ParentID(),
		"the child's handle names its caller")
	require.NotEmpty(t, ch.CallNodeID())

	ph, ok := th.Instance(parentID)
	require.True(t, ok)
	require.Empty(t, ph.ParentID(), "a root has no parent linkage")
}

// TestCallChildTerminatedWhileDown: a child whose record turned
// Terminated while the engine was down faults the restored caller —
// the terminal record's outcome maps onto the call, exactly as a live
// termination would.
func TestCallChildTerminatedWhileDown(t *testing.T) {
	repo := memrepo.New()

	var gate atomic.Int32

	callee := parkingCallee(t, "cr-term-ee", &gate)
	parent := callerOf(t, "cr-term-er", callee)

	parentID, childID, cancel1 := parkedCall(t, repo, parent, callee)
	defer cancel1()

	ctx := context.Background()
	crec, ok, err := repo.Load(ctx, childID)
	require.NoError(t, err)
	require.True(t, ok)

	crec.Status = repository.StatusTerminated
	require.NoError(t, repo.Save(ctx, crec))

	time.Sleep(120 * time.Millisecond)

	_, fw2, cancel2 := bootCallEngine(t, "engine-2", repo,
		time.Minute, parent, callee)
	defer cancel2()

	// the fault reaches the caller: the parent never completes and the
	// failure is operator-visible.
	require.Eventually(t, func() bool {
		return fw2.saw(observability.KindCall, observability.PhaseFailed)
	}, 3*time.Second, 10*time.Millisecond,
		"the terminal record's failure must map onto the call")

	rec, ok, _ := repo.Load(ctx, parentID)
	require.True(t, ok)
	require.NotEqual(t, repository.StatusCompleted, rec.Status)
}

// TestCompositeCallKillAndResume is T-10 — the SRD-082 §3 worked trace
// end to end: a Call Activity INSIDE a sequential Multi-Instance,
// killed mid-pass-2 while the pass's body waits on the called child.
// Recovery seeds the iteration at the recorded pass, re-links the
// call, the child completes, the remaining passes run — and nothing
// completed ever re-executes.
func TestCompositeCallKillAndResume(t *testing.T) {
	repo := memrepo.New()

	// permits gate the CALLEE per instance: each arming consumes one,
	// so pass 0's child completes and pass 1's parks — the kill point.
	var permits atomic.Int32

	permits.Store(1)

	permitCond := goexpr.Must(nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(_ context.Context, _ data.Source) (data.Value, error) {
			if permits.Load() > 0 {
				permits.Add(-1)

				return values.NewVariable(true), nil
			}

			return values.NewVariable(false), nil
		})

	callee := parkingCalleeWith(t, "cr-mix-ee", permitCond)

	// the parent: start → miBody(sequential MI ×3 over a Call
	// Activity) → end. The body sub-process holds the call.
	p, err := process.New("caller", foundation.WithID("cr-mix-er"))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	mi, err := activities.NewMultiInstance(
		activities.WithSequential(),
		activities.WithCardinality(mixCard(t, 3)))
	require.NoError(t, err)

	body, err := activities.NewSubProcess("body", activities.WithLoop(mi))
	require.NoError(t, err)

	bStart, err := events.NewStartEvent("b-start")
	require.NoError(t, err)

	ca, err := activities.NewCallActivity("invoke", callee.ID())
	require.NoError(t, err)

	bEnd, err := events.NewEndEvent("b-end")
	require.NoError(t, err)

	for _, e := range []flow.Element{bStart, ca, bEnd} {
		require.NoError(t, body.Add(e))
	}

	link(t, bStart, ca)
	link(t, ca, bEnd)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, body, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, body)
	link(t, body, end)

	th1, _, cancel1 := bootCallEngine(t, "engine-1", repo,
		80*time.Millisecond, p, callee)
	defer cancel1()

	h, err := th1.StartLatest(p.ID())
	require.NoError(t, err)

	parentID := h.ID()

	var childID string

	require.Eventually(t, func() bool {
		rec, ok, _ := repo.Load(context.Background(), parentID)
		if !ok {
			return false
		}

		doc, err := checkpoint.Unmarshal(rec.Payload)
		if err != nil || len(doc.Calls) != 1 {
			return false
		}

		// the recorded position: pass 1 in flight, pass 0 done.
		for _, tr := range doc.Tracks {
			if tr.MI != nil && tr.MI.Completed == 1 {
				childID = doc.Calls[0].ChildID

				return true
			}
		}

		return false
	}, 3*time.Second, 10*time.Millisecond,
		"the kill point: MI at pass 1, its call in flight")

	// how many child instances did engine-1 launch so far? Exactly 2
	// (pass 0's, completed; pass 1's, parked).
	time.Sleep(120 * time.Millisecond) // the lease lapses
	permits.Store(10)                  // every remaining child flows through

	th2, fw2, cancel2 := bootCallEngine(t, "engine-2", repo,
		time.Minute, p, callee)
	defer cancel2()

	require.Eventually(t, func() bool {
		rec, ok, _ := repo.Load(context.Background(), parentID)

		return ok && rec.Status == repository.StatusCompleted &&
			rec.Lease.Owner == "engine-2"
	}, 5*time.Second, 10*time.Millisecond,
		"the composite must run to completion on engine-2")

	// pass 1's recorded child was RE-LINKED, not re-invoked: engine-2's
	// first call-start names the recorded id; exactly one more child
	// (pass 2's) was invoked fresh.
	starts := fw2.callStarts()
	require.NotEmpty(t, starts)
	require.Equal(t, childID,
		starts[0].Details[observability.AttrChildInstanceID],
		"the re-link reuses pass 1's recorded child")
	require.Len(t, starts, 2,
		"one re-link + one fresh pass-2 invoke — pass 0 never re-runs")

	_ = th2
}

// mixCard builds an integer cardinality expression for the T-10 mix.
func mixCard(t *testing.T, n int) data.FormalExpression {
	t.Helper()

	return goexpr.Must(nil,
		data.MustItemDefinition(values.NewVariable(0)),
		func(_ context.Context, _ data.Source) (data.Value, error) {
			return values.NewVariable(n), nil
		})
}
