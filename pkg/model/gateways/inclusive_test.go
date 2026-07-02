package gateways_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockrenv"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/exec"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	exprengine "github.com/dr-dobermann/gobpm/pkg/model/expression/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// inclusiveRe builds a RuntimeEnvironment mock that resolves "X" to 10 and
// serves the Go expression engine.
func inclusiveRe(t *testing.T) *mockrenv.MockRuntimeEnvironment {
	t.Helper()

	re := mockrenv.NewMockRuntimeEnvironment(t)
	re.EXPECT().
		Find(mock.Anything, "X").
		Return(
			data.MustParameter("X",
				data.MustItemAwareElement(
					data.MustItemDefinition(values.NewVariable(10)),
					data.ReadyDataState)),
			nil).
		Maybe()
	re.EXPECT().ExpressionEngine().Return(exprengine.New()).Maybe()

	return re
}

// boolCond builds a bool-typed condition expression over X (mocked to 10).
func boolCond(t *testing.T, pred func(x int) bool) data.FormalExpression {
	t.Helper()

	c, err := goexpr.New(
		nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			xval, err := ds.Find(ctx, "X")
			if err != nil {
				return nil, err
			}

			x, _ := xval.Value().Get(ctx).(int)

			return values.NewVariable(pred(x)), nil
		})
	require.NoError(t, err)

	return c
}

func TestNewInclusiveGateway(t *testing.T) {
	_, err := gateways.NewInclusiveGateway(events.WithParallel())
	require.Error(t, err)

	_, err = gateways.NewInclusiveGateway(
		foundation.WithID("inclusive-1"),
		options.WithName("my inclusive gateway"),
		gateways.WithDirection(gateways.Diverging))
	require.NoError(t, err)
}

func TestInclusiveSplitSubset(t *testing.T) {
	data.CreateDefaultStates()

	re := inclusiveRe(t)
	xEq10 := boolCond(t, func(x int) bool { return x == 10 })
	xGe5 := boolCond(t, func(x int) bool { return x >= 5 })
	xGt100 := boolCond(t, func(x int) bool { return x > 100 })

	t.Run("multiple true → all true flows forked",
		func(t *testing.T) {
			ig, err := gateways.NewInclusiveGateway()
			require.NoError(t, err)

			nodes := getDummyNodes(4)
			_, err = flow.Link(nodes[0], ig)
			require.NoError(t, err)

			a, err := flow.Link(ig, nodes[1], flow.WithCondition(xEq10))
			require.NoError(t, err)
			b, err := flow.Link(ig, nodes[2], flow.WithCondition(xGe5))
			require.NoError(t, err)
			df, err := flow.Link(ig, nodes[3])
			require.NoError(t, err)
			ig.UpdateDefaultFlow(df)

			flows, err := ig.Exec(context.Background(), re)
			require.NoError(t, err)
			require.Len(t, flows, 2)
			require.Contains(t, flows, a)
			require.Contains(t, flows, b)
			require.NotContains(t, flows, df)
		})

	t.Run("none true → default",
		func(t *testing.T) {
			ig, err := gateways.NewInclusiveGateway()
			require.NoError(t, err)

			nodes := getDummyNodes(3)
			_, err = flow.Link(nodes[0], ig)
			require.NoError(t, err)

			_, err = flow.Link(ig, nodes[1], flow.WithCondition(xGt100))
			require.NoError(t, err)
			df, err := flow.Link(ig, nodes[2])
			require.NoError(t, err)
			ig.UpdateDefaultFlow(df)

			flows, err := ig.Exec(context.Background(), re)
			require.NoError(t, err)
			require.Len(t, flows, 1)
			require.Contains(t, flows, df)
		})

	t.Run("none true and no default → error",
		func(t *testing.T) {
			ig, err := gateways.NewInclusiveGateway()
			require.NoError(t, err)

			nodes := getDummyNodes(3)
			_, err = flow.Link(nodes[0], ig)
			require.NoError(t, err)

			_, err = flow.Link(ig, nodes[1], flow.WithCondition(xGt100))
			require.NoError(t, err)
			_, err = flow.Link(ig, nodes[2], flow.WithCondition(xGt100))
			require.NoError(t, err)

			_, err = ig.Exec(context.Background(), re)
			require.Error(t, err)
		})

	t.Run("converging merge passes through",
		func(t *testing.T) {
			ig, err := gateways.NewInclusiveGateway()
			require.NoError(t, err)

			nodes := getDummyNodes(3)
			_, err = flow.Link(nodes[0], ig)
			require.NoError(t, err)
			_, err = flow.Link(nodes[1], ig)
			require.NoError(t, err)

			out, err := flow.Link(ig, nodes[2])
			require.NoError(t, err)

			flows, err := ig.Exec(context.Background(), re)
			require.NoError(t, err)
			require.Len(t, flows, 1)
			require.Contains(t, flows, out)
		})

	// A conditionless non-default flow is never selected (skipped).
	t.Run("conditionless non-default flow is skipped",
		func(t *testing.T) {
			ig, err := gateways.NewInclusiveGateway()
			require.NoError(t, err)

			nodes := getDummyNodes(4)
			_, err = flow.Link(nodes[0], ig)
			require.NoError(t, err)

			_, err = flow.Link(ig, nodes[1]) // conditionless, non-default
			require.NoError(t, err)
			taken, err := flow.Link(ig, nodes[2], flow.WithCondition(xEq10))
			require.NoError(t, err)
			df, err := flow.Link(ig, nodes[3])
			require.NoError(t, err)
			ig.UpdateDefaultFlow(df)

			flows, err := ig.Exec(context.Background(), re)
			require.NoError(t, err)
			require.Equal(t, []*flow.SequenceFlow{taken}, flows)
		})

	// A condition whose evaluation errors propagates the failure.
	t.Run("condition evaluation error propagates",
		func(t *testing.T) {
			ig, err := gateways.NewInclusiveGateway()
			require.NoError(t, err)

			boom, err := goexpr.New(
				nil,
				data.MustItemDefinition(values.NewVariable(false)),
				func(ctx context.Context, ds data.Source) (data.Value, error) {
					return nil, errs.New(
						errs.M("boom"),
						errs.C("test", errs.OperationFailed))
				})
			require.NoError(t, err)

			nodes := getDummyNodes(3)
			_, err = flow.Link(nodes[0], ig)
			require.NoError(t, err)
			_, err = flow.Link(ig, nodes[1], flow.WithCondition(boom))
			require.NoError(t, err)
			_, err = flow.Link(ig, nodes[2])
			require.NoError(t, err)

			_, err = ig.Exec(context.Background(), re)
			require.Error(t, err)
		})
}

// TestInclusiveIsReachabilityJoin asserts the converging Inclusive gateway is a
// reachability-based synchronizing join (SRD-022 — replaces the SRD-021-era
// "unsupported" assertion).
func TestInclusiveIsReachabilityJoin(t *testing.T) {
	ig, err := gateways.NewInclusiveGateway()
	require.NoError(t, err)

	_, ok := any(ig).(exec.ReachabilityJoin)
	require.True(t, ok,
		"converging InclusiveGateway must be an exec.ReachabilityJoin (SRD-022)")
}

// stubChecker is a hand-written exec.FlowChecker for the OR-join unit tests.
type stubChecker struct {
	reachable []*flow.SequenceFlow
	err       error
}

func (s stubChecker) CheckFlows(
	_ flow.Node, _ []*flow.SequenceFlow,
) ([]*flow.SequenceFlow, error) {
	return s.reachable, s.err
}

// convergingOR builds an InclusiveGateway with n incoming flows and returns it
// with those flows (in order).
func convergingOR(t *testing.T, n int) (*gateways.InclusiveGateway, []*flow.SequenceFlow) {
	t.Helper()

	ig, err := gateways.NewInclusiveGateway()
	require.NoError(t, err)

	for _, src := range getDummyNodes(n) {
		_, err := flow.Link(src, ig)
		require.NoError(t, err)
	}

	return ig, ig.Incoming()
}

func TestORJoinArriveAllMarked(t *testing.T) {
	ig, flows := convergingOR(t, 2)

	complete, merged := ig.Arrive(flows[0].ID(), "t1")
	require.False(t, complete)
	require.Nil(t, merged)

	// the second (last) arrival marks every flow → fire; survivor is the live
	// arriving track (last-in), t1 is merged.
	complete, merged = ig.Arrive(flows[1].ID(), "t2")
	require.True(t, complete)
	require.Equal(t, []string{"t1"}, merged)

	// already fired → a further arrival is a no-op.
	complete, _ = ig.Arrive(flows[0].ID(), "t3")
	require.False(t, complete)
}

func TestORJoinArriveParks(t *testing.T) {
	ig, flows := convergingOR(t, 3)

	complete, merged := ig.Arrive(flows[0].ID(), "t1")
	require.False(t, complete)
	require.Nil(t, merged)

	// a duplicate arrival on the same flow is ignored (still parked).
	complete, _ = ig.Arrive(flows[0].ID(), "t1")
	require.False(t, complete)
}

func TestORJoinRecheckFiresFirstIn(t *testing.T) {
	ig, flows := convergingOR(t, 3)

	ig.Arrive(flows[0].ID(), "t1")
	ig.Arrive(flows[1].ID(), "t2") // 2 of 3 marked → parked

	// the remaining flow is unreachable → fire; survivor is the earliest arrival
	// (first-in, t1), the rest are merged.
	complete, survivor, merged := ig.Recheck(stubChecker{reachable: nil})
	require.True(t, complete)
	require.Equal(t, "t1", survivor)
	require.Equal(t, []string{"t2"}, merged)

	// already fired → a further recheck is a no-op.
	complete, _, _ = ig.Recheck(stubChecker{reachable: nil})
	require.False(t, complete)
}

func TestORJoinRecheckNotComplete(t *testing.T) {
	ig, flows := convergingOR(t, 3)
	ig.Arrive(flows[0].ID(), "t1") // parked; f2, f3 un-marked

	// an un-marked flow is still reachable → not complete.
	complete, _, _ := ig.Recheck(
		stubChecker{reachable: []*flow.SequenceFlow{flows[2]}})
	require.False(t, complete)

	// a reachability error is treated conservatively → not complete.
	complete, _, _ = ig.Recheck(
		stubChecker{err: errs.New(errs.M("boom"), errs.C("test", errs.OperationFailed))})
	require.False(t, complete)

	// no arrival yet (empty order) → not complete.
	idle, _ := convergingOR(t, 2)
	complete, _, _ = idle.Recheck(stubChecker{})
	require.False(t, complete)
}

// TestInclusiveGatewayClone exercises the per-instance Clone + Node accessors.
func TestInclusiveGatewayClone(t *testing.T) {
	ig, err := gateways.NewInclusiveGateway()
	require.NoError(t, err)

	c, err := ig.Clone()
	require.NoError(t, err)
	require.NotNil(t, c)
	require.IsType(t, &gateways.InclusiveGateway{}, c)
	require.NotSame(t, ig, c)
	require.NotNil(t, ig.Node())
}
