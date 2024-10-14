package gateways_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockrenv"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/stretchr/testify/require"
)

func TestNewExclGateway(t *testing.T) {
	// invalid options
	_, err := gateways.NewExclusiveGateway(events.WithParallel())
	require.Error(t, err)

	// valid options
	_, err = gateways.NewExclusiveGateway(
		foundation.WithId("exclusive gateway #1"),
		foundation.WithDoc("this gateway does nothing", foundation.PlainText),
		options.WithName("my first exclusive gateway"),
		gateways.WithDirection(gateways.Mixed))
	require.NoError(t, err)
}

func TestExclusiveGatewayExec(t *testing.T) {
	data.CreateDefaultStates()

	re := mockrenv.NewMockRuntimeEnvironment(t)
	re.EXPECT().
		Root().
		Return(scope.DataPath("/")).
		Maybe()
	re.EXPECT().
		GetData(scope.DataPath("/"), "X").
		Return(
			data.MustParameter(
				"X",
				data.MustItemAwareElement(
					data.MustItemDefinition(values.NewVariable(10)),
					data.ReadyDataState)),
			nil).
		Maybe()

	xless10, err := goexpr.New(
		nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			xval, err := ds.Find(ctx, "X")
			if err != nil {
				return nil, err
			}

			x, ok := xval.Value().Get().(int)
			if !ok {
				return nil,
					errs.New(
						errs.M("x isn't int"),
						errs.C("executiveGatewayTestErrors", errs.TypeCastingError),
						errs.D("x_type", reflect.TypeOf(xval).String()))
			}

			return values.NewVariable(x < 10), nil
		})
	require.NoError(t, err)

	xequal10, err := goexpr.New(
		nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			xval, err := ds.Find(ctx, "X")
			if err != nil {
				return nil, err
			}

			x, ok := xval.Value().Get().(int)
			if !ok {
				return nil,
					errs.New(
						errs.M("x isn't int"),
						errs.C("executiveGatewayTestErrors", errs.TypeCastingError),
						errs.D("x_type", reflect.TypeOf(xval).String()))
			}

			return values.NewVariable(x == 10), nil
		})
	require.NoError(t, err)

	t.Run("no outgoing flows",
		func(t *testing.T) {
			eg, err := gateways.NewExclusiveGateway()
			require.NoError(t, err)

			nodes := getDummyNodes(3)
			_, err = flow.Link(nodes[0], eg)
			require.NoError(t, err)

			_, err = flow.Link(eg, nodes[1],
				flow.WithCondition(xless10))
			require.NoError(t, err)

			_, err = flow.Link(eg, nodes[2])
			require.NoError(t, err)

			_, err = eg.Exec(context.Background(), re)
			t.Log(err)
			require.Error(t, err)
		})

	t.Run("no outgoing flows with default",
		func(t *testing.T) {
			eg, err := gateways.NewExclusiveGateway()
			require.NoError(t, err)

			nodes := getDummyNodes(3)
			_, err = flow.Link(nodes[0], eg)
			require.NoError(t, err)

			_, err = flow.Link(eg, nodes[1],
				flow.WithCondition(xless10))
			require.NoError(t, err)

			df, err := flow.Link(eg, nodes[2])
			require.NoError(t, err)

			eg.UpdateDefaultFlow(df)

			flows, err := eg.Exec(context.Background(), re)
			require.NoError(t, err)
			require.Len(t, flows, 1)
			require.Contains(t, flows, df)
		})

	t.Run("normal outgoing flow",
		func(t *testing.T) {
			eg, err := gateways.NewExclusiveGateway()
			require.NoError(t, err)

			nodes := getDummyNodes(3)
			_, err = flow.Link(nodes[0], eg)
			require.NoError(t, err)

			nf, err := flow.Link(eg, nodes[1],
				flow.WithCondition(xequal10))
			require.NoError(t, err)

			df, err := flow.Link(eg, nodes[2])
			require.NoError(t, err)

			eg.UpdateDefaultFlow(df)

			flows, err := eg.Exec(context.Background(), re)
			require.NoError(t, err)
			require.Len(t, flows, 1)
			require.Contains(t, flows, nf)
		})
}
