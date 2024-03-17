package data_test

import (
	"strconv"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
)

func TestDataSet(t *testing.T) {
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

	t.Run("empty set",
		func(t *testing.T) {
			s, err := data.NewDataSet("")
			require.Error(t, err)
			require.Empty(t, s)
		})

	t.Run("named set",
		func(t *testing.T) {
			s, err := data.NewDataSet("test_set", foundation.WithId("set_id"))
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

			// remove invalide param
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
		})
}
