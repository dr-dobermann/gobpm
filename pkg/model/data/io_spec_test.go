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

		params = append(params, p)
	}

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
			require.Error(t, err)
			require.Empty(t, pp)

			// add Parameters
			require.NoError(t, s.AddParameter(params[0], data.DefaultSet))
			require.NoError(t, s.AddParameter(params[1],
				data.OptionalSet|data.WhileExecutionSet))
		})
}
