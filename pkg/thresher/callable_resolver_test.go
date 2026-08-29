package thresher_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/exec"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

// qualifiedCaller builds a caller whose Call Activity names callee through
// namespace ns — the shape an imported document produces when its
// calledElement carries a prefix bound to another definitions document.
func qualifiedCaller(
	t *testing.T, callee, ns string, saw *atomic.Int64,
) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New("qualified-caller",
		data.WithProperties(
			data.MustProperty("amount",
				data.MustItemDefinition(values.NewVariable(21),
					foundation.WithID("amount")),
				data.ReadyDataState)))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	caOpts := []options.Option{
		activities.WithParameters(data.Input, ioParam(t, "amount")),
		activities.WithParameters(data.Output, ioParam(t, "result")),
	}
	if ns != "" {
		caOpts = append(caOpts, activities.WithCalledNamespace(ns))
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

// runWith runs caller against callees on a thresher built with opts, and
// returns its terminal state.
func runWith(
	t *testing.T, opts []thresher.Option,
	caller *process.Process, callees ...*process.Process,
) (thresher.InstanceState, error) {
	t.Helper()

	th, err := thresher.New("resolver-e2e", opts...)
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

	wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
	defer wcancel()
	st, werr := h.WaitCompletion(wctx)

	require.NoError(t, th.Shutdown(context.Background()))

	return st, werr
}

// runUntilIncident runs caller against callees and waits for the failed call
// to raise an incident, returning its cause.
//
// A failed call is a TECHNICAL fault, so the engine parks it as an incident an
// operator can retry once the cause is fixed — registering the missing
// process, or teaching the resolver the namespace — rather than terminating
// the instance (ADR-036). The cause text is therefore the diagnostic surface,
// and what these tests assert.
func runUntilIncident(
	t *testing.T, opts []thresher.Option,
	caller *process.Process, callees ...*process.Process,
) string {
	t.Helper()

	th, err := thresher.New("resolver-incident", opts...)
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

	require.Eventually(t, func() bool { return h.OpenIncidents() == 1 },
		10*time.Second, 10*time.Millisecond,
		"a call the engine cannot resolve must raise an incident, not hang")

	incidents := h.Incidents()
	require.Len(t, incidents, 1)

	cause := incidents[0].Cause

	require.NoError(t, th.Shutdown(context.Background()))

	return cause
}

// TestWithCallableResolverRejectsNil is SRD-096 T-4.
func TestWithCallableResolverRejectsNil(t *testing.T) {
	_, err := thresher.New("nil-resolver",
		thresher.WithCallableResolver(nil))
	require.Error(t, err,
		"a nil resolver must not silently replace the default — that is the "+
			"WithLogger(nil) failure, a bad argument becoming a default")
	require.Contains(t, err.Error(), "WithCallableResolver")
}

// TestWithCallableResolverAcceptsATypedNil records the hole the guard above
// cannot close, so nobody widens that guard believing it covers this.
//
// WithCallableResolver compares `r == nil`, and an interface holding a typed
// nil func is NOT nil by that comparison — so CallableResolverFunc(nil) is
// accepted as a perfectly good resolver. The option is right to accept it: an
// interface value it cannot inspect is not its business. The refusal belongs
// where the function is actually called, and that is what
// exec.TestCallableResolverFuncRefusesNil pins. Both halves have to exist;
// either one alone leaves a panic in the engine.
func TestWithCallableResolverAcceptsATypedNil(t *testing.T) {
	th, err := thresher.New("typed-nil-resolver",
		thresher.WithCallableResolver(exec.CallableResolverFunc(nil)))

	require.NoError(t, err,
		"the option cannot see inside the interface; if this ever starts "+
			"failing, the guard grew teeth and the note above is stale")
	require.NotNil(t, th)
}

// TestInvokeProcessRejectsANilContext covers the parameter that reaches HOST
// code. InvokeProcess hands ctx to the configured CallableResolver, which is
// whatever the embedding application wrote — so a nil one is the caller's bug
// to be told about here, not a panic inside somebody's callback.
func TestInvokeProcessRejectsANilContext(t *testing.T) {
	th, err := thresher.New("nil-ctx-invoke")
	require.NoError(t, err)

	// A nil Context variable rather than the literal, which vet rejects at
	// the call site before the guard under test can answer.
	var ctx context.Context

	child, err := th.InvokeProcess(ctx, exec.ProcessCall{
		Key:              "callee",
		ParentInstanceID: "parent",
		CallNodeID:       "call",
	})

	require.Error(t, err)
	require.Nil(t, child)
	require.Contains(t, err.Error(), "ctx",
		"the message names the parameter at fault — InvokeProcess takes "+
			"several, and 'invalid argument' would not say which")
}

// TestQualifiedCallWithoutAResolver is SRD-096 T-5: with no resolver
// configured, a qualified reference fails the CALL.
//
// The instance must reach a terminal state rather than hang, and the failure
// must name the namespace — that message is the only thing telling a host
// which document it has to teach the engine about.
func TestQualifiedCallWithoutAResolver(t *testing.T) {
	var saw atomic.Int64

	callee := scaleCallee(t, "calc", 2)
	caller := qualifiedCaller(t, "calc", "http://example.com/shared", &saw)

	cause := runUntilIncident(t, nil, caller, callee)

	require.Zero(t, saw.Load(),
		"the callee must not run: 'calc' IS registered, so taking the local "+
			"part would have called it — the silent mis-call the refusal exists "+
			"to prevent")
	require.Contains(t, cause, "http://example.com/shared",
		"the cause names the namespace nobody mapped — that message is how a "+
			"host learns which document to teach the engine about")
	require.Contains(t, cause, "WithCallableResolver",
		"and how to teach it")
}

// TestResolverMapsAQualifiedCall is SRD-096 T-6: a host resolver maps the
// (namespace, key) pair onto a registered key, and the call runs.
func TestResolverMapsAQualifiedCall(t *testing.T) {
	var saw atomic.Int64

	const ns = "http://example.com/shared"

	callee := scaleCallee(t, "shared.calc", 2)
	caller := qualifiedCaller(t, "calc", ns, &saw)

	var seen atomic.Int64

	resolver := exec.CallableResolverFunc(
		func(_ context.Context, ref exec.CallableRef) (string, error) {
			seen.Add(1)

			if ref.Namespace == ns {
				return "shared." + ref.Key, nil
			}

			return ref.Key, nil
		})

	st, err := runWith(t,
		[]thresher.Option{thresher.WithCallableResolver(resolver)},
		caller, callee)
	require.NoError(t, err)
	require.Equal(t, thresher.StateCompleted, st)
	require.EqualValues(t, 42, saw.Load(),
		"the child the resolver named ran, and its output crossed back")
	require.EqualValues(t, 1, seen.Load(),
		"the resolver is consulted once per call")
}

// TestResolverErrorFailsTheCall is SRD-096 T-7: a resolver's own error fails
// the call with the cause preserved, not the engine.
func TestResolverErrorFailsTheCall(t *testing.T) {
	var saw atomic.Int64

	callee := scaleCallee(t, "calc", 2)
	caller := qualifiedCaller(t, "calc", "http://example.com/shared", &saw)

	boom := errors.New("directory unreachable")
	resolver := exec.CallableResolverFunc(
		func(_ context.Context, _ exec.CallableRef) (string, error) {
			return "", boom
		})

	cause := runUntilIncident(t,
		[]thresher.Option{thresher.WithCallableResolver(resolver)},
		caller, callee)

	require.Zero(t, saw.Load())
	require.Contains(t, cause, boom.Error(),
		"the host's own reason must survive into the incident — a host that "+
			"cannot see why its resolver failed cannot fix it")
}

// TestUnqualifiedCallIsUnchangedByTheResolver is SRD-096 T-6's other half and
// FR-3's promise: every call that exists today is unqualified, and the default
// resolver must leave it byte-identical.
func TestUnqualifiedCallIsUnchangedByTheResolver(t *testing.T) {
	var saw atomic.Int64

	callee := scaleCallee(t, "calc", 2)
	caller := qualifiedCaller(t, "calc", "", &saw)

	st, err := runWith(t, nil, caller, callee)
	require.NoError(t, err)
	require.Equal(t, thresher.StateCompleted, st)
	require.EqualValues(t, 42, saw.Load())
}

// TestCallableRegisteredAfterTheCaller is SRD-096 T-8, and the promise
// NewCallActivity's doc comment has always made: resolution happens at CALL
// time, so a callable may be registered after the process that calls it.
func TestCallableRegisteredAfterTheCaller(t *testing.T) {
	var saw atomic.Int64

	callee := scaleCallee(t, "calc", 2)
	caller := qualifiedCaller(t, "calc", "", &saw)

	th, err := thresher.New("late-callee")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))

	// The caller registers FIRST, naming a key nothing serves yet.
	_, err = th.RegisterProcess(caller)
	require.NoError(t, err,
		"registration must not resolve the callable — the key names a "+
			"process that may not exist yet, or may be re-versioned later")

	_, err = th.RegisterProcess(callee)
	require.NoError(t, err)

	h, err := th.StartLatest(caller.ID())
	require.NoError(t, err)

	wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
	defer wcancel()
	st, werr := h.WaitCompletion(wctx)
	require.NoError(t, werr)
	require.Equal(t, thresher.StateCompleted, st)
	require.EqualValues(t, 42, saw.Load())

	require.NoError(t, th.Shutdown(context.Background()))
}

// TestResolverMayReEnterTheEngine is SRD-096 T-13 and NFR-1, and it is the
// only test here that is about a LOCK rather than a value.
//
// The resolver is host code: it may consult the engine it is resolving for.
// If InvokeProcess held the registry mutex across the call, this deadlocks and
// the test times out. `make lock-sweep` checks the same rule syntactically,
// which is evidence and not proof — it knows only the names in its PATTERNS
// set. This is the dynamic half.
func TestResolverMayReEnterTheEngine(t *testing.T) {
	var saw atomic.Int64

	callee := scaleCallee(t, "calc", 2)
	caller := qualifiedCaller(t, "calc", "http://example.com/shared", &saw)

	var th *thresher.Thresher

	var reentered atomic.Bool

	resolver := exec.CallableResolverFunc(
		func(_ context.Context, ref exec.CallableRef) (string, error) {
			// Reach back into the engine mid-resolution: both of these take
			// the same lock InvokeProcess would still be holding if the
			// resolution were inside the critical section.
			regs := th.Registrations("calc")
			_, _ = th.Instances(thresher.InstanceQuery{})

			reentered.Store(true)

			if len(regs) == 0 {
				return "", errors.New("no registration for calc")
			}

			return ref.Key, nil
		})

	var err error

	th, err = thresher.New("reentrant-resolver",
		thresher.WithCallableResolver(resolver))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))

	_, err = th.RegisterProcess(callee)
	require.NoError(t, err)
	_, err = th.RegisterProcess(caller)
	require.NoError(t, err)

	h, err := th.StartLatest(caller.ID())
	require.NoError(t, err)

	wctx, wcancel := context.WithTimeout(ctx, 15*time.Second)
	defer wcancel()

	st, werr := h.WaitCompletion(wctx)
	require.NoError(t, werr,
		"a resolver that calls back into the engine must not deadlock — the "+
			"resolution runs outside every engine lock (ADR-023 v.5 §2.7)")
	require.Equal(t, thresher.StateCompleted, st)
	require.True(t, reentered.Load(), "the resolver did re-enter the engine")
	require.EqualValues(t, 42, saw.Load())

	require.NoError(t, th.Shutdown(context.Background()))
}

// TestQualifiedCallNamesBothHalves pins the diagnostic shape: a failure names
// the reference as the host's resolver was handed it, so an operator reading
// the error sees what to map.
func TestQualifiedCallNamesBothHalves(t *testing.T) {
	var saw atomic.Int64

	callee := scaleCallee(t, "calc", 2)
	caller := qualifiedCaller(t, "calc", "http://example.com/shared", &saw)

	cause := runUntilIncident(t, nil, caller, callee)

	for _, want := range []string{"http://example.com/shared", "calc"} {
		require.Truef(t, strings.Contains(cause, want),
			"cause %q must name %q — an operator retries the incident after "+
				"fixing what it names", cause, want)
	}
}

// TestUnqualifiedResolverFailureNamesTheBareKey covers the other half of the
// diagnostic: a host resolver may refuse an UNQUALIFIED reference too — a
// tenant mapping that does not recognise the key, say — and the cause must
// then read as the plain key rather than as an empty namespace glued to it.
func TestUnqualifiedResolverFailureNamesTheBareKey(t *testing.T) {
	var saw atomic.Int64

	callee := scaleCallee(t, "calc", 2)
	caller := qualifiedCaller(t, "calc", "", &saw)

	resolver := exec.CallableResolverFunc(
		func(_ context.Context, ref exec.CallableRef) (string, error) {
			return "", errors.New("no tenant mapping for " + ref.Key)
		})

	cause := runUntilIncident(t,
		[]thresher.Option{thresher.WithCallableResolver(resolver)},
		caller, callee)

	require.Contains(t, cause, `"calc"`,
		"an unqualified reference is named as the bare key")
	require.NotContains(t, cause, "#calc",
		"and never as a namespace-qualified form with the namespace missing")
}

// TestCallFactNamesTheResolvedCallable is SRD-096 T-6's third clause and
// FR-4's audit half.
//
// The document said "audit, in namespace .../shared"; the host's resolver
// answered "shared.audit". An operator asking what actually ran needs the
// SECOND — the reference alone names a registration that may not exist under
// that key at all — and needs the namespace too, so the reference in the file
// can still be recognised. So the fact carries the resolved key, and the
// namespace beside it.
func TestCallFactNamesTheResolvedCallable(t *testing.T) {
	var saw atomic.Int64

	const ns = "http://example.com/shared"

	callee := scaleCallee(t, "shared.calc", 2)
	caller := qualifiedCaller(t, "calc", ns, &saw)

	resolver := exec.CallableResolverFunc(
		func(_ context.Context, ref exec.CallableRef) (string, error) {
			if ref.Namespace == ns {
				return "shared." + ref.Key, nil
			}

			return ref.Key, nil
		})

	th, err := thresher.New("call-fact",
		thresher.WithCallableResolver(resolver))
	require.NoError(t, err)

	fw := &factWatch{}
	sub := th.Observe(fw)

	t.Cleanup(sub.Cancel)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))

	_, err = th.RegisterProcess(callee)
	require.NoError(t, err)
	_, err = th.RegisterProcess(caller)
	require.NoError(t, err)

	h, err := th.StartLatest(caller.ID())
	require.NoError(t, err)

	wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
	defer wcancel()

	_, err = h.WaitCompletion(wctx)
	require.NoError(t, err)

	starts := fw.callStarts()
	require.NotEmpty(t, starts, "a call emits a started fact")

	f := starts[0]
	require.Equal(t, "shared.calc", f.Details[observability.AttrCalledKey],
		"the fact names the RESOLVED key — the registration that ran, not "+
			"the reference the document wrote")
	require.Equal(t, ns, f.Details[observability.AttrCalledNamespace],
		"and the namespace beside it, so the file's own reference is still "+
			"recognisable in the audit")

	require.NoError(t, th.Shutdown(context.Background()))
}

// TestUnqualifiedCallFactCarriesNoNamespace: the attribute is absent, not
// empty, when nothing qualified the reference — an absent attribute reads as
// "unqualified", which is a fact about the file; an empty one reads as a
// value that got dropped.
func TestUnqualifiedCallFactCarriesNoNamespace(t *testing.T) {
	var saw atomic.Int64

	callee := scaleCallee(t, "calc", 2)
	caller := qualifiedCaller(t, "calc", "", &saw)

	th, err := thresher.New("call-fact-plain")
	require.NoError(t, err)

	fw := &factWatch{}
	sub := th.Observe(fw)

	t.Cleanup(sub.Cancel)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))

	_, err = th.RegisterProcess(callee)
	require.NoError(t, err)
	_, err = th.RegisterProcess(caller)
	require.NoError(t, err)

	h, err := th.StartLatest(caller.ID())
	require.NoError(t, err)

	wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
	defer wcancel()

	_, err = h.WaitCompletion(wctx)
	require.NoError(t, err)

	starts := fw.callStarts()
	require.NotEmpty(t, starts)

	_, present := starts[0].Details[observability.AttrCalledNamespace]
	require.False(t, present,
		"an unqualified reference carries no namespace attribute at all")

	require.NoError(t, th.Shutdown(context.Background()))
}
