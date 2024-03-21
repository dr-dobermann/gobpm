package activities_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/stretchr/testify/require"
)

func TestServiceTask(t *testing.T) {

	op, err := service.NewOperation("my op", nil, nil, nil)
	require.NoError(t, err)

	t.Run("empty args",
		func(t *testing.T) {
			st, err := activities.NewServiceTask("", nil,
				activities.WithoutParams())

			require.Error(t, err)
			require.Empty(t, st)

			st, err = activities.NewServiceTask("test", nil,
				activities.WithoutParams())

			require.Error(t, err)
			require.Empty(t, st)
		})

	t.Run("multyinsatance",
		func(t *testing.T) {
			st, err := activities.NewServiceTask("test", op,
				activities.WithMultyInstance(),
				activities.WithoutParams())
			require.NoError(t, err)
			require.Equal(t, "test", st.Name())
			require.Equal(t, true, st.IsMultyinstance())
			require.Equal(t, "##unspecified", st.Implementation())
		})

	t.Run("invalid options check",
		func(t *testing.T) {
			_, err := activities.NewServiceTask(
				"test",
				op,
				events.WithParallel(),
			)
			require.Error(t, err)
		})
}
