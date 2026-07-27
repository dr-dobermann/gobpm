package activities_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/hinteraction/consinp"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
)

// TestDehydratable covers SRD-071 FR-1a's static element policies: a UserTask
// releases the instance, a ServiceTask (worker/active work) does not.
func TestDehydratable(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()

	t.Run("UserTask is dehydratable",
		func(t *testing.T) {
			r, err := consinp.NewRenderer(consinp.WithMessager("hi", "hi"))
			require.NoError(t, err)

			ut, err := activities.NewUserTask("u",
				activities.WithRenderer(r),
				activities.WithOutput("x", "string", true),
				activities.WithoutParams())
			require.NoError(t, err)
			require.True(t, ut.Dehydratable(ctx, nil))
		})

	t.Run("ServiceTask is not dehydratable",
		func(t *testing.T) {
			op, err := gooper.New("op",
				func(context.Context, service.DataReader,
					*data.ItemDefinition) (*data.ItemDefinition, error) {
					return nil, nil
				})
			require.NoError(t, err)

			st, err := activities.NewServiceTask("s", op,
				activities.WithoutParams())
			require.NoError(t, err)
			require.False(t, st.Dehydratable(ctx, nil))
		})
}
