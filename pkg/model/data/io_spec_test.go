package data_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/stretchr/testify/require"
)

// newParam builds a ready Parameter for the given value, applying any parameter
// options (Optional/WhileExecuting).
func newParam(
	t *testing.T,
	name string,
	v any,
	opts ...data.ParameterOption,
) *data.Parameter {
	t.Helper()

	rs, err := data.NewSrcState("ready")
	require.NoError(t, err)

	p, err := data.NewParameter(name,
		data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable(v)), rs),
		opts...)
	require.NoError(t, err)

	return p
}

func TestParameter(t *testing.T) {
	rs, err := data.NewSrcState("ready")
	require.NoError(t, err)

	iae := data.MustItemAwareElement(
		data.MustItemDefinition(values.NewVariable(1)), rs)

	t.Run("invalid construction",
		func(t *testing.T) {
			_, err := data.NewParameter("", iae)
			require.Error(t, err)

			_, err = data.NewParameter("p", nil)
			require.Error(t, err)

			require.Panics(t, func() { _ = data.MustParameter("", nil) })
		})

	t.Run("required by default",
		func(t *testing.T) {
			p, err := data.NewParameter("p", iae)
			require.NoError(t, err)
			require.Equal(t, "p", p.Name())
			require.False(t, p.IsOptional())
			require.False(t, p.IsWhileExecuting())
		})

	t.Run("flagged optional and while-executing",
		func(t *testing.T) {
			p := data.MustParameter("p", iae,
				data.Optional(), data.WhileExecuting())
			require.True(t, p.IsOptional())
			require.True(t, p.IsWhileExecuting())
		})
}

func TestIOSpec(t *testing.T) {
	in0 := newParam(t, "in_0", 0)
	in1 := newParam(t, "in_1", 1, data.Optional())
	out0 := newParam(t, "out_0", "")

	ios, err := data.NewIOSpec()
	require.NoError(t, err)
	require.NotEmpty(t, ios)

	// empty parameter lists and the set views
	pp, err := ios.Parameters(data.Input)
	require.NoError(t, err)
	require.Empty(t, pp)
	require.Empty(t, ios.InputSet())
	require.Empty(t, ios.OutputSet())

	// invalid direction
	_, err = ios.Parameters("invalid type")
	require.Error(t, err)

	// invalid params
	require.Error(t, ios.AddParameter(nil, data.Input))
	require.Error(t, ios.AddParameter(in0, "wrong direction"))
	require.False(t, ios.HasParameter(nil, data.Input))
	require.False(t, ios.HasParameter(in0, "invalid_direction"))

	// add params
	require.NoError(t, ios.AddParameter(in0, data.Input))
	require.NoError(t, ios.AddParameter(in1, data.Input))
	require.NoError(t, ios.AddParameter(out0, data.Output))
	require.True(t, ios.HasParameter(in0, data.Input))
	require.False(t, ios.HasParameter(in0, data.Output))

	// adding the same parameter again is idempotent
	require.NoError(t, ios.AddParameter(in0, data.Input))

	// the views reflect the params in order; the per-parameter flags survive
	is := ios.InputSet()
	require.Len(t, is, 2)
	require.Equal(t, "in_0", is[0].Name())
	require.False(t, is[0].IsOptional())
	require.Equal(t, "in_1", is[1].Name())
	require.True(t, is[1].IsOptional())
	require.Len(t, ios.OutputSet(), 1)

	// a well-formed spec validates
	require.NoError(t, ios.Validate())

	// remove a param
	require.NoError(t, ios.RemoveParameter(in0, data.Input))
	require.Error(t, ios.RemoveParameter(nil, data.Input))
	require.Error(t, ios.RemoveParameter(in0, data.Input)) // already gone
	require.Len(t, ios.InputSet(), 1)
	require.Equal(t, "in_1", ios.InputSet()[0].Name())
}

// TestRequiredItemIDs covers the gating-set helper: only parameters that are
// neither optional nor while-executing gate the start/completion.
func TestRequiredItemIDs(t *testing.T) {
	req := newParam(t, "req", 0)
	opt := newParam(t, "opt", 0, data.Optional())
	we := newParam(t, "we", 0, data.WhileExecuting())

	ids := data.RequiredItemIDs([]*data.Parameter{req, opt, we})

	require.Len(t, ids, 1)
	require.True(t, ids[req.ItemDefinition().ID()])
	require.False(t, ids[opt.ItemDefinition().ID()])
	require.False(t, ids[we.ItemDefinition().ID()])
}

// TestIOSpecValidateDuplicateName covers the structural Validate: two
// parameters sharing a name in one direction is an error; a well-formed spec
// passes.
func TestIOSpecValidateDuplicateName(t *testing.T) {
	ios, err := data.NewIOSpec()
	require.NoError(t, err)

	require.NoError(t, ios.AddParameter(newParam(t, "same", 1), data.Input))
	require.NoError(t, ios.AddParameter(newParam(t, "same", 2), data.Input))

	require.Error(t, ios.Validate())
	t.Log(ios.Validate())
}
