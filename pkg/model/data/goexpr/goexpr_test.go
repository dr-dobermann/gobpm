package goexpr_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockdata"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/stretchr/testify/require"
)

func CheckPositive(ctx context.Context, ds data.Source) (data.Value, error) {
	if ds == nil {
		return nil, fmt.Errorf("data source is empty")
	}
	xv, err := ds.Find(ctx, "x")
	if err != nil {
		return nil, fmt.Errorf("couldn't find x value: %w", err)
	}

	bx, ok := xv.Value().Get().(int)
	if !ok {
		return nil,
			fmt.Errorf("x isn't a int type (have %q)",
				reflect.TypeOf(xv).String())
	}

	return values.NewVariable(bx >= 0), nil
}

func TestGoBpmExpression(t *testing.T) {
	dsm := mockdata.NewMockSource(t)

	ctx := context.Background()

	data.CreateDefaultStates()

	dsm.EXPECT().
		Find(ctx, "x").
		Return(
			data.MustParameter("x value",
				data.MustItemAwareElement(
					data.MustItemDefinition(
						values.NewVariable(10)),
					data.ReadyDataState),
			),
			nil).Once()

	ge := goexpr.Must(
		dsm,
		data.MustItemDefinition(values.NewVariable(false)),
		CheckPositive,
		foundation.WithId("my first expression"),
		foundation.WithDoc("x >= 0", foundation.PlainText))

	require.Equal(t, "gobpm:goexpr", ge.Language())

	require.Equal(t, "my first expression", ge.Id())
	t.Log(" >> have an GoExpression: ", ge.Docs()[0])

	require.False(t, ge.IsEvaluated())
	require.Equal(t, "bool", ge.ResultType())
	_, err := ge.Result()
	require.Error(t, err)

	res, err := ge.Evaluate(ctx, dsm)
	require.NoError(t, err)
	require.True(t, res.Get().(bool))

	rr, err := ge.Result()
	require.NoError(t, err)
	require.True(t, rr.Get().(bool))
}

func TestGoBpmExprErrors(t *testing.T) {
	// not created properly Ge
	invalidGe := goexpr.GExpression{}

	_, err := invalidGe.Evaluate(context.Background(), nil)
	require.Error(t, err)

	ctx := context.Background()

	// init data default state
	data.CreateDefaultStates()

	// invalid data.Source mock
	iDsm := mockdata.NewMockSource(t)

	iDsm.EXPECT().
		Find(ctx, "x").
		Return(
			nil,
			fmt.Errorf("couldn't find x"),
		).Maybe()

	// invalid option
	_, err = goexpr.New(
		iDsm,
		data.MustItemDefinition(values.NewVariable("test")),
		CheckPositive,
		options.WithName("my expression"),
		foundation.WithDoc("x >= 0", foundation.PlainText))
	require.Error(t, err)

	// nil params
	_, err = goexpr.New(
		iDsm,
		nil,
		CheckPositive,
		foundation.WithId("my first expression"),
		foundation.WithDoc("x >= 0", foundation.PlainText))
	require.Error(t, err)

	_, err = goexpr.New(
		iDsm,
		data.MustItemDefinition(values.NewVariable("test")),
		nil,
		foundation.WithId("my first expression"),
		foundation.WithDoc("x >= 0", foundation.PlainText))
	require.Error(t, err)

	require.Panics(t,
		func() {
			_ = goexpr.Must(iDsm, nil, CheckPositive)
		})

	// invalid data source value type
	invDs, err := goexpr.New(
		iDsm,
		data.MustItemDefinition(values.NewVariable("test")),
		CheckPositive,
		foundation.WithId("invalid ds"),
		foundation.WithDoc("x >= 0", foundation.PlainText))
	require.NoError(t, err)
	_, err = invDs.Evaluate(ctx, iDsm)
	require.Error(t, err)

	// empty data source on evaluation
	emptyDs, err := goexpr.New(
		nil,
		data.MustItemDefinition(values.NewVariable("test")),
		CheckPositive,
		foundation.WithId("invalid ds"),
		foundation.WithDoc("x >= 0", foundation.PlainText))
	require.NoError(t, err)
	_, err = emptyDs.Evaluate(ctx, nil)
	require.Error(t, err)

	// data.Source mock
	dsm := mockdata.NewMockSource(t)

	dsm.EXPECT().
		Find(ctx, "x").
		Return(
			data.MustParameter("x value",
				data.MustItemAwareElement(
					data.MustItemDefinition(
						values.NewVariable(10)),
					data.ReadyDataState),
			),
			nil).Maybe()

	// wrong Result type
	wrongResTypeGe, err := goexpr.New(
		dsm,
		data.MustItemDefinition(values.NewVariable("test")),
		CheckPositive,
		foundation.WithId("my first expression"),
		foundation.WithDoc("x >= 0", foundation.PlainText))
	require.NoError(t, err)

	_, err = wrongResTypeGe.Evaluate(ctx, nil)
	require.Error(t, err)
}
