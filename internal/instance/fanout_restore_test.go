package instance

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// fanOutFixture is a PARALLEL Multi-Instance composite host and the loop
// state to drive it: the one shape whose instances take scopes of their
// own, and so the only one the restore derivation below reads as such.
func fanOutFixture(
	t *testing.T,
) (*Instance, *loopState, flow.Node, *track) {
	t.Helper()

	inst, node, host := miParFixture(t)

	return inst, newLoopState(inst), node, host
}

// instancePath is the scope one fanned-out instance of node opens under its
// host — the path shape the whole restore derivation turns on.
func instancePath(
	t *testing.T, host *track, seg string,
) scope.DataPath {
	t.Helper()

	p, err := host.scopePath.Append(seg)
	require.NoError(t, err)

	return p
}

// TestRestoredScopeHostReadsAnInstanceOrdinal pins what makes a fanned-out
// composite restorable WITHOUT an open-set record (SRD-090.A M3b): the
// instance's ordinal is in its own scope segment, so the host derivation
// that already finds a serial pass's scope finds an instance's too, and
// says which one it is.
func TestRestoredScopeHostReadsAnInstanceOrdinal(t *testing.T) {
	_, _, node, host := fanOutFixture(t)

	path := instancePath(t, host, scopeSegment(node)+"-2")

	got, gotNode, ord := restoredScopeHost(
		[]*track{host}, host.scopePath, path)

	require.Same(t, host, got)
	require.Equal(t, node.ID(), gotNode.ID())
	require.Equal(t, 2, ord, "the segment names instance 2")
}

// TestRestoredHostSegmentSkipsANonHost: a scope is named only by a track
// that both sits in the parent scope and executes a composite. A leaf
// activity's track opens no scope at all, and a composite in some other
// scope names one somewhere else — neither can own the path being adopted.
func TestRestoredHostSegmentSkipsANonHost(t *testing.T) {
	inst, _, node, host := fanOutFixture(t)

	leaf := &track{
		steps:     []*stepInfo{{node: findNode(t, inst.s, "start")}},
		scopePath: host.scopePath,
	}

	_, _, ok := restoredHostSegment(leaf, host.scopePath)
	require.False(t, ok, "a non-composite node's track names no scope")

	_, _, ok = restoredHostSegment(host, scope.DataPath("/elsewhere"))
	require.False(t, ok, "and a composite names one only where it stands")

	got, seg, ok := restoredHostSegment(host, host.scopePath)
	require.True(t, ok)
	require.Equal(t, node.ID(), got.ID())
	require.Equal(t, scopeSegment(node), seg)
}

// TestRestoredScopeHostPrefersAnOwnScope: `sp-body-1` is BOTH instance 1 of
// `body` and the own scope of a sibling whose segment reads that way, and
// only one of the two can be open — they are the same path. The own reading
// wins, and it wins whichever order the track table lists them in: a
// derivation that answered by table order would attach a restored scope to
// a different host run to run.
func TestRestoredScopeHostPrefersAnOwnScope(t *testing.T) {
	_, _, node, host := fanOutFixture(t)

	seg := scopeSegment(node) + "-1"
	path := instancePath(t, host, seg)

	// a sibling composite whose own segment is spelled like an instance —
	// the override is how a track names a scope the node's id would not.
	twin := &track{
		steps:     []*stepInfo{{node: node}},
		scopePath: host.scopePath,
		scopeSeg:  seg,
	}

	for name, initial := range map[string][]*track{
		"the twin listed first": {twin, host},
		"the host listed first": {host, twin},
	} {
		t.Run(name, func(t *testing.T) {
			got, _, ord := restoredScopeHost(initial, host.scopePath, path)

			require.Same(t, twin, got, "the scope's OWNER, not its reader")
			require.Equal(t, -1, ord, "an own scope, not an instance")
		})
	}
}

// TestInstanceOrdinalOfRejects: everything that merely LOOKS like an
// instance segment is refused. The suffix has to read back as exactly the
// number it claims, or a scope belonging to something else would be adopted
// as an instance of this host — and then torn down with its fan-out.
func TestInstanceOrdinalOfRejects(t *testing.T) {
	_, _, node, host := fanOutFixture(t)

	seg := scopeSegment(node)

	for name, tail := range map[string]string{
		"the host's own scope":     seg,
		"another node's instance":  "sp-other-1",
		"a named suffix":           seg + "-retry",
		"a non-canonical number":   seg + "-01",
		"a signed number":          seg + "-+1",
		"a negative ordinal":       seg + "--1",
		"nothing after the dash":   seg + "-",
		"an instance's own child":  seg + "-1/sp-inner",
		"an instance's own second": seg + "-1/" + seg + "-2",
	} {
		t.Run(name, func(t *testing.T) {
			_, ok := instanceOrdinalOf(host, seg, instancePath(t, host, tail))
			require.False(t, ok)
		})
	}

	// a host whose own path will not take a segment answers no rather
	// than deriving an ordinal from a path it cannot have produced.
	_, ok := instanceOrdinalOf(
		&track{scopePath: scope.EmptyDataPath}, seg, instancePath(t, host, seg+"-1"))
	require.False(t, ok)
}

// TestFanOutPostsItsPositionBeforeItStarts pins the order that keeps a
// fan-out restorable: the executor set is posted BEFORE the first instance
// runs, so the window where a checkpoint would find no set at all — and
// restore it as "all N still to run" — does not exist (SRD-090.A FR-6).
//
// A post that fails takes the activation with it, rather than fanning out
// into a position nothing recorded.
func TestFanOutPostsItsPositionBeforeItStarts(t *testing.T) {
	inst, node, host := miParFixture(t)

	got := make(chan scopeRequest, 1)

	go func() {
		req := <-inst.scopeReq
		got <- req
		req.reply <- scopeReply{err: errors.New("the loop refuses")}
	}()

	d := newIterDecorator(host, &stepInfo{node: node},
		multiInstanceOf(node), true)

	_, err := d.run(t.Context())
	require.ErrorContains(t, err, "the loop refuses")

	req := <-got
	require.Equal(t, scopeIterPost, req.op,
		"the FIRST thing a fan-out asks of the loop is to record its set")
	require.Equal(t, 0, req.completed, "nothing has completed yet")
	require.Len(t, req.insts, 3, "and all three instances are named")
}

// TestAdoptRestoredScopesRebuildsAFanOut: the loop rebuilds an entry per
// still-open instance scope from the scope table alone — host, ordinal, and
// the hold that keeps its drain waiting for the executor that has not been
// launched yet (SRD-090.A M3b, retiring MIGroupRecord.Open).
func TestAdoptRestoredScopesRebuildsAFanOut(t *testing.T) {
	inst, ls, node, host := fanOutFixture(t)

	paths := map[int]scope.DataPath{}

	for _, ord := range []int{0, 3} {
		p := instancePath(t, host, scopeSegment(node)+"-"+itoa(ord))
		require.NoError(t, inst.sc.plane.OpenScope(p))

		paths[ord] = p
	}

	require.NoError(t, ls.adoptRestoredScopes([]*track{host}))

	for ord, p := range paths {
		entry, ok := ls.scopes[p]
		require.True(t, ok, "instance %d's scope was not adopted", ord)
		require.Same(t, host, entry.host)
		require.True(t, entry.instance)
		require.Equal(t, ord, entry.ordinal)
		require.True(t, entry.awaitAttach,
			"its drain waits for the executor that resumes it")
	}

	require.Contains(t, ls.waiting, host.ID())
}

// TestAdoptRestoredScopesRefusesACompletedInstanceStillOpen: a document
// whose scope table and executor set describe different moments is refused
// rather than half-restored. Nothing would ever re-attach to that scope —
// the decorator does not relaunch a completed ordinal — so it would stay
// open for the life of the instance.
func TestAdoptRestoredScopesRefusesACompletedInstanceStillOpen(t *testing.T) {
	inst, ls, node, host := fanOutFixture(t)

	p := instancePath(t, host, scopeSegment(node)+"-1")
	require.NoError(t, inst.sc.plane.OpenScope(p))

	host.iterSeed = &checkpoint.IterationRecord{
		Kind: iterKindMIParallel, N: 2,
		Instances: []checkpoint.IterationInstance{
			{Ordinal: 0, State: instanceRunning},
			{Ordinal: 1, State: instanceCompleted},
		},
	}

	err := ls.adoptRestoredScopes([]*track{host})
	require.ErrorContains(t, err, "scope table and executor set disagree")
}

// TestInstanceRecordedDone reads the restored set the refusal above rests
// on: only an ordinal the set NAMES as completed counts, and a document
// carrying no set at all (a pre-Schema-6 capture, whose fan-out rides the
// group record) answers no rather than guessing.
func TestInstanceRecordedDone(t *testing.T) {
	_, _, _, host := fanOutFixture(t)

	require.False(t, instanceRecordedDone(host, 0), "no recorded set")

	host.iterSeed = &checkpoint.IterationRecord{
		Instances: []checkpoint.IterationInstance{
			{Ordinal: 1, State: instanceCompleted},
			{Ordinal: 2, State: instanceRunning},
		},
	}

	require.True(t, instanceRecordedDone(host, 1))
	require.False(t, instanceRecordedDone(host, 2))
	require.False(t, instanceRecordedDone(host, 7), "not in the set")
}

// TestReAttachAdoptsTheInstancesCell: a restored instance scope was rebuilt
// by the loop and carries neither a drain channel nor an output cell — both
// belong to the executor that resumes it, and both are adopted on the
// re-attach. Without the cell the resumed instance's output would be read
// from nowhere and its slot would stay nil, which reads downstream as an
// instance that produced nothing.
func TestReAttachAdoptsTheInstancesCell(t *testing.T) {
	_, ls, node, host := fanOutFixture(t)

	seg := scopeSegment(node) + "-2"
	child := instancePath(t, host, seg)
	ls.scopes[child] = &scopeEntry{
		host: host, node: node, parent: host.scopePath,
		awaitAttach: true, instance: true, ordinal: 2,
	}

	var (
		drain = make(chan struct{})
		cell  = &instanceCapture{item: "result"}
		reply = make(chan scopeReply, 1)
	)

	ls.handleScopeOpen(t.Context(), scopeRequest{
		op: scopeOpen, host: host, node: node, segment: seg, ordinal: 2,
		drain: drain, capture: cell, reply: reply,
	})

	r := <-reply
	require.NoError(t, r.err)
	require.Equal(t, child, r.scopePath, "the SAME scope, not a second one")

	entry := ls.scopes[child]
	require.Same(t, cell, entry.capture)
	require.Equal(t, (<-chan struct{})(drain), (<-chan struct{})(entry.drain))
	require.False(t, entry.awaitAttach, "the hold is lifted")
}

// TestMarkIterDrainSkipsAFannedOutInstance: an instance's drain must not
// advance the iteration mirror. The host's loopCounter stands still for the
// whole fan-out, so deriving the position from it would overwrite the
// decorator's posted set with a zero (SRD-090.A M3b).
//
// **T-1 finding, SRD-090.A M3c.** The entries below now set `iterating`,
// which handleScopeOpen copies from the request: the drain accounting reads
// what the scope's OPENER declared instead of asking the node whether it
// iterates (FR-11). A hand-built entry has to say so, and one that does not
// is correctly ignored — which is what the third case pins.
func TestMarkIterDrainSkipsAFannedOutInstance(t *testing.T) {
	_, ls, node, host := fanOutFixture(t)

	m := ls.ensureIterMirror(host, iterKindMIParallel)
	m.completed = 3

	ls.markIterDrain(&scopeEntry{
		host: host, node: node, iterating: true, instance: true, ordinal: 1})
	require.Equal(t, 3, m.completed, "the posted set stands")

	// the SERIAL pass of the same node still advances it: one open scope,
	// one pass, and the host's counter is the position.
	ls.markIterDrain(&scopeEntry{host: host, node: node, iterating: true})
	require.Equal(t, host.loopCounterSnap()+1, m.completed)

	// a scope whose opener did NOT iterate never advances a mirror, whatever
	// the node's own characteristics say — the opener is the authority.
	m.completed = 3
	ls.markIterDrain(&scopeEntry{host: host, node: node})
	require.Equal(t, 3, m.completed, "a plain open touches no mirror")
}

// TestScopeFactOrdinal pins the ordinal a scope's Completed / Canceled fact
// carries — the same question the Opened fact answers, asked in one place
// so the three cannot drift (SRD-056.A FR-14).
func TestScopeFactOrdinal(t *testing.T) {
	_, _, node, host := fanOutFixture(t)

	host.setLoopCounter(4)

	require.Equal(t, 2, scopeFactOrdinal(
		&scopeEntry{host: host, node: node, instance: true, ordinal: 2}),
		"a fanned-out instance reports its OWN ordinal")

	require.Equal(t, 4, scopeFactOrdinal(&scopeEntry{host: host, node: node}),
		"a serial pass reports the host's pass counter")
}

// TestRecordedScopeHostResolvesWhatTheDerivationCannot is the point of the
// Schema-7 scope record (SRD-090.A M3c). `sp-body-1` reads two ways — the
// own scope of a track whose segment is spelled that way, and instance 1 of
// a fanning-out host — and the derivation cannot tell which one opened it,
// so it applies a precedence rule and always answers "own scope".
//
// That rule is a coin-flip dressed as a decision: when the instance IS the
// opener, the derivation attaches the scope to the wrong host, and the
// instance's drain is delivered to a track that never fanned out. The
// record says who opened it, so the lookup answers correctly on exactly the
// input the derivation gets wrong.
func TestRecordedScopeHostResolvesWhatTheDerivationCannot(t *testing.T) {
	inst, ls, node, host := fanOutFixture(t)

	seg := scopeSegment(node) + "-1"
	path := instancePath(t, host, seg)

	// the sibling that makes the path ambiguous: its OWN scope is spelled
	// exactly like the host's instance 1.
	twin := &track{
		steps:     []*stepInfo{{node: node}},
		scopePath: host.scopePath,
		scopeSeg:  seg,
	}

	initial := []*track{twin, host}

	// what the derivation answers, unchanged — the own reading wins.
	derived, _, derivedOrd := restoredScopeHost(initial, host.scopePath, path)
	require.Same(t, twin, derived)
	require.Equal(t, -1, derivedOrd)

	// what the record answers: the host actually opened it, as instance 1.
	inst.restoredScopes = []checkpoint.ScopeRecord{
		{Path: string(path), HostTrack: host.ID(), Ordinal: 1},
	}

	got, gotNode, ord := ls.recordedScopeHost(initial, path)

	require.Same(t, host, got, "the recorded opener, not the likelier one")
	require.Equal(t, node.ID(), gotNode.ID())
	require.Equal(t, 1, ord, "and the ordinal it recorded, not one re-read")
}

// TestRecordedScopeHostFallsBack: every way the record can fail to answer
// sends the caller to the derivation instead of failing the restore. A
// Schema ≤ 6 document carries no host at all, and that is the ONLY expected
// case — the other three are documents whose tables disagree, where letting
// the derivation speak keeps one error message for both routes.
func TestRecordedScopeHostFallsBack(t *testing.T) {
	inst, ls, node, host := fanOutFixture(t)

	path := instancePath(t, host, scopeSegment(node)+"-2")
	initial := []*track{host}

	// a real identity, not the zero one: a bare &track{} has an empty ID,
	// which the "no host recorded" branch swallows before the disagreement
	// branch is ever reached — the subtest below would pass for the wrong
	// reason and leave that branch untested.
	leaf := &track{
		BaseElement: *foundation.MustBaseElement(),
		steps:       []*stepInfo{{node: findNode(t, inst.s, "start")}},
		scopePath:   host.scopePath,
	}

	for name, recs := range map[string][]checkpoint.ScopeRecord{
		"a Schema 6 document names no host": {
			{Path: string(path)},
		},
		"no record for this path at all": {
			{Path: "/somewhere/else", HostTrack: host.ID()},
		},
		"the recorded host is not in the track table": {
			{Path: string(path), HostTrack: "gone", Ordinal: 2},
		},
	} {
		t.Run(name, func(t *testing.T) {
			inst.restoredScopes = recs

			got, _, _ := ls.recordedScopeHost(initial, path)
			require.Nil(t, got, "no answer, so the derivation runs")
		})
	}

	t.Run("the recorded host is no longer on a composite", func(t *testing.T) {
		inst.restoredScopes = []checkpoint.ScopeRecord{
			{Path: string(path), HostTrack: leaf.ID(), Ordinal: 2},
		}

		got, _, _ := ls.recordedScopeHost([]*track{leaf}, path)
		require.Nil(t, got, "the two tables disagree — derivation speaks")
	})
}
