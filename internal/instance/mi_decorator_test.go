package instance

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// getAtErrColl is a broken collection whose per-instance read fails — a real array
// for Count()/type, with GetAt overridden to error. It exercises the decorator's
// input-split fail-fast guard (a Collection whose Count lies / GetAt errors).
type getAtErrColl struct {
	*values.Array[any]
}

func (getAtErrColl) GetAt(context.Context, any) (any, error) {
	return nil, errors.New("getat boom")
}

// miSeqFixture builds a sequential-MI composite instance (cardinality 3) and
// returns the host track positioned on the MI node — the white-box pieces the
// runMISequential error-path tests drive directly (the loop is NOT running).
func miSeqFixture(t *testing.T) (*Instance, flow.Node, *track) {
	t.Helper()

	var count atomic.Int32

	mi := mustSeqMI(t, activities.WithCardinality(cardExpr(t, 3)))
	inst := miSubProcessInstance(t, &count, mi)
	inst.tracks = map[string]*track{}
	node := findNode(t, inst.s, "body")

	host, err := newTrack(node, inst, nil)
	require.NoError(t, err)

	return inst, node, host
}

// TestRunMISequentialRequestError: a scope-open request on a stopped instance
// faults out of the decorator (the roundtrip's loopDone path) — after the count is
// resolved and instance 0's data is split, the open request fails.
func TestRunMISequentialRequestError(t *testing.T) {
	inst, node, host := miSeqFixture(t)
	close(inst.loopDone) // scopeRoundtrip returns the not-running error

	_, err := host.runMISequential(
		t.Context(), &stepInfo{node: node}, multiInstanceOf(node))
	require.Error(t, err)
}

// TestRunMISequentialBindError: the per-instance input split fails when the input
// collection is broken (its GetAt errors) — the decorator's fail-fast guard fires
// before any scope opens. The broken collection is injected straight into the
// running scope (bypassing the snapshot's property clone).
func TestRunMISequentialBindError(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var count atomic.Int32

	mi := mustSeqMI(t, activities.WithInputCollection("items", "item"))
	inst := miSubProcessInstance(t, &count, mi)
	inst.tracks = map[string]*track{}
	node := findNode(t, inst.s, "body")

	host, err := newTrack(node, inst, nil)
	require.NoError(t, err)

	coll := getAtErrColl{values.NewArray[any](1, 2, 3)}
	require.NoError(t, inst.sc.bindValueAt(host.scopePath, "items", coll))

	_, err = host.runMISequential(
		t.Context(), &stepInfo{node: node}, multiInstanceOf(node))
	require.Error(t, err)
}

// TestRunMISequentialDrainError: a stand-in loop opens instance 0's scope then
// closes evtCh (a mid-instance stop) — the decorator's drain wait unblocks with an
// error rather than hanging.
func TestRunMISequentialDrainError(t *testing.T) {
	inst, node, host := miSeqFixture(t)

	go func() {
		req := <-inst.scopeReq
		req.reply <- scopeReply{scopePath: host.scopePath}
		close(host.evtCh) // the loop closes evtCh on stop
	}()

	_, err := host.runMISequential(
		t.Context(), &stepInfo{node: node}, multiInstanceOf(node))
	require.Error(t, err)
}
