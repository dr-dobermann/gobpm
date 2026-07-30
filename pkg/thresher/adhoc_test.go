package thresher_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/adhoc"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// SRD-074 §3.5 — the public per-container control surface: a host discovers the
// enabled set and selects from it through the engine, exactly as it takes and
// completes a human task.

// offerRouter offers both activities once, then ends the container.
type offerRouter struct {
	mu   sync.Mutex
	done bool
}

func (r *offerRouter) Next(_ context.Context, _ adhoc.State) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.done {
		return nil, nil
	}

	r.done = true

	return []string{"review", "escalate"}, nil
}

// adHocProcess builds start -> triage(manual ad-hoc: review, escalate) -> end.
func adHocProcess(
	t *testing.T, id string, r adhoc.Router, ran *sync.Map,
) *process.Process {
	t.Helper()

	p, err := process.New(id)
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	triage, err := activities.NewSubProcess("triage",
		activities.WithAdHoc(r), activities.WithAdHocManualSelection())
	require.NoError(t, err)

	for _, name := range []string{"review", "escalate"} {
		op, oerr := gooper.New(name,
			func(_ context.Context, _ service.DataReader,
				_ *data.ItemDefinition) (*data.ItemDefinition, error) {
				ran.Store(name, true)

				return nil, nil
			})
		require.NoError(t, oerr)

		task, terr := activities.NewServiceTask(name, op,
			activities.WithoutParams())
		require.NoError(t, terr)
		require.NoError(t, triage.Add(task))
	}

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, triage, end} {
		require.NoError(t, p.Add(e))
	}

	_, err = flow.Link(start, triage)
	require.NoError(t, err)
	_, err = flow.Link(triage, end)
	require.NoError(t, err)

	return p
}

func TestAdHocHandleEnabledAndActivate(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var ran sync.Map

	proc := adHocProcess(t, "adhoc-e2e", &offerRouter{}, &ran)

	th, err := thresher.New("test-adhoc")
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, th.Run(ctx))

	_, err = th.RegisterProcess(proc)
	require.NoError(t, err)

	h, err := th.StartLatest(proc.ID())
	require.NoError(t, err)

	// The container node id is the model's, stable across the snapshot clone.
	var nodeID string

	for _, n := range proc.Nodes() {
		if sp, ok := n.(*activities.SubProcess); ok && sp.IsAdHoc() {
			nodeID = sp.ID()
		}
	}

	require.NotEmpty(t, nodeID)

	ah, err := h.AdHoc(nodeID)
	require.NoError(t, err)

	// The container offers both activities and runs neither until asked.
	var enabled []string

	require.Eventually(t, func() bool {
		e, verr := ah.Enabled(ctx)
		if verr != nil {
			return false
		}

		enabled = e

		return len(e) == 2
	}, 5*time.Second, 10*time.Millisecond, "the container never offered")

	_, reviewed := ran.Load("review")
	_, escalated := ran.Load("escalate")
	require.False(t, reviewed || escalated,
		"nothing runs before the host selects")

	running, err := ah.Running(ctx)
	require.NoError(t, err)
	require.Empty(t, running,
		"an offer is not work in flight — the two views are distinct")

	require.NoError(t, ah.Activate(ctx, enabled[0]))

	require.Eventually(t, func() bool {
		return h.State() == thresher.StateCompleted
	}, 5*time.Second, 10*time.Millisecond)
}

func TestAdHocHandleRejections(t *testing.T) {
	th, err := thresher.New("test-adhoc-rej")
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, th.Run(ctx))

	require.NoError(t, data.CreateDefaultStates())

	var ran sync.Map

	proc := adHocProcess(t, "adhoc-rej", &offerRouter{}, &ran)
	_, err = th.RegisterProcess(proc)
	require.NoError(t, err)

	h, err := th.StartLatest(proc.ID())
	require.NoError(t, err)

	_, err = h.AdHoc("")
	require.Error(t, err, "an unnamed container is rejected at the handle")

	var ae *errs.ApplicationError

	require.ErrorAs(t, err, &ae)
	require.True(t, ae.HasClass(errs.EmptyNotAllowed))

	ah, err := h.AdHoc("no-such-container")
	require.NoError(t, err, "the handle is minted before the container opens")

	_, err = ah.Enabled(ctx)
	require.Error(t, err, "an unknown container reports rather than empty")

	require.Error(t, ah.Activate(ctx, "review"))
}
