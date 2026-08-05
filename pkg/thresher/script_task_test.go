package thresher_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/repository/memrepo"
	"github.com/dr-dobermann/gobpm/pkg/script"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

// e2eScriptEngine is a routing-observable stub engine: it "executes" by
// returning a fixed named output and records what it ran.
type e2eScriptEngine struct {
	mu      sync.Mutex
	kind    string
	formats []string
	outName string
	ran     []string
}

func (e *e2eScriptEngine) Type() string { return e.kind }

func (e *e2eScriptEngine) Formats() []string { return e.formats }

func (e *e2eScriptEngine) Execute(
	_ context.Context, format, body string, _ service.DataReader,
) (script.Outputs, error) {
	e.mu.Lock()
	e.ran = append(e.ran, format+"|"+body)
	e.mu.Unlock()

	return script.Outputs{e.outName: values.NewVariable(e.kind)}, nil
}

func (e *e2eScriptEngine) runs() []string {
	e.mu.Lock()
	defer e.mu.Unlock()

	return append([]string{}, e.ran...)
}

// twoScriptProcess builds start → warm(sleep) → alphaTask → betaTask →
// end, with the two ScriptTasks carrying different formats.
func twoScriptProcess(t *testing.T, id string) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	proc, err := process.New(id)
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	warm, err := activities.NewServiceTask("warm",
		nopOp(t, "warm-op", 300*time.Millisecond),
		activities.WithoutParams())
	require.NoError(t, err)

	alpha, err := activities.NewScriptTask("alpha-script", "text/x-alpha",
		"alpha body")
	require.NoError(t, err)

	beta, err := activities.NewScriptTask("beta-script", "TEXT/X-BETA",
		"beta body")
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, warm, alpha, beta, end} {
		require.NoError(t, proc.Add(e))
	}

	link(t, start, warm)
	link(t, warm, alpha)
	link(t, alpha, beta)
	link(t, beta, end)

	return proc
}

// runScripts starts an engine with opts, runs proc and returns the
// collected facts and the completion error.
func runScripts(
	t *testing.T, proc *process.Process, opts ...thresher.Option,
) ([]observability.Fact, error) {
	t.Helper()

	th, err := thresher.New("test-"+proc.ID(),
		append([]thresher.Option{thresher.WithoutBanner()}, opts...)...)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, th.Run(ctx))

	_, err = th.RegisterProcess(proc)
	require.NoError(t, err)

	h, err := th.StartLatest(proc.ID())
	require.NoError(t, err)

	c := &collector{}
	sub := h.Observe(c)

	wctx, wcancel := context.WithTimeout(ctx, 5*time.Second)
	defer wcancel()

	// A failing script no longer terminates the instance — it opens an
	// incident and the instance stays alive (SRD-079 FR-1), so completion
	// never comes. Race WaitCompletion against the incident: on an incident,
	// the raised fact's error detail is the failure the callers assert on.
	done := make(chan error, 1)

	go func() {
		_, e := h.WaitCompletion(wctx)
		done <- e
	}()

	var werr error

wait:
	for {
		select {
		case werr = <-done:
			break wait

		case <-time.After(5 * time.Millisecond):
			if h.OpenIncidents() == 0 {
				continue
			}

			if e := incidentCause(c); e != nil {
				werr = e

				break wait
			}
		}
	}

	sub.Cancel()

	c.mu.Lock()
	defer c.mu.Unlock()

	return append([]observability.Fact{}, c.events...), werr
}

// waitCompletionOrIncident waits for the instance to complete OR to open an
// incident (SRD-079: a technical failure parks the instance instead of
// terminating it). On completion it returns WaitCompletion's error; on an
// incident, an error carrying the raised fact's cause — what the pre-incident
// tests used to read off the terminal fault.
func waitCompletionOrIncident(
	t *testing.T, ctx context.Context, h *thresher.InstanceHandle,
) error {
	t.Helper()

	c := &collector{}
	sub := h.Observe(c)
	defer sub.Cancel()

	done := make(chan error, 1)

	go func() {
		_, e := h.WaitCompletion(ctx)
		done <- e
	}()

	for {
		select {
		case e := <-done:
			return e

		case <-time.After(5 * time.Millisecond):
			if h.OpenIncidents() == 0 {
				continue
			}

			if e := incidentCause(c); e != nil {
				return e
			}
		}
	}
}

// incidentCause extracts the raised incident's cause from the collected facts
// as an error; nil until the raised fact has arrived.
func incidentCause(c *collector) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, f := range c.events {
		if f.Kind == observability.KindFault &&
			f.Phase == observability.PhaseIncident {
			return errors.New(f.Details[observability.AttrError])
		}
	}

	return nil
}

// scriptFacts filters the KindScript facts with the given phase.
func scriptFacts(
	ff []observability.Fact, phase observability.Phase,
) []observability.Fact {
	var out []observability.Fact

	for _, f := range ff {
		if f.Kind == observability.KindScript && f.Phase == phase {
			out = append(out, f)
		}
	}

	return out
}

// TestScriptTaskE2E covers SRD-064 T-5: two registered engines, live
// format routing, per-name commits read downstream, and the fact
// attribution.
func TestScriptTaskE2E(t *testing.T) {
	alpha := &e2eScriptEngine{kind: "##Alpha",
		formats: []string{"text/x-alpha"}, outName: "alpha_out"}
	beta := &e2eScriptEngine{kind: "##Beta",
		formats: []string{"text/x-beta"}, outName: "beta_out"}

	facts, err := runScripts(t, twoScriptProcess(t, "st-route"),
		thresher.WithScriptEngine(alpha), thresher.WithScriptEngine(beta))
	require.NoError(t, err)

	require.Equal(t, []string{"text/x-alpha|alpha body"}, alpha.runs(),
		"alpha must run exactly its own task")
	require.Equal(t, []string{"TEXT/X-BETA|beta body"}, beta.runs(),
		"beta must run exactly its own task (routing is case-insensitive)")

	executed := scriptFacts(facts, observability.PhaseExecuted)
	require.Len(t, executed, 2)

	kinds := map[string]string{}
	for _, f := range executed {
		kinds[f.Details[observability.AttrScriptFormat]] =
			f.Details[observability.AttrImplementation]
	}

	require.Equal(t, "##Alpha", kinds["text/x-alpha"])
	require.Equal(t, "##Beta", kinds["TEXT/X-BETA"])
}

// TestScriptTaskUnclaimedFormat: a format nobody claims faults the
// instance with the claims-listing error.
func TestScriptTaskUnclaimedFormat(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	proc, err := process.New("st-unclaimed")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	ruby, err := activities.NewScriptTask("ruby", "text/x-ruby", "puts 1")
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, ruby, end} {
		require.NoError(t, proc.Add(e))
	}

	link(t, start, ruby)
	link(t, ruby, end)

	alpha := &e2eScriptEngine{kind: "##Alpha",
		formats: []string{"text/x-alpha"}, outName: "x"}

	_, werr := runScripts(t, proc, thresher.WithScriptEngine(alpha))
	require.Error(t, werr)
	require.Contains(t, werr.Error(), "text/x-ruby")
	require.Contains(t, werr.Error(), "text/x-alpha",
		"the failure must list the registered claims")
}

// TestIncidentHandleSurface (SRD-079 M2, T-5 at the public surface): a
// technical failure surfaces on the handle — the incident view carries the
// node, cause, state and the failure-time data snapshot, and the token stays
// visible at the failing node in the Incident state.
func TestIncidentHandleSurface(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	proc, err := process.New("st-incident-surface")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	lua, err := activities.NewScriptTask("calc", "text/x-lua", "return {}")
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, lua, end} {
		require.NoError(t, proc.Add(e))
	}

	link(t, start, lua)
	link(t, lua, end)

	th, err := thresher.New("test-incident-surface", thresher.WithoutBanner())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, th.Run(ctx))

	_, err = th.RegisterProcess(proc)
	require.NoError(t, err)

	h, err := th.StartLatest(proc.ID())
	require.NoError(t, err)

	require.Eventually(t, func() bool { return h.OpenIncidents() == 1 },
		5*time.Second, 5*time.Millisecond)

	incs := h.Incidents()
	require.Len(t, incs, 1)
	require.Equal(t, lua.ID(), incs[0].NodeID)
	require.Equal(t, "calc", incs[0].NodeName)
	require.Equal(t, "open", incs[0].State)
	require.Equal(t, 1, incs[0].Attempts)
	require.Contains(t, incs[0].Cause, "WithScriptEngine")
	require.False(t, incs[0].FirstAt.IsZero())

	var found bool

	for _, tok := range h.Tokens() {
		if tok.NodeID == lua.ID() {
			require.Equal(t, thresher.TokenIncident, tok.State)

			found = true
		}
	}

	require.True(t, found, "the incident token must be visible on the handle")
}

// incidentParkedInstance runs start → lua(script, no engine) → end to its
// incident park and returns the handle and the incident id.
func incidentParkedInstance(
	t *testing.T, procID string,
) (*thresher.InstanceHandle, string) {
	t.Helper()
	require.NoError(t, data.CreateDefaultStates())

	proc, err := process.New(procID)
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	lua, err := activities.NewScriptTask("calc", "text/x-lua", "return {}")
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, lua, end} {
		require.NoError(t, proc.Add(e))
	}

	link(t, start, lua)
	link(t, lua, end)

	th, err := thresher.New("test-"+procID, thresher.WithoutBanner(),
		thresher.WithRepository(memrepo.New()))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, th.Run(ctx))

	_, err = th.RegisterProcess(proc)
	require.NoError(t, err)

	h, err := th.StartLatest(proc.ID())
	require.NoError(t, err)

	require.Eventually(t, func() bool { return h.OpenIncidents() == 1 },
		5*time.Second, 5*time.Millisecond)

	return h, h.Incidents()[0].ID
}

// TestIncidentOpsOnParkedInstance (SRD-079 M5, T-9/T-10 at the public
// surface): the operator's resolve and drop land on a PARKED instance — the
// engine rebuilds it from its checkpoint, applies the op on the fresh loop,
// and the op is never lost to the re-park.
func TestIncidentOpsOnParkedInstance(t *testing.T) {
	t.Run("resolve continues past the node", func(t *testing.T) {
		h, incID := incidentParkedInstance(t, "st-inc-resolve")

		require.NoError(t,
			h.ResolveIncident(context.Background(), incID))

		// the continuation proceeds from the script task's outgoing flow —
		// without re-executing a script nothing can run — to completion.
		require.Eventually(t,
			func() bool { return h.State() == thresher.StateCompleted },
			5*time.Second, 5*time.Millisecond)

		incs := h.Incidents()
		require.Len(t, incs, 1)
		require.Equal(t, "resolved", incs[0].State)
		require.Zero(t, h.OpenIncidents())
	})

	t.Run("drop dead-letters and blocks completion", func(t *testing.T) {
		h, incID := incidentParkedInstance(t, "st-inc-drop")

		require.NoError(t, h.DropIncident(context.Background(), incID))

		incs := h.Incidents()
		require.Len(t, incs, 1)
		require.Equal(t, "dead-lettered", incs[0].State)
		require.Zero(t, h.OpenIncidents())
		require.NotEqual(t, thresher.StateCompleted, h.State(),
			"an instance never completes normally past a dead letter")
	})

	t.Run("an unknown incident id is a named error", func(t *testing.T) {
		h, _ := incidentParkedInstance(t, "st-inc-badid")

		err := h.RetryIncident(context.Background(), "ghost")
		require.Error(t, err)
		require.Contains(t, err.Error(), "ghost")
	})
}

// TestIncidentOpOnVolatileParkedInstance: without a configured repository
// there is no checkpoint to rebuild a parked instance from — the op fails
// loudly instead of pretending.
func TestIncidentOpOnVolatileParkedInstance(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	proc, err := process.New("st-inc-volatile")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	lua, err := activities.NewScriptTask("calc", "text/x-lua", "return {}")
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, lua, end} {
		require.NoError(t, proc.Add(e))
	}

	link(t, start, lua)
	link(t, lua, end)

	th, err := thresher.New("test-inc-volatile", thresher.WithoutBanner())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, th.Run(ctx))

	_, err = th.RegisterProcess(proc)
	require.NoError(t, err)

	h, err := th.StartLatest(proc.ID())
	require.NoError(t, err)

	require.Eventually(t, func() bool { return h.OpenIncidents() == 1 },
		5*time.Second, 5*time.Millisecond)

	err = h.RetryIncident(context.Background(), h.Incidents()[0].ID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "doesn't wake")
}

// TestIncidentOpRetryNow (SRD-079 M5): the operator's retry-now re-enters the
// failed node with no policy involved — a flaky service task heals on the
// second attempt and the instance completes.
func TestIncidentOpRetryNow(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var calls int32

	op, err := gooper.New("flaky",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition,
		) (*data.ItemDefinition, error) {
			if atomic.AddInt32(&calls, 1) == 1 {
				return nil, fmt.Errorf("first attempt fails")
			}

			return nil, nil
		})
	require.NoError(t, err)

	proc, err := process.New("st-inc-retrynow")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	task, err := activities.NewServiceTask("flaky", op,
		activities.WithoutParams())
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, task, end} {
		require.NoError(t, proc.Add(e))
	}

	link(t, start, task)
	link(t, task, end)

	th, err := thresher.New("test-retrynow", thresher.WithoutBanner(),
		thresher.WithRepository(memrepo.New()))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, th.Run(ctx))

	_, err = th.RegisterProcess(proc)
	require.NoError(t, err)

	h, err := th.StartLatest(proc.ID())
	require.NoError(t, err)

	require.Eventually(t, func() bool { return h.OpenIncidents() == 1 },
		5*time.Second, 5*time.Millisecond)

	require.NoError(t,
		h.RetryIncident(context.Background(), h.Incidents()[0].ID))

	require.Eventually(t,
		func() bool { return h.State() == thresher.StateCompleted },
		5*time.Second, 5*time.Millisecond)
	require.EqualValues(t, 2, atomic.LoadInt32(&calls))
}

// TestScriptTaskNoEngine: the zero-config ##None default fails loud with
// the wiring hint.
func TestScriptTaskNoEngine(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	proc, err := process.New("st-none")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	lua, err := activities.NewScriptTask("calc", "text/x-lua", "return {}")
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, lua, end} {
		require.NoError(t, proc.Add(e))
	}

	link(t, start, lua)
	link(t, lua, end)

	_, werr := runScripts(t, proc)
	require.Error(t, werr)
	require.Contains(t, werr.Error(), "WithScriptEngine")
}
