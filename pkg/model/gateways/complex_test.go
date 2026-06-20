package gateways_test

import (
	"context"
	"errors"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/exec"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/stretchr/testify/require"
)

// errStub is a sentinel error for the GuardEval / FlowChecker stubs.
var errStub = errors.New("stub error")

// guardTrue / guardFalse are exec.GuardEval stubs: they ignore the condition and
// report a fixed result, so the model tests exercise the activation logic without a
// real expression engine (the GuardEval seam is exactly this decoupling).
func guardTrue(_ data.FormalExpression) (bool, error)  { return true, nil }
func guardFalse(_ data.FormalExpression) (bool, error) { return false, nil }

// convergingComplex builds a ComplexGateway with the given activation rule and n
// incoming flows whose ids are inIDs (so WithRequired can reference them).
func convergingComplex(
	t *testing.T, act gateways.ComplexOption, inIDs ...string,
) (*gateways.ComplexGateway, []*flow.SequenceFlow) {
	t.Helper()

	cg, err := gateways.NewComplexGateway(act)
	require.NoError(t, err)

	srcs := getDummyNodes(len(inIDs))
	flows := make([]*flow.SequenceFlow, 0, len(inIDs))

	for i, id := range inIDs {
		f, err := flow.Link(srcs[i], cg, foundation.WithID(id))
		require.NoError(t, err)
		flows = append(flows, f)
	}

	return cg, flows
}

func TestNewTriple(t *testing.T) {
	cond := boolCond(t, func(x int) bool { return x == 10 })

	_, err := gateways.NewTriple(2)
	require.NoError(t, err)

	_, err = gateways.NewTriple(2,
		gateways.WithGuard(cond), gateways.WithRequired("a", "b"))
	require.NoError(t, err)

	_, err = gateways.NewTriple(0) // count < 1
	require.Error(t, err)

	_, err = gateways.NewTriple(1, gateways.WithRequired("a", "b")) // count < required
	require.Error(t, err)

	_, err = gateways.NewTriple(2, gateways.WithGuard(nil)) // nil guard
	require.Error(t, err)

	_, err = gateways.NewTriple(2, gateways.WithRequired("")) // empty id
	require.Error(t, err)

	_, err = gateways.NewTriple(2, gateways.WithRequired()) // no ids
	require.Error(t, err)
}

func TestNewComplexGateway(t *testing.T) {
	_, err := gateways.NewComplexGateway(gateways.WithActivationThreshold(2))
	require.NoError(t, err)

	tr, err := gateways.NewTriple(2)
	require.NoError(t, err)
	_, err = gateways.NewComplexGateway(gateways.WithActivation(tr))
	require.NoError(t, err)

	_, err = gateways.NewComplexGateway() // no activation
	require.Error(t, err)

	_, err = gateways.NewComplexGateway( // mutually exclusive
		gateways.WithActivationThreshold(2), gateways.WithActivation(tr))
	require.Error(t, err)

	_, err = gateways.NewComplexGateway(gateways.WithActivationThreshold(0))
	require.Error(t, err)

	_, err = gateways.NewComplexGateway(gateways.WithActivation()) // empty triples
	require.Error(t, err)

	// threshold after an activation rule is set → mutually-exclusive error.
	_, err = gateways.NewComplexGateway(
		gateways.WithActivation(tr), gateways.WithActivationThreshold(2))
	require.Error(t, err)

	// a failing base option (invalid direction) propagates from New().
	_, err = gateways.NewComplexGateway(
		gateways.WithActivationThreshold(2),
		gateways.WithDirection(gateways.GDirection("bogus")))
	require.Error(t, err)
}

// otherConfig is an options.Configurator that is not a complexConfig.
type otherConfig struct{}

func (otherConfig) Validate() error { return nil }

func TestComplexOptionApplyWrongConfig(t *testing.T) {
	require.Error(t, gateways.WithActivationThreshold(2).Apply(otherConfig{}))
}

func TestComplexIsActivationJoin(t *testing.T) {
	cg, err := gateways.NewComplexGateway(gateways.WithActivationThreshold(1))
	require.NoError(t, err)

	_, ok := any(cg).(exec.ActivationJoin)
	require.True(t, ok, "ComplexGateway must be an exec.ActivationJoin")
}

func TestComplexFiresAtThreshold(t *testing.T) {
	cg, flows := convergingComplex(t,
		gateways.WithActivationThreshold(2), "in0", "in1", "in2")
	reachable := stubChecker{reachable: flows} // non-empty → "more can arrive"

	require.False(t, cg.Record(flows[0].ID(), "t1"))
	d, err := cg.Recheck(guardTrue, reachable)
	require.NoError(t, err)
	require.False(t, d.Fired) // 1 of 2 → wait

	require.False(t, cg.Record(flows[1].ID(), "t2"))
	d, err = cg.Recheck(guardTrue, reachable)
	require.NoError(t, err)
	require.True(t, d.Fired)
	require.Equal(t, "t2", d.Survivor) // last-in
	require.Equal(t, []string{"t1"}, d.Merged)

	// already fired → a further arrival is a trailing token.
	require.True(t, cg.Record(flows[2].ID(), "t3"))
}

func TestComplexGuardGatesFire(t *testing.T) {
	cond := boolCond(t, func(x int) bool { return true })
	tr, err := gateways.NewTriple(2, gateways.WithGuard(cond))
	require.NoError(t, err)

	cg, flows := convergingComplex(t, gateways.WithActivation(tr),
		"in0", "in1", "in2")
	moreToCome := stubChecker{reachable: []*flow.SequenceFlow{flows[2]}}

	require.False(t, cg.Record(flows[0].ID(), "t1"))
	require.False(t, cg.Record(flows[1].ID(), "t2"))

	// count met at 2 but guard false, third still reachable → wait.
	d, err := cg.Recheck(guardFalse, moreToCome)
	require.NoError(t, err)
	require.False(t, d.Fired)
	require.False(t, d.Aborted)

	// the guard turns true → fire.
	d, err = cg.Recheck(guardTrue, moreToCome)
	require.NoError(t, err)
	require.True(t, d.Fired)
	require.Equal(t, "t2", d.Survivor)
}

func TestComplexRequiredFires(t *testing.T) {
	tr, err := gateways.NewTriple(2, gateways.WithRequired("in0"))
	require.NoError(t, err)

	cg, _ := convergingComplex(t, gateways.WithActivation(tr),
		"in0", "in1", "in2")
	all := stubChecker{reachable: nil}

	require.False(t, cg.Record("in1", "t1"))
	require.False(t, cg.Record("in2", "t2"))

	// in1 + in2 reach count 2 but the required in0 is absent; in0 still reachable →
	// wait.
	d, err := cg.Recheck(guardTrue,
		stubChecker{reachable: []*flow.SequenceFlow{cg.Incoming()[0]}})
	require.NoError(t, err)
	require.False(t, d.Fired)
	require.False(t, d.Aborted)

	// in0 arrives → required satisfied, count met → fire.
	require.False(t, cg.Record("in0", "t3"))
	d, err = cg.Recheck(guardTrue, all)
	require.NoError(t, err)
	require.True(t, d.Fired)
	require.Equal(t, "t3", d.Survivor)
}

func TestComplexAbortCountUnreachable(t *testing.T) {
	cg, flows := convergingComplex(t,
		gateways.WithActivationThreshold(3), "in0", "in1", "in2")

	require.False(t, cg.Record(flows[0].ID(), "t1"))

	// one arrival, the rest still reachable → 1 of 3 → wait.
	d, err := cg.Recheck(guardTrue, stubChecker{reachable: flows[1:]})
	require.NoError(t, err)
	require.False(t, d.Fired)
	require.False(t, d.Aborted)

	// a death makes the rest unreachable → 1 + 0 < 3 → abort.
	d, err = cg.Recheck(guardTrue, stubChecker{reachable: nil})
	require.NoError(t, err)
	require.True(t, d.Aborted)
	require.False(t, d.Fired)
}

func TestComplexAbortRequiredUnreachable(t *testing.T) {
	tr, err := gateways.NewTriple(2, gateways.WithRequired("in0"))
	require.NoError(t, err)

	cg, flows := convergingComplex(t, gateways.WithActivation(tr),
		"in0", "in1", "in2")
	in0Reachable := stubChecker{reachable: []*flow.SequenceFlow{flows[0]}}

	require.False(t, cg.Record("in1", "t1"))
	require.False(t, cg.Record("in2", "t2"))

	// count 2 met but the required in0 is absent; in0 still reachable → wait.
	d, err := cg.Recheck(guardTrue, in0Reachable)
	require.NoError(t, err)
	require.False(t, d.Aborted)

	// a death makes in0 unreachable → the required gate can never come → abort.
	d, err = cg.Recheck(guardTrue, stubChecker{reachable: nil})
	require.NoError(t, err)
	require.True(t, d.Aborted)
}

func TestComplexExhaustionNoMatch(t *testing.T) {
	cond := boolCond(t, func(x int) bool { return true })
	tr, err := gateways.NewTriple(2, gateways.WithGuard(cond))
	require.NoError(t, err)

	cg, flows := convergingComplex(t, gateways.WithActivation(tr), "in0", "in1")

	require.False(t, cg.Record(flows[0].ID(), "t1"))

	// in0 in, in1 still reachable → wait.
	d, err := cg.Recheck(guardFalse, stubChecker{reachable: flows[1:]})
	require.NoError(t, err)
	require.False(t, d.Aborted)

	// in1 arrives: count met but the guard is false and nothing more can arrive →
	// exhaustion no-match → abort.
	require.False(t, cg.Record(flows[1].ID(), "t2"))
	d, err = cg.Recheck(guardFalse, stubChecker{reachable: nil})
	require.NoError(t, err)
	require.True(t, d.Aborted)
	require.False(t, d.Fired)
}

func TestComplexRecheckReachabilityError(t *testing.T) {
	cg, flows := convergingComplex(t,
		gateways.WithActivationThreshold(2), "in0", "in1")

	require.False(t, cg.Record(flows[0].ID(), "t1"))

	// a reachability error is treated conservatively: wait (no fire, no abort).
	d, err := cg.Recheck(guardTrue, stubChecker{err: errStub})
	require.NoError(t, err)
	require.False(t, d.Fired)
	require.False(t, d.Aborted)
}

func TestComplexGuardError(t *testing.T) {
	cond := boolCond(t, func(x int) bool { return true })
	tr, err := gateways.NewTriple(1, gateways.WithGuard(cond))
	require.NoError(t, err)

	cg, flows := convergingComplex(t, gateways.WithActivation(tr), "in0", "in1")

	require.False(t, cg.Record(flows[0].ID(), "t1"))

	// count met at 1, so the guard is evaluated — its error propagates.
	_, err = cg.Recheck(
		func(_ data.FormalExpression) (bool, error) { return false, errStub },
		stubChecker{reachable: flows[1:]})
	require.Error(t, err)
}

func TestComplexRecheckNoArrivals(t *testing.T) {
	cg, _ := convergingComplex(t,
		gateways.WithActivationThreshold(2), "in0", "in1")

	// no arrival yet → Recheck is a no-op (neither fire nor abort).
	d, err := cg.Recheck(guardTrue, stubChecker{reachable: nil})
	require.NoError(t, err)
	require.False(t, d.Fired)
	require.False(t, d.Aborted)
}

func TestComplexGatewayClone(t *testing.T) {
	cg, err := gateways.NewComplexGateway(gateways.WithActivationThreshold(2))
	require.NoError(t, err)

	c := cg.Clone()
	require.NotNil(t, c)
	require.IsType(t, &gateways.ComplexGateway{}, c)
	require.NotSame(t, cg, c)
	require.NotNil(t, cg.Node())
}

func TestComplexValidate(t *testing.T) {
	good, err := gateways.NewTriple(2, gateways.WithRequired("in0"))
	require.NoError(t, err)
	cg, _ := convergingComplex(t, gateways.WithActivation(good),
		"in0", "in1", "in2")
	require.NoError(t, cg.Validate())

	// count > M (incoming = 2).
	cgBig, _ := convergingComplex(t, gateways.WithActivationThreshold(5),
		"in0", "in1")
	require.Error(t, cgBig.Validate())

	// required id is not an incoming flow.
	bad, err := gateways.NewTriple(2, gateways.WithRequired("nope"))
	require.NoError(t, err)
	cgBad, _ := convergingComplex(t, gateways.WithActivation(bad),
		"in0", "in1", "in2")
	require.Error(t, cgBad.Validate())
}

func TestComplexSplitSubset(t *testing.T) {
	_ = data.CreateDefaultStates()

	re := inclusiveRe(t)
	xEq10 := boolCond(t, func(x int) bool { return x == 10 })
	xGt100 := boolCond(t, func(x int) bool { return x > 100 })

	cg, err := gateways.NewComplexGateway(gateways.WithActivationThreshold(1))
	require.NoError(t, err)

	nodes := getDummyNodes(4)
	_, err = flow.Link(nodes[0], cg)
	require.NoError(t, err)

	a, err := flow.Link(cg, nodes[1], flow.WithCondition(xEq10))
	require.NoError(t, err)
	_, err = flow.Link(cg, nodes[2], flow.WithCondition(xGt100))
	require.NoError(t, err)
	df, err := flow.Link(cg, nodes[3])
	require.NoError(t, err)
	require.NoError(t, cg.UpdateDefaultFlow(df))

	flows, err := cg.Exec(context.Background(), re)
	require.NoError(t, err)
	require.Equal(t, []*flow.SequenceFlow{a}, flows) // only the true subset
}
