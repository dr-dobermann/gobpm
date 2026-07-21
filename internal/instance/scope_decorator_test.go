package instance

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// decoratorFixture builds a looped-composite instance and returns it with a fresh
// loop state, the composite body node, and a host track positioned on it — the
// white-box pieces the scope-protocol tests drive directly (the loop is NOT
// running, so state is inspected on the test goroutine).
func decoratorFixture(t *testing.T) (*Instance, *loopState, flow.Node, *track) {
	t.Helper()

	var count atomic.Int32

	sl, err := activities.NewStandardLoop(loopCondLt(t, 3))
	require.NoError(t, err)

	inst := loopedSubProcessInstance(t, &count, sl)
	inst.tracks = map[string]*track{}
	ls := newLoopState(inst)
	node := findNode(t, inst.s, "body")

	host, err := newTrack(node, inst, nil)
	require.NoError(t, err)

	return inst, ls, node, host
}

// TestScopeRoundtripDelivers: scopeRoundtrip sends the request into the loop and
// returns the reply's path (the happy path — a stand-in loop reads scopeReq and
// answers).
func TestScopeRoundtripDelivers(t *testing.T) {
	inst, _, node, host := decoratorFixture(t)

	want := scope.DataPath("/std-loop-sp/sp-body")
	go func() {
		req := <-inst.scopeReq
		req.reply <- scopeReply{scopePath: want}
	}()

	got, err := inst.scopeRoundtrip(t.Context(),
		scopeRequest{host: host, node: node})
	require.NoError(t, err)
	require.Equal(t, want, got)
}

// TestScopeRoundtripSendUnblocks: while the request is still being handed to the
// loop, a shutdown or a cancelled context unblocks the decorator (NFR-4) — no
// hang if the loop is gone.
func TestScopeRoundtripSendUnblocks(t *testing.T) {
	t.Run("instance stopped", func(t *testing.T) {
		inst, _, node, host := decoratorFixture(t)
		close(inst.loopDone) // no reader on scopeReq → the send loses to loopDone

		_, err := inst.scopeRoundtrip(t.Context(),
			scopeRequest{host: host, node: node})
		require.Error(t, err)
	})

	t.Run("context cancelled", func(t *testing.T) {
		inst, _, node, host := decoratorFixture(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := inst.scopeRoundtrip(ctx, scopeRequest{host: host, node: node})
		require.ErrorIs(t, err, context.Canceled)
	})
}

// TestScopeRoundtripReplyUnblocks: after the loop accepts the request but before it
// replies, a shutdown or a cancelled context unblocks the decorator (NFR-4).
func TestScopeRoundtripReplyUnblocks(t *testing.T) {
	t.Run("instance stopped", func(t *testing.T) {
		inst, _, node, host := decoratorFixture(t)
		go func() { <-inst.scopeReq; close(inst.loopDone) }() // accept, never reply

		_, err := inst.scopeRoundtrip(t.Context(),
			scopeRequest{host: host, node: node})
		require.Error(t, err)
	})

	t.Run("context cancelled", func(t *testing.T) {
		inst, _, node, host := decoratorFixture(t)
		ctx, cancel := context.WithCancel(context.Background())
		go func() { <-inst.scopeReq; cancel() }() // accept, never reply

		_, err := inst.scopeRoundtrip(ctx, scopeRequest{host: host, node: node})
		require.ErrorIs(t, err, context.Canceled)
	})
}

// TestHandleScopeRequestOpens: the loop-side handler opens the child scope, records
// the entry, parks the host for drain, and replies with the opened path (FR-8a).
// The pass ordinal is bound by the decorator off the loop (runCompositeLoop), not
// here. loopDone is closed first so the seeded inner tracks exit on emit rather
// than blocking (no running loop reads inst.events).
func TestHandleScopeRequestOpens(t *testing.T) {
	inst, ls, node, host := decoratorFixture(t)
	close(inst.loopDone)

	reply := make(chan scopeReply, 1)
	ls.handleScopeRequest(t.Context(),
		scopeRequest{host: host, node: node, reply: reply})

	r := <-reply
	require.NoError(t, r.err)

	child, err := host.scopePath.Append(scopeSegment(node))
	require.NoError(t, err)
	require.Equal(t, child, r.scopePath)
	require.Contains(t, ls.scopes, child, "the entry is registered")
	require.Contains(t, ls.waiting, host.ID(), "the host is parked for drain")
}

// TestRunCompositeLoopBindError: a loopCounter bind failure at an unopened host
// scope faults out of the decorator's first pass (defensive, §4.6).
func TestRunCompositeLoopBindError(t *testing.T) {
	_, _, node, host := decoratorFixture(t)
	host.scopePath = scope.DataPath("/nope") // unopened → the bind Commit fails

	_, err := host.runCompositeLoop(
		t.Context(), &stepInfo{node: node}, standardLoopOf(node))
	require.Error(t, err)
}

// TestRunCompositeLoopRequestError: a scope-open request on a stopped instance
// faults out (the roundtrip's loopDone path). The default post-tested loop skips
// the pass-0 condition test, so the first thing that fails is the open request.
func TestRunCompositeLoopRequestError(t *testing.T) {
	inst, _, node, host := decoratorFixture(t)
	close(inst.loopDone) // scopeRoundtrip returns the not-running error

	_, err := host.runCompositeLoop(
		t.Context(), &stepInfo{node: node}, standardLoopOf(node))
	require.Error(t, err)
}

// TestAwaitScopeDrainedUnblocks: the decorator's per-pass drain wait unblocks on a
// cancelled context and on the loop closing evtCh (a mid-pass interrupt / terminate),
// so runCompositeLoop returns instead of hanging (NFR-4).
func TestAwaitScopeDrainedUnblocks(t *testing.T) {
	t.Run("context cancelled", func(t *testing.T) {
		_, _, _, host := decoratorFixture(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		require.ErrorIs(t, host.awaitScopeDrained(ctx), context.Canceled)
	})

	t.Run("evtCh closed on stop", func(t *testing.T) {
		_, _, _, host := decoratorFixture(t)
		close(host.evtCh) // the loop closes evtCh on stop

		require.Error(t, host.awaitScopeDrained(context.Background()))
	})
}

// TestHandleScopeRequestNonComposite: a scope-open for a node that is not a
// composite is a corrupt-graph error surfaced to the decorator.
func TestHandleScopeRequestNonComposite(t *testing.T) {
	inst, ls, _, host := decoratorFixture(t)
	leaf := findNode(t, inst.s, "start") // a StartEvent is not a scopeHost

	reply := make(chan scopeReply, 1)
	ls.handleScopeRequest(t.Context(),
		scopeRequest{host: host, node: leaf, reply: reply})

	require.Error(t, (<-reply).err)
}

// TestHandleScopeRequestOpenScopeError: a data-plane open failure (the child path
// already open) is surfaced to the decorator, before any seed or arm.
func TestHandleScopeRequestOpenScopeError(t *testing.T) {
	inst, ls, node, host := decoratorFixture(t)

	child, err := host.scopePath.Append(scopeSegment(node))
	require.NoError(t, err)
	require.NoError(t, inst.sc.plane.OpenScope(child)) // pre-open → duplicate fails

	reply := make(chan scopeReply, 1)
	ls.handleScopeRequest(t.Context(),
		scopeRequest{host: host, node: node, reply: reply})

	require.Error(t, (<-reply).err)
}

// TestHandleScopeRequestAppendError: a host whose scope path is malformed fails at
// the child-path derivation, surfaced to the decorator before any mutation.
func TestHandleScopeRequestAppendError(t *testing.T) {
	_, ls, node, host := decoratorFixture(t)
	host.scopePath = scope.DataPath("no-leading-slash") // Validate rejects → Append fails

	reply := make(chan scopeReply, 1)
	ls.handleScopeRequest(t.Context(),
		scopeRequest{host: host, node: node, reply: reply})

	require.Error(t, (<-reply).err)
}

// TestHandleScopeRequestBindCounterError: binding loopCounter at a host scope that
// is not open fails (the bind precedes OpenScope), surfaced to the decorator.
func TestHandleScopeRequestBindCounterError(t *testing.T) {
	_, ls, node, host := decoratorFixture(t)
	host.scopePath = scope.DataPath("/does/not/exist") // well-formed but not open

	reply := make(chan scopeReply, 1)
	ls.handleScopeRequest(t.Context(),
		scopeRequest{host: host, node: node, reply: reply})

	require.Error(t, (<-reply).err)
}
