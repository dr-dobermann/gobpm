package gooper_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/stretchr/testify/require"
)

func TestGofunc(t *testing.T) {
	mult := func(multiplicator int) gooper.OpFunctor {
		f := func(in *data.ItemDefinition) (*data.ItemDefinition, error) {
			if in == nil {
				return nil,
					errs.New(
						errs.M("empty input parameter"),
						errs.C(errs.EmptyNotAllowed))
			}

			v, ok := in.Structure().Get().(int)
			if !ok {
				return nil,
					errs.New(
						errs.M("expected int but get %s",
							in.Structure().Type()),
						errs.C(errs.TypeCastingError))
			}

			return data.NewItemDefinition(
				values.NewVariable(v * multiplicator))
		}

		return gooper.OpFunctor(f)
	}

	multErr := []string{
		errs.TypeCastingError,
	}

	in := data.MustItemDefinition(values.NewVariable(100))

	t.Run("empty func",
		func(t *testing.T) {
			_, err := gooper.New(nil)
			require.Error(t, err)
		})

	t.Run("normal",
		func(t *testing.T) {
			gf, err := gooper.New(mult(2), multErr...)
			require.NoError(t, err)
			require.Equal(t, gooper.GoOperType, gf.Type())
			require.Contains(t, gf.ErrorClasses(), errs.TypeCastingError)

			out, err := gf.Execute(context.Background(), in)
			require.NoError(t, err)
			require.NotEmpty(t, out)
			require.Equal(t, 200, out.Structure().Get())
		})

	t.Run("failed func",
		func(t *testing.T) {
			gf, err := gooper.New(mult(10), multErr...)
			require.NoError(t, err)

			_, err = gf.Execute(
				context.Background(),
				data.MustItemDefinition(values.NewVariable("invalid int")))
			require.Error(t, err)
		})
}
