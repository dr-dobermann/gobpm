package data_test

import (
	"strconv"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
)

func TestSet(t *testing.T) {
	rs, err := data.NewDataState("my ready state")
	require.NoError(t, err)

	params := make([]*data.Parameter, 2)
	for i := 0; i < 2; i++ {
		p, err := data.NewParameter("Parameter_"+strconv.Itoa(i+1),
			data.MustItemAwareElement(
				data.MustItemDefinition(
					values.NewVariable(i)),
				rs))
		require.NoError(t, err)

		params[i] = p
	}

	duplParam, err := data.NewParameter("Parameter_1",
		data.MustItemAwareElement(
			data.MustItemDefinition(
				values.NewVariable(100)),
			rs))
	require.NoError(t, err)

	t.Run("empty set",
		func(t *testing.T) {
			s, err := data.NewSet("")
			require.Error(t, err)
			require.Empty(t, s)
			require.Panics(t, func() {
				s := data.MustSet("")
				t.Log(s)
			})
		})

	t.Run("named set",
		func(t *testing.T) {
			s, err := data.NewSet("test_set", foundation.WithId("set_id"))
			require.NoError(t, err)
			require.Equal(t, "test_set", s.Name())
			require.Equal(t, "set_id", s.Id())
			require.False(t, s.IsValid())
			require.NoError(t, s.Validate(rs, false))
			require.True(t, s.IsValid())
			require.Error(t, s.Link(nil))

			pp, err := s.Parameters(data.DefaultSet)
			require.NoError(t, err)
			require.Empty(t, pp)

			pp, err = s.Parameters(42)
			require.Error(t, err)

			// ------------- parameter -------------------------------
			// invalid params
			require.Panics(t, func() {
				p := data.MustParameter("", nil)
				t.Log(p)
			})
			require.Panics(t, func() {
				p := data.MustParameter("test", nil)
				t.Log(p)
			})

			// ----------------- set's parameters --------------------

			// invalid parameters
			require.Error(t, s.AddParameter(nil, data.DefaultSet))
			require.Error(t, s.AddParameter(params[0], 42))

			// normal
			require.NoError(t, s.AddParameter(params[0], data.DefaultSet))

			// add same param
			require.NoError(t, s.AddParameter(params[0], data.DefaultSet))

			// add duplicate param
			require.Error(t, s.AddParameter(duplParam, data.DefaultSet))

			// add param into multiple set types
			require.NoError(t, s.AddParameter(params[1],
				data.OptionalSet|data.WhileExecutionSet))
			t.Log(s.Parameters(data.AllSets))

			defaultParams, err := s.Parameters(data.DefaultSet)
			optionalParams, err := s.Parameters(data.OptionalSet)
			whileExcParams, err := s.Parameters(data.WhileExecutionSet)
			require.NoError(t, err)
			require.Equal(t, "Parameter_1", defaultParams[data.DefaultSet][0].Name())
			require.NoError(t, s.Validate(rs, true))
			require.True(t, s.IsValid())
			require.Equal(t, 1, len(optionalParams))
			require.Equal(t, 1, len(whileExcParams))

			// invalid state validation
			ws, err := data.NewDataState("not ready data state")
			require.NoError(t, err)
			require.Error(t, s.Validate(ws, true))

			// remove invalid param
			require.Error(t, s.RemoveParameter(nil, data.DefaultSet))
			require.Error(t, s.RemoveParameter(params[0], 32))
			require.NoError(t, s.RemoveParameter(params[0], data.OptionalSet))

			// remove param from single set type
			require.NoError(t, s.RemoveParameter(params[0], data.DefaultSet))
			pp, err = s.Parameters(data.DefaultSet)
			require.NoError(t, err)
			require.Equal(t, 0, len(pp[data.DefaultSet]))
			_, ok := pp[data.OptionalSet]
			require.False(t, ok)

			// remove param from multiple set types
			require.NoError(t, s.RemoveParameter(params[1],
				data.OptionalSet|data.WhileExecutionSet))
			pp, err = s.Parameters(data.OptionalSet | data.WhileExecutionSet)
			require.NoError(t, err)
			require.Equal(t, 0, len(pp[data.OptionalSet]))
			require.Equal(t, 0, len(pp[data.WhileExecutionSet]))
			require.NoError(t, s.Validate(rs, true))
			require.True(t, s.IsValid())

			// clear data set
			require.NoError(t, s.AddParameter(params[0], data.DefaultSet))
			pp, err = s.Parameters(data.DefaultSet)
			require.NoError(t, err)
			require.Equal(t, 1, len(pp[data.DefaultSet]))

			require.NoError(t, s.Clear())
			pp, err = s.Parameters(data.DefaultSet)
			require.NoError(t, err)
			require.Equal(t, 0, len(pp[data.DefaultSet]))

			// link data set
			require.Error(t, s.Link(s))
			require.Error(t, s.Link(nil))
			require.Equal(t, 0, len(s.LinkedSets()))

			ls, err := data.NewSet("linked_set")
			require.NoError(t, err)
			require.NoError(t, s.Link(ls))
			require.Equal(t, 1, len(s.LinkedSets()))
			require.Equal(t, "linked_set", s.LinkedSets()[0].Name())

			require.Error(t, s.Unlink(nil))
			require.NoError(t, s.Unlink(ls))
			require.Equal(t, 0, len(s.LinkedSets()))
			require.Error(t, s.Unlink(ls))
		})
}

func TestIOSpec(t *testing.T) {
	sets := []*data.Set{}
	for i := 0; i < 2; i++ {
		s, err := data.NewSet(
			"data_set_" + strconv.Itoa(i+1))
		require.NoError(t, err)
		sets = append(sets, s)
	}

	rs, err := data.NewDataState("my ready state")
	require.NoError(t, err)

	params := make([]*data.Parameter, 2)
	for i := 0; i < 2; i++ {
		p, err := data.NewParameter("Parameter_"+strconv.Itoa(i+1),
			data.MustItemAwareElement(
				data.MustItemDefinition(
					values.NewVariable(i)),
				rs))
		require.NoError(t, err)

		params[i] = p
	}

	ios, err := data.NewIOSpec()
	require.NoError(t, err)
	require.NotEmpty(t, ios)

	// empty parameters list
	pp, err := ios.Parameters(data.Input)
	require.NoError(t, err)
	require.Empty(t, pp)

	// invalid parameters type
	pp, err = ios.Parameters("invalid type")
	require.Error(t, err)
	require.Empty(t, pp)

	// -------------------- parameters ---------------------------------
	// invalid params
	require.Error(t, ios.AddParameter(nil, data.Input))
	require.Error(t, ios.AddParameter(params[0], "wrong param type"))
	require.False(t, ios.HasParameter(nil, data.Input))
	require.False(t, ios.HasParameter(params[0], "invalid_direction"))

	// one type param
	require.NoError(t, ios.AddParameter(params[0], data.Input))
	require.True(t, ios.HasParameter(params[0], data.Input))
	require.False(t, ios.HasParameter(params[0], data.Output))
	pp, err = ios.Parameters(data.Input)
	require.NoError(t, err)
	require.Equal(t, 1, len(pp))
	require.Equal(t, "Parameter_1", pp[0].Name())

	// two type param
	require.NoError(t, ios.AddParameter(params[1], data.Input))
	require.NoError(t, ios.AddParameter(params[1], data.Output))

	// duplicate param
	require.NoError(t, ios.AddParameter(params[0], data.Input))
	pp, err = ios.Parameters(data.Input)
	require.NoError(t, err)
	require.Equal(t, 2, len(pp))
	require.Equal(t, "Parameter_1", pp[0].Name())
	require.Equal(t, "Parameter_2", pp[1].Name())

	// remove param
	require.NoError(t, ios.RemoveParameter(params[0], data.Input))

	// remove non-existing param
	require.Error(t, ios.RemoveParameter(nil, data.Input))
	require.Error(t, ios.RemoveParameter(params[0], data.Input))
	require.Error(t, ios.RemoveParameter(params[0], data.Output))
	pp, err = ios.Parameters(data.Input)
	require.NoError(t, err)
	require.Equal(t, 1, len(pp))
	require.Equal(t, "Parameter_2", pp[0].Name())

	// --------------- data sets ---------------------------------------
	// invalid sets
	require.Error(t, ios.AddSet(nil, data.Input))
	require.Error(t, ios.AddSet(sets[0], "ErrorType"))

	// normal sets
	require.NoError(t, ios.AddSet(sets[0], data.Input))
	require.NoError(t, ios.AddSet(sets[1], data.Output))

	// duplicate set
	require.NoError(t, ios.AddSet(sets[0], data.Input))

	// set in opposite type
	require.Error(t, ios.AddSet(sets[0], data.Output))

	ss, err := ios.Sets(data.Input)
	require.NoError(t, err)
	require.Equal(t, 1, len(ss))
	require.Equal(t, "data_set_1", ss[0].Name())

	require.NoError(t, ss[0].AddParameter(params[1], data.DefaultSet))

	// remove data set
	require.NoError(t, ios.RemoveSet(ss[0], data.Input))

	// remove non-existed data set
	require.Error(t, ios.RemoveSet(ss[0], data.Input))
	require.Error(t, ios.RemoveSet(nil, data.Output))
	require.Error(t, ios.RemoveSet(ss[0], data.Output))

	// ------------------ IOSpecs validation ---------------------------
	require.Error(t, ios.Validate())
	t.Log(ios.Validate())

	ios2, err := data.NewIOSpec()
	require.NoError(t, err)
	require.NotEmpty(t, ios2)

	inpS := data.MustSet("input set")
	require.NoError(t, ios2.AddSet(
		inpS, data.Input))
	outS := data.MustSet("output set")
	require.NoError(t, ios2.AddSet(
		outS, data.Output))
	require.NoError(t, ios2.Validate())

	inpP := data.MustParameter("input",
		data.MustItemAwareElement(
			data.MustItemDefinition(
				values.NewVariable(42)), rs))
	require.NoError(t, ios2.AddParameter(inpP, data.Input))
	require.Error(t, ios2.Validate())
	// parameter in invalid set direction
	require.NoError(t, outS.AddParameter(inpP, data.DefaultSet))
	require.Error(t, ios2.Validate())

	require.NoError(t, outS.RemoveParameter(inpP, data.DefaultSet))

	// right parameter in right set
	require.NoError(t, inpS.AddParameter(inpP, data.DefaultSet))
	require.NoError(t, ios2.Validate())
}
