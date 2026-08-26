package thresher_test

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
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
