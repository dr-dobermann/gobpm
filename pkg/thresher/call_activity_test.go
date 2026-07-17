package thresher_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

// ioParam builds a declared parameter named name (zero-valued — only the name
// is the call contract).
func ioParam(t *testing.T, name string) *data.Parameter {
	t.Helper()

	return data.MustParameter(name,
		data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable(0),
				foundation.WithID(name)),
			data.ReadyDataState))
}

// scaleCallee builds start → scale(reads "amount", writes "result" = amount*f)
// → end under the given stable key, so two versions of one key can differ.
func scaleCallee(t *testing.T, key string, f int) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New("callee", foundation.WithID(key))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	op, err := gooper.New("scale",
		func(ctx context.Context, ds service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			d, err := ds.GetData("amount")
			if err != nil {
				return nil, err
			}

			n, _ := d.Value().Get(ctx).(int)

			return data.MustItemDefinition(values.NewVariable(n*f),
				foundation.WithID("result")), nil
		})
	require.NoError(t, err)

	scale, err := activities.NewServiceTask("scale", op,
		activities.WithoutParams())
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, scale, end} {
		require.NoError(t, p.Add(e))
	}
	link(t, start, scale)
	link(t, scale, end)

	return p
}

// raisingCallee builds start → raise(BpmnError code) → end.
func raisingCallee(t *testing.T, key, code string) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New("callee", foundation.WithID(key))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	op, err := gooper.New("raise",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			return nil, &events.BpmnError{Code: code}
		})
	require.NoError(t, err)

	raise, err := activities.NewServiceTask("raise", op,
		activities.WithoutParams())
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, raise, end} {
		require.NoError(t, p.Add(e))
	}
	link(t, start, raise)
	link(t, raise, end)

	return p
}

// recordOp reads an int datum by name into saw.
func recordOp(t *testing.T, name string, saw *atomic.Int64) service.Operation {
	t.Helper()

	op, err := gooper.New("record-"+name,
		func(ctx context.Context, ds service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			d, err := ds.GetData(name)
			if err != nil {
				return nil, err
			}

			n, _ := d.Value().Get(ctx).(int)
			saw.Store(int64(n))

			return nil, nil
		})
	require.NoError(t, err)

	return op
}

// markSawOp sets saw to -1 — the exception-path marker.
func markSawOp(t *testing.T, saw *atomic.Int64) service.Operation {
	t.Helper()

	op, err := gooper.New("mark-saw",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			saw.Store(-1)

			return nil, nil
		})
	require.NoError(t, err)

	return op
}

// callerProcess builds start → callActivity(in "amount", out "result") → check
// → end, seeding "amount". version 0 = latest, else pinned.
func callerProcess(
	t *testing.T, callee string, amount, version int, saw *atomic.Int64,
) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New("caller",
		data.WithProperties(
			data.MustProperty("amount",
				data.MustItemDefinition(values.NewVariable(amount),
					foundation.WithID("amount")),
				data.ReadyDataState)))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	caOpts := []options.Option{
		activities.WithParameters(data.Input, ioParam(t, "amount")),
		activities.WithParameters(data.Output, ioParam(t, "result")),
	}
	if version > 0 {
		caOpts = append(caOpts, activities.WithCalledVersion(version))
	}

	ca, err := activities.NewCallActivity("invoke", callee, caOpts...)
	require.NoError(t, err)

	check, err := activities.NewServiceTask("check",
		recordOp(t, "result", saw), activities.WithoutParams())
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, ca, check, end} {
		require.NoError(t, p.Add(e))
	}
	link(t, start, ca)
	link(t, ca, check)
	link(t, check, end)

	return p
}

// callerWithBoundary builds start → callActivity[Error boundary code] → check →
// end, with the boundary routing to mark → exc-end.
func callerWithBoundary(
	t *testing.T, callee, code string, saw *atomic.Int64,
) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New("caller-bnd")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	ca, err := activities.NewCallActivity("invoke", callee)
	require.NoError(t, err)

	check, err := activities.NewServiceTask("check",
		nopOp(t, "check-op", 0), activities.WithoutParams())
	require.NoError(t, err)

	bpErr, err := bpmncommon.NewError("call-err", code, nil)
	require.NoError(t, err)
	eed, err := events.NewErrorEventDefinition(bpErr)
	require.NoError(t, err)
	be, err := events.NewBoundaryEvent("err-bnd", ca, eed, true)
	require.NoError(t, err)

	mark, err := activities.NewServiceTask("mark",
		markSawOp(t, saw), activities.WithoutParams())
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)
	excEnd, err := events.NewEndEvent("exc-end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, ca, check, be, mark, excEnd, end} {
		require.NoError(t, p.Add(e))
	}
	link(t, start, ca)
	link(t, ca, check)
	link(t, check, end)
	link(t, be, mark)
	link(t, mark, excEnd)

	return p
}

// runCaller registers the callees + caller, runs the caller to a terminal
// state, and returns the observed state.
func runCaller(
	t *testing.T, caller *process.Process, callees ...*process.Process,
) (thresher.InstanceState, error) {
	t.Helper()

	th, err := thresher.New("call-e2e")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))

	for _, c := range callees {
		_, err = th.RegisterProcess(c)
		require.NoError(t, err)
	}
	_, err = th.RegisterProcess(caller)
	require.NoError(t, err)

	h, err := th.StartLatest(caller.ID())
	require.NoError(t, err)

	wctx, wcancel := context.WithTimeout(ctx, 5*time.Second)
	defer wcancel()
	st, werr := h.WaitCompletion(wctx)

	require.NoError(t, th.Shutdown(context.Background()))

	return st, werr
}

// TestCallActivityE2E (SRD-050 §6, M4): a caller invokes a registered child
// process through the public engine — the input crosses in, the child computes,
// and the output crosses back into the caller's scope.
func TestCallActivityE2E(t *testing.T) {
	var saw atomic.Int64

	callee := scaleCallee(t, "calc", 2)
	caller := callerProcess(t, callee.ID(), 21, 0, &saw)

	st, err := runCaller(t, caller, callee)
	require.NoError(t, err)
	require.Equal(t, thresher.StateCompleted, st)
	require.EqualValues(t, 42, saw.Load(),
		"the child's output crossed back into the caller")
}

// TestCallActivityPinnedVersion (SRD-050 §6, M4): a pinned call binds the exact
// registered version, not the latest.
func TestCallActivityPinnedVersion(t *testing.T) {
	var saw atomic.Int64

	v1 := scaleCallee(t, "calc", 2) // version 1: doubler
	v2 := scaleCallee(t, "calc", 3) // version 2: tripler (same key)
	caller := callerProcess(t, "calc", 10, 1, &saw)

	st, err := runCaller(t, caller, v1, v2)
	require.NoError(t, err)
	require.Equal(t, thresher.StateCompleted, st)
	require.EqualValues(t, 20, saw.Load(),
		"the pin binds v1 (doubler), ignoring the newer v2 (tripler)")
}

// TestCallActivityChildErrorCaught (SRD-050 §6, M4): a child BpmnError faults
// the caller at the Call Activity node; an Error boundary catches it and the
// exception flow runs.
func TestCallActivityChildErrorCaught(t *testing.T) {
	var saw atomic.Int64

	callee := raisingCallee(t, "raiser", "E_CALL")
	caller := callerWithBoundary(t, callee.ID(), "E_CALL", &saw)

	st, err := runCaller(t, caller, callee)
	require.NoError(t, err)
	require.Equal(t, thresher.StateCompleted, st)
	require.EqualValues(t, -1, saw.Load(),
		"the Error boundary on the Call Activity caught the child fault")
}
