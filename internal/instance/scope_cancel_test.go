package instance

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
)

// SRD-049 M5 — scope-cancel: the scoped Terminate, the interrupting
// boundary on a composite, and the error scope-chain.

// blockedReceive builds a ReceiveTask that parks forever (its message is
// never sent) — the "remaining token" a cancel must discard.
func blockedReceive(t *testing.T, name string) *activities.ReceiveTask {
	t.Helper()

	rt, err := activities.NewReceiveTask(name,
		bpmncommon.MustMessage("never-"+name,
			data.MustItemDefinition(values.NewVariable(1))))
	require.NoError(t, err)

	return rt
}

// errDef builds an ErrorEventDefinition with the given code.
func errDef(t *testing.T, name, code string) *events.ErrorEventDefinition {
	t.Helper()

	bpErr, err := bpmncommon.NewError(name, code, nil)
	require.NoError(t, err)

	eed, err := events.NewErrorEventDefinition(bpErr)
	require.NoError(t, err)

	return eed
}

// throwTask builds a task whose operation raises a BpmnError with code.
func throwTask(t *testing.T, name, code string) *activities.ServiceTask {
	t.Helper()

	op, err := gooper.New(name,
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			return nil, &events.BpmnError{Code: code}
		})
	require.NoError(t, err)

	st, err := activities.NewServiceTask(name, op, activities.WithoutParams())
	require.NoError(t, err)

	return st
}

// TestScopedTerminate — a Terminate End Event inside a sub-process
// discards only ITS scope's tokens (a blocked sibling branch dies), the
// composite completes, and the parent continues (§13.5.6, SRD-049 FR-11).
func TestScopedTerminate(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	sp, err := activities.NewSubProcess("body")
	require.NoError(t, err)

	sStart, err := events.NewStartEvent("s-start")
	require.NoError(t, err)
	fork, err := gateways.NewParallelGateway(
		gateways.WithDirection(gateways.Diverging))
	require.NoError(t, err)

	stuck := blockedReceive(t, "stuck")

	ted, err := events.NewTerminateEventDefinition()
	require.NoError(t, err)
	term, err := events.NewEndEvent("term", events.WithTerminateTrigger(ted))
	require.NoError(t, err)

	for _, e := range []flow.Element{sStart, fork, stuck, term} {
		require.NoError(t, sp.Add(e))
	}
	linkAll(t,
		[2]flow.Element{sStart, fork},
		[2]flow.Element{fork, stuck},
		[2]flow.Element{fork, term})

	var after atomic.Int32
	inst := runInstance(t,
		wrapSP(t, "scoped-term", sp, hitTask(t, "after", &after, "", 0)))

	require.Equal(t, Completed, inst.State(),
		"the parent must complete — only the scope died")
	require.NoError(t, inst.LastErr())
	require.EqualValues(t, 1, after.Load(),
		"the host resumed onto its outgoing")
}

// capturingProducer captures registered processors per definition id, so a
// test can fire a boundary watch by hand. Guarded: registrations arrive
// from instance goroutines while tests poll.
type capturingProducer struct {
	m     sync.Mutex
	procs map[string]eventproc.EventProcessor
}

func (c *capturingProducer) RegisterEvent(
	p eventproc.EventProcessor, d flow.EventDefinition,
) error {
	c.m.Lock()
	defer c.m.Unlock()

	c.procs[d.ID()] = p

	return nil
}

// watch returns a captured boundaryWatch, if any.
func (c *capturingProducer) watch() eventproc.EventProcessor {
	c.m.Lock()
	defer c.m.Unlock()

	for _, pr := range c.procs {
		if _, isWatch := pr.(*boundaryWatch); isWatch {
			return pr
		}
	}

	return nil
}

// scopeWatch returns a captured Event Sub-Process handler watch, if any — the
// scope-level peer of watch() (SRD-052).
func (c *capturingProducer) scopeWatch() *scopeHandlerWatch {
	c.m.Lock()
	defer c.m.Unlock()

	for _, pr := range c.procs {
		if w, ok := pr.(*scopeHandlerWatch); ok {
			return w
		}
	}

	return nil
}

// numProcs returns how many processors are currently registered — a test waits
// on it to know a nested scope's inner waiters have all armed.
func (c *capturingProducer) numProcs() int {
	c.m.Lock()
	defer c.m.Unlock()

	return len(c.procs)
}

func (c *capturingProducer) UnregisterEvent(
	_ eventproc.EventProcessor, _ string,
) error {
	return nil
}

func (c *capturingProducer) UnregisterProcessor(_ eventproc.EventProcessor) {}

func (c *capturingProducer) PropagateEvent(
	_ context.Context, _ flow.EventDefinition,
) error {
	return nil
}

// TestBoundaryOnCompositeInterrupts — an interrupting signal boundary on a
// sub-process cancels the whole scope (a blocked inner track dies) and
// routes the exception flow (ADR-023 §2.5, SRD-049 FR-10).
func TestBoundaryOnCompositeInterrupts(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	sp, err := activities.NewSubProcess("guarded")
	require.NoError(t, err)

	sStart, err := events.NewStartEvent("s-start")
	require.NoError(t, err)
	stuck := blockedReceive(t, "stuck")
	sEnd, err := events.NewEndEvent("s-end")
	require.NoError(t, err)

	for _, e := range []flow.Element{sStart, stuck, sEnd} {
		require.NoError(t, sp.Add(e))
	}
	linkAll(t, [2]flow.Element{sStart, stuck}, [2]flow.Element{stuck, sEnd})

	sig, err := events.NewSignal("break",
		data.MustItemDefinition(values.NewVariable(1)))
	require.NoError(t, err)
	sdef, err := events.NewSignalEventDefinition(sig)
	require.NoError(t, err)

	bnd, err := events.NewBoundaryEvent("bnd", sp, sdef, true)
	require.NoError(t, err)

	var exc atomic.Int32
	excTask := hitTask(t, "exc", &exc, "", 0)

	p, err := process.New("bnd-composite")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)
	excEnd, err := events.NewEndEvent("exc-end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, sp, bnd, excTask, end, excEnd} {
		require.NoError(t, p.Add(e))
	}
	linkAll(t,
		[2]flow.Element{start, sp},
		[2]flow.Element{sp, end},
		[2]flow.Element{bnd, excTask},
		[2]flow.Element{excTask, excEnd})

	s, err := snapshot.New(p)
	require.NoError(t, err)

	cp := &capturingProducer{procs: map[string]eventproc.EventProcessor{}}

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(), cp, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, inst.Run(ctx))

	// wait until the boundary watch registered, then fire it.
	var proc eventproc.EventProcessor
	require.Eventually(t, func() bool {
		proc = cp.watch()

		return proc != nil
	}, 3*time.Second, 5*time.Millisecond)

	// the fired definition: the boundary's own signal def (any instance
	// carries the same id via the clone).
	require.NoError(t, proc.ProcessEvent(ctx,
		proc.(*boundaryWatch).def))

	require.Eventually(t,
		func() bool { return inst.State() == Completed },
		3*time.Second, 5*time.Millisecond)
	require.NoError(t, inst.LastErr())
	require.EqualValues(t, 1, exc.Load(), "the exception flow must run")
}

// chainProcess builds start → outer[ start → inner? ] with an Error
// boundary (code) on the composite at `level` and a thrower nested at the
// deepest level.
func chainProcess(
	t *testing.T, name, boundaryCode, thrownCode string, twoLevels bool,
	exc *atomic.Int32,
) *process.Process {
	t.Helper()

	thrower := throwTask(t, "thrower", thrownCode)

	deepest, err := activities.NewSubProcess("deep")
	require.NoError(t, err)
	dStart, err := events.NewStartEvent("d-start")
	require.NoError(t, err)
	dEnd, err := events.NewEndEvent("d-end")
	require.NoError(t, err)
	for _, e := range []flow.Element{dStart, thrower, dEnd} {
		require.NoError(t, deepest.Add(e))
	}
	linkAll(t, [2]flow.Element{dStart, thrower},
		[2]flow.Element{thrower, dEnd})

	guarded := deepest
	if twoLevels {
		outer, err := activities.NewSubProcess("outer")
		require.NoError(t, err)
		oStart, err := events.NewStartEvent("o-start")
		require.NoError(t, err)
		oEnd, err := events.NewEndEvent("o-end")
		require.NoError(t, err)
		for _, e := range []flow.Element{oStart, deepest, oEnd} {
			require.NoError(t, outer.Add(e))
		}
		linkAll(t, [2]flow.Element{oStart, deepest},
			[2]flow.Element{deepest, oEnd})
		guarded = outer
	}

	bnd, err := events.NewBoundaryEvent("catch", guarded,
		errDef(t, "chain-err-"+name, boundaryCode), true)
	require.NoError(t, err)

	excTask := hitTask(t, "exc", exc, "", 0)

	p, err := process.New(name)
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)
	excEnd, err := events.NewEndEvent("exc-end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, guarded, bnd, excTask, end, excEnd} {
		require.NoError(t, p.Add(e))
	}
	linkAll(t,
		[2]flow.Element{start, guarded},
		[2]flow.Element{guarded, end},
		[2]flow.Element{bnd, excTask},
		[2]flow.Element{excTask, excEnd})

	return p
}

// TestErrorScopeChain — the §10.5.1 innermost-enclosing-catcher walk
// (ADR-023 §2.6, SRD-049 FR-12).
func TestErrorScopeChain(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	t.Run("caught one level up", func(t *testing.T) {
		var exc atomic.Int32

		inst := runInstance(t,
			chainProcess(t, "one-level", "E1", "E1", false, &exc))

		require.Equal(t, Completed, inst.State())
		require.NoError(t, inst.LastErr())
		require.EqualValues(t, 1, exc.Load())
	})

	t.Run("caught two levels up", func(t *testing.T) {
		var exc atomic.Int32

		inst := runInstance(t,
			chainProcess(t, "two-levels", "E1", "E1", true, &exc))

		require.Equal(t, Completed, inst.State())
		require.NoError(t, inst.LastErr())
		require.EqualValues(t, 1, exc.Load())
	})

	t.Run("no code match faults the instance", func(t *testing.T) {
		var exc atomic.Int32

		inst := runInstance(t,
			chainProcess(t, "no-match", "E1", "OTHER", false, &exc))

		require.Equal(t, Terminated, inst.State())
		require.Error(t, inst.LastErr())
		require.Zero(t, exc.Load())
	})

	t.Run("error end inside is caught by the composite", func(t *testing.T) {
		var exc atomic.Int32

		sp, err := activities.NewSubProcess("body")
		require.NoError(t, err)

		sStart, err := events.NewStartEvent("s-start")
		require.NoError(t, err)
		errEnd, err := events.NewEndEvent("err-end",
			events.WithErrorTrigger(errDef(t, "ee", "E7")))
		require.NoError(t, err)

		require.NoError(t, sp.Add(sStart))
		require.NoError(t, sp.Add(errEnd))
		linkAll(t, [2]flow.Element{sStart, errEnd})

		bnd, err := events.NewBoundaryEvent("catch", sp,
			errDef(t, "ee-catch", "E7"), true)
		require.NoError(t, err)

		excTask := hitTask(t, "exc", &exc, "", 0)

		p, err := process.New("error-end-inside")
		require.NoError(t, err)

		start, err := events.NewStartEvent("start")
		require.NoError(t, err)
		end, err := events.NewEndEvent("end")
		require.NoError(t, err)
		excEnd, err := events.NewEndEvent("exc-end")
		require.NoError(t, err)

		for _, e := range []flow.Element{start, sp, bnd, excTask, end, excEnd} {
			require.NoError(t, p.Add(e))
		}
		linkAll(t,
			[2]flow.Element{start, sp},
			[2]flow.Element{sp, end},
			[2]flow.Element{bnd, excTask},
			[2]flow.Element{excTask, excEnd})

		inst := runInstance(t, p)

		require.Equal(t, Completed, inst.State(),
			"the Error End inside must be caught by the enclosing composite")
		require.NoError(t, inst.LastErr())
		require.EqualValues(t, 1, exc.Load())
	})
}

var _ = mock.Anything // mockeventproc stays imported for other files
var _ = mockeventproc.NewMockEventProducer
