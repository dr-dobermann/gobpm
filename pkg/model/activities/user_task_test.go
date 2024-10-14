package activities_test

import (
	"bytes"
	"context"
	"slices"
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockinteractor"
	"github.com/dr-dobermann/gobpm/generated/mockrenv"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/hinteraction/consinp"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNewUserTask(t *testing.T) {
	t.Run("invalid parameters",
		func(t *testing.T) {
			// empty name
			_, err := activities.NewUserTask("")
			require.Error(t, err)

			// empty renderer
			_, err = activities.NewUserTask("no renderer")
			require.Error(t, err)
			_, err = activities.NewUserTask("invalid",
				activities.WithRenderer(nil))
			require.Error(t, err)

			// duplicate renderers
			r, err := consinp.NewRenderer(
				consinp.WithMessager("Hello world", "Hello world"))
			require.NoError(t, err)
			_, err = activities.NewUserTask("invalid",
				activities.WithRenderer(r),
				activities.WithRenderer(r))
			require.Error(t, err)
		})

	t.Run("normal",
		func(t *testing.T) {
			src := bytes.NewBufferString("John\n52\n")

			r, err := consinp.NewRenderer(
				consinp.WithStringInput("name", "Enter your name"),
				consinp.WithIntInput("age", "Enter your age"),
				consinp.WithSource(src))
			require.NoError(t, err)

			ut, err := activities.NewUserTask("Enter user info",
				activities.WithRenderer(r),
				activities.WithOutput("name", "string", true),
				activities.WithOutput("age", "int", true),
				activities.WithoutParams())
			require.NoError(t, err)

			mrr := mockinteractor.NewMockRegistrator(t)
			// create data channel for mocked render registrator
			dCh := make(chan data.Data)
			go func() {
				for _, p := range []data.Data{
					data.MustParameter("name",
						data.MustItemAwareElement(
							data.MustItemDefinition(
								values.NewVariable("John Doe")),
							data.ReadyDataState)),
					data.MustParameter("age",
						data.MustItemAwareElement(
							data.MustItemDefinition(
								values.NewVariable(27)),
							data.ReadyDataState)),
				} {
					dCh <- p
				}

				close(dCh)
			}()

			mrr.EXPECT().
				Register(ut).
				Return(dCh, nil).
				Once()

			mrenv := mockrenv.NewMockRuntimeEnvironment(t)
			mrenv.EXPECT().InstanceId().Return("mocked_instance").Maybe()
			mrenv.EXPECT().RenderRegistrator().Return(mrr).Once()
			mrenv.EXPECT().AddData(mock.Anything, mock.Anything, mock.Anything).
				RunAndReturn(
					func(ndl scope.NodeDataLoader, dd ...data.Data) error {
						for _, d := range dd {
							t.Log("got data from ", ndl.Name(),
								": name [", d.Name(),
								"] value [", d.Value().Get(), "]")
						}

						return nil
					}).Maybe()

			// implementations description
			impls := ut.Implementation()
			require.Len(t, impls, 1)
			require.Contains(t, impls, consinp.ConsInpRender)

			// renderers
			rr := ut.Renderers()
			require.Len(t, rr, 1)

			// outputs
			oo := ut.Outputs()
			require.Len(t, oo, 2)
			for _, o := range []struct{ name, oType string }{
				{"name", "string"},
				{"age", "int"},
			} {
				if !slices.ContainsFunc(oo,
					func(rp *common.ResourceParameter) bool {
						return rp.Name() == o.name && rp.Type() == o.oType
					}) {
					t.Fatal("no output with name [", o.name, "] of type [", o.oType, "]")
				}
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			err = ut.Prologue(ctx, mrenv)
			require.NoError(t, err)

			flows, err := ut.Exec(ctx, mrenv)
			require.NoError(t, err)
			require.Len(t, flows, 0)
		})
}
