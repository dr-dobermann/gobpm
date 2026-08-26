package thresher_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

// intInput declares a required (or optional) int input whose item carries
// the given id, so a test can prove the bound datum is the DECLARATION's
// instance and not the delivered one.
func intInput(
	t *testing.T, name, itemID string, opts ...data.ParameterOption,
) *data.Parameter {
	t.Helper()

	return data.MustParameter(name,
		data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable(0),
				foundation.WithID(itemID)),
			data.ReadyDataState),
		opts...)
}

// contractedProcess builds start → end declaring subtotal (required) and
// discount (optional) as inputs. No outputs: M2 is the launch half.
func contractedProcess(t *testing.T, id string) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New(id, foundation.WithID(id),
		data.WithInputs(
			intInput(t, "subtotal", id+"-subtotal"),
			intInput(t, "discount", id+"-discount", data.Optional())))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, end)

	return p
}

// bootIOEngine registers p on a running engine.
func bootIOEngine(
	t *testing.T, name string, p *process.Process,
) *thresher.Thresher {
	t.Helper()

	th, err := thresher.New(name)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, th.Run(ctx))

	_, err = th.RegisterProcess(p)
	require.NoError(t, err)

	return th
}

// TestLaunchBindsDeclaredInputs — SRD-093 T-5: the host's start inputs
// arrive as the DECLARED parameters, typed by the declaration; an optional
// input not supplied stays absent.
func TestLaunchBindsDeclaredInputs(t *testing.T) {
	p := contractedProcess(t, "io-bind")
	th := bootIOEngine(t, "io-bind-engine", p)

	h, err := th.StartLatest(p.ID(), thresher.WithStartInput("subtotal", 120))
	require.NoError(t, err)

	wctx, wc := context.WithTimeout(context.Background(), 5*time.Second)
	defer wc()

	state, err := h.WaitCompletion(wctx)
	require.NoError(t, err)
	require.Equal(t, thresher.StateCompleted, state)

	d, err := h.Data().GetData("subtotal")
	require.NoError(t, err)
	require.Equal(t, 120, d.Value().Get(context.Background()))
	require.Equal(t, "io-bind-subtotal", d.ItemDefinition().ID(),
		"the bound datum is the declaration's instance, not the delivered one")

	_, err = h.Data().GetData("discount")
	require.Error(t, err, "an optional input not supplied is absent")
}

// TestLaunchRefusesUnboundRequiredInput — SRD-093 T-6: no instance comes
// out of a launch missing a required input.
func TestLaunchRefusesUnboundRequiredInput(t *testing.T) {
	p := contractedProcess(t, "io-required")
	th := bootIOEngine(t, "io-required-engine", p)

	_, err := th.StartLatest(p.ID(), thresher.WithStartInput("discount", 5))
	require.Error(t, err)
	require.ErrorContains(t, err, `required input "subtotal"`)
	require.ErrorContains(t, err, "io-required")

	ids, err := th.Instances(thresher.InstanceQuery{})
	require.NoError(t, err)
	require.Empty(t, ids, "a refused launch leaves no instance")
}

// TestLaunchRefusesUndeclaredDatum — SRD-093 T-7: with a contract the
// boundary is strict both ways; without one, anything is accepted.
func TestLaunchRefusesUndeclaredDatum(t *testing.T) {
	p := contractedProcess(t, "io-strict")
	th := bootIOEngine(t, "io-strict-engine", p)

	_, err := th.StartLatest(p.ID(),
		thresher.WithStartInput("subtotal", 120),
		thresher.WithStartInput("subttl", 1))
	require.ErrorContains(t, err, `declares no input "subttl"`)
	require.ErrorContains(t, err, "subtotal, discount")

	// The contract-less process takes whatever it is handed (ADR-040 §2.5).
	require.NoError(t, data.CreateDefaultStates())

	plain, err := process.New("io-plain", foundation.WithID("io-plain"))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, end} {
		require.NoError(t, plain.Add(e))
	}

	link(t, start, end)

	_, err = th.RegisterProcess(plain)
	require.NoError(t, err)

	h, err := th.StartLatest("io-plain", thresher.WithStartInput("anything", 1))
	require.NoError(t, err)

	wctx, wc := context.WithTimeout(context.Background(), 5*time.Second)
	defer wc()

	_, err = h.WaitCompletion(wctx)
	require.NoError(t, err)

	d, err := h.Data().GetData("anything")
	require.NoError(t, err)
	require.Equal(t, 1, d.Value().Get(context.Background()))
}

// TestLaunchTypeChecksInput — SRD-093 T-8: the declaration's item is the
// template, so a value it cannot hold refuses the launch.
func TestLaunchTypeChecksInput(t *testing.T) {
	p := contractedProcess(t, "io-typed")
	th := bootIOEngine(t, "io-typed-engine", p)

	_, err := th.StartLatest(p.ID(), thresher.WithStartInput("subtotal", "120"))
	require.ErrorContains(t, err, `input "subtotal" rejects the delivered value`)
}

// contractedScaleCallee is scaleCallee with the contract declared: input
// amount (required), output result (required) — the callee side ADR-040
// §2.4 gives the call boundary. Its Go operation reads amount and returns
// result = amount*f, committed into the root scope under the output's name.
func contractedScaleCallee(
	t *testing.T, key string, f int, extraOutputs ...*data.Parameter,
) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New("callee", foundation.WithID(key),
		data.WithInputs(ioParam(t, "amount")),
		data.WithOutputs(ioParam(t, "result")),
		data.WithOutputs(extraOutputs...))
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

// TestOutputsCollectedAtCompletion — SRD-093 T-10: the host reads the
// declared result after Completed; it is a copy taken at completion.
func TestOutputsCollectedAtCompletion(t *testing.T) {
	p := contractedScaleCallee(t, "io-result", 3)
	th := bootIOEngine(t, "io-result-engine", p)

	h, err := th.StartLatest(p.ID(), thresher.WithStartInput("amount", 14))
	require.NoError(t, err)

	wctx, wc := context.WithTimeout(context.Background(), 5*time.Second)
	defer wc()

	state, err := h.WaitCompletion(wctx)
	require.NoError(t, err)
	require.Equal(t, thresher.StateCompleted, state)

	outs := h.Outputs()
	require.Len(t, outs, 1)
	require.Equal(t, "result", outs[0].Name())
	require.Equal(t, 42, outs[0].Value().Get(context.Background()))

	// a copy: mutating what the host got leaves the instance's result alone
	require.NoError(t, outs[0].Value().Update(context.Background(), 0))
	require.Equal(t, 42, h.Outputs()[0].Value().Get(context.Background()))
}

// TestCallBindsThroughDeclaredInputs — SRD-093 T-13/T-14: a contracted
// callee called through the existing caller path binds the caller's input
// through its declaration and returns its collected result to the caller's
// scope; the caller's check task records it.
func TestCallBindsThroughDeclaredInputs(t *testing.T) {
	var saw atomic.Int64

	state, err := runCaller(t,
		callerProcess(t, "io-callee", 7, 0, &saw),
		contractedScaleCallee(t, "io-callee", 6))
	require.NoError(t, err)
	require.Equal(t, thresher.StateCompleted, state)
	require.Equal(t, int64(42), saw.Load(),
		"the caller committed the child's declared result")
}

// TestCallBoundaryValidatesOutputs — SRD-093 T-12: a caller output the
// callee's contract does not declare faults the call at launch, at the
// Call Activity, naming both sides.
func TestCallBoundaryValidatesOutputs(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	caller, err := process.New("caller-mismatch",
		data.WithProperties(
			data.MustProperty("amount",
				data.MustItemDefinition(values.NewVariable(1),
					foundation.WithID("amount")),
				data.ReadyDataState)))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	ca, err := activities.NewCallActivity("charge", "io-strict-callee",
		activities.WithParameters(data.Input, ioParam(t, "amount")),
		activities.WithParameters(data.Output, ioParam(t, "grandTotal")))
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, ca, end} {
		require.NoError(t, caller.Add(e))
	}

	link(t, start, ca)
	link(t, ca, end)

	// The refusal reaches the caller as the Call Activity's fault: an
	// unhandled failure at that node, which the incident machinery records
	// against it (ADR-036) — the caller parks there instead of running on.
	th := bootIOEngine(t, "io-boundary-engine", caller)

	_, err = th.RegisterProcess(contractedScaleCallee(t, "io-strict-callee", 2))
	require.NoError(t, err)

	h, err := th.StartLatest(caller.ID())
	require.NoError(t, err)

	require.Eventually(t, func() bool { return h.OpenIncidents() == 1 },
		5*time.Second, 10*time.Millisecond,
		"the contract mismatch must fault the caller at the Call Activity")

	inc := h.Incidents()
	require.Len(t, inc, 1)
	require.Equal(t, "charge", inc[0].NodeName)
	require.Contains(t, inc[0].Cause, `output "grandTotal" is not declared`)
	require.Contains(t, inc[0].Cause, "result")
}

// noteParam declares "note": an optional string output.
func noteParam(t *testing.T) *data.Parameter {
	t.Helper()

	return data.MustParameter("note",
		data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable("")),
			data.ReadyDataState),
		data.Optional())
}

// optionalOnlyCallee declares amount in and ONLY the optional note out, and
// produces nothing: its collected result is empty, not absent.
func optionalOnlyCallee(t *testing.T, key string) *process.Process {
	t.Helper()

	p, err := process.New("quiet", foundation.WithID(key),
		data.WithInputs(ioParam(t, "amount")),
		data.WithOutputs(noteParam(t)))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, end)

	return p
}

// optionalCaller builds start → charge[calls key] → end, mapping amount in
// and the given outputs back.
func optionalCaller(
	t *testing.T, name, key string, outputs ...*data.Parameter,
) *process.Process {
	t.Helper()

	caller, err := process.New(name,
		data.WithProperties(
			data.MustProperty("amount",
				data.MustItemDefinition(values.NewVariable(1),
					foundation.WithID("amount")),
				data.ReadyDataState)))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	ca, err := activities.NewCallActivity("charge", key,
		activities.WithParameters(data.Input, ioParam(t, "amount")),
		activities.WithParameters(data.Output, outputs...))
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, ca, end} {
		require.NoError(t, caller.Add(e))
	}

	link(t, start, ca)
	link(t, ca, end)

	return caller
}

// TestCallerReadsUnproducedOptionalOutput — SRD-093 FR-9's edge: a callee
// may declare an OPTIONAL output and never produce it. The caller's output
// name passes the launch check (it is declared), and at completion it
// simply does not flow (ADR-040 §2.3): the caller completes with the name
// unbound, no incident. The second case has the callee produce NOTHING —
// an empty result, which must still be served as the result and never fall
// through to the child's raw scope.
func TestCallerReadsUnproducedOptionalOutput(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	tests := map[string]struct {
		callee  *process.Process
		outputs []*data.Parameter
	}{
		"result produced, note absent": {
			callee:  contractedScaleCallee(t, "io-opt-callee", 2, noteParam(t)),
			outputs: []*data.Parameter{ioParam(t, "result"), noteParam(t)},
		},
		"nothing produced at all": {
			callee:  optionalOnlyCallee(t, "io-quiet-callee"),
			outputs: []*data.Parameter{noteParam(t)},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			caller := optionalCaller(t, "caller-"+tc.callee.ID(),
				tc.callee.ID(), tc.outputs...)

			th := bootIOEngine(t, "io-opt-engine-"+tc.callee.ID(), caller)

			_, err := th.RegisterProcess(tc.callee)
			require.NoError(t, err)

			h, err := th.StartLatest(caller.ID())
			require.NoError(t, err)

			wctx, wc := context.WithTimeout(context.Background(),
				5*time.Second)
			defer wc()

			state, err := h.WaitCompletion(wctx)
			require.NoError(t, err)
			require.Equal(t, thresher.StateCompleted, state)
			require.Zero(t, h.OpenIncidents(),
				"an unproduced optional output is not a fault")
		})
	}
}

// TestStartOptionsValidate covers the option constructors' own guards.
func TestStartOptionsValidate(t *testing.T) {
	p := contractedProcess(t, "io-opts")
	th := bootIOEngine(t, "io-opts-engine", p)

	_, err := th.StartLatest(p.ID(), thresher.WithStartInputs(nil))
	require.ErrorContains(t, err, "nil datum")

	_, err = th.StartLatest(p.ID(), nil)
	require.ErrorContains(t, err, "nil StartOption")

	_, err = th.StartLatest(p.ID(), thresher.WithStartInput("", 1))
	require.ErrorContains(t, err, "can't be built")

	// StartVersion takes the same options; WithStartInputs delivers ready
	// data as it is.
	sub, err := data.ReadyValueParameter("subtotal", values.NewVariable(7))
	require.NoError(t, err)

	h, err := th.StartVersion(p.ID(), 1, thresher.WithStartInputs(sub))
	require.NoError(t, err)

	wctx, wc := context.WithTimeout(context.Background(), 5*time.Second)
	defer wc()

	_, err = h.WaitCompletion(wctx)
	require.NoError(t, err)

	d, err := h.Data().GetData("subtotal")
	require.NoError(t, err)
	require.Equal(t, 7, d.Value().Get(context.Background()))
}
