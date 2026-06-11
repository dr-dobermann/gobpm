package activities_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockinteractor"
	"github.com/dr-dobermann/gobpm/generated/mockrenv"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
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
			mrenv.EXPECT().InstanceID().Return("mocked_instance").Maybe()
			mrenv.EXPECT().RenderRegistrator().Return(mrr).Once()
			mrenv.EXPECT().AddData(mock.Anything, mock.Anything, mock.Anything).
				RunAndReturn(
					func(ndl scope.NodeDataLoader, dd ...data.Data) error {
						for _, d := range dd {
							t.Log("got data from ", ndl.Name(),
								": name [", d.Name(),
								"] value [", d.Value().Get(context.Background()), "]")
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
					func(rp *bpmncommon.ResourceParameter) bool {
						return rp.Name() == o.name && rp.Type() == o.oType
					}) {
					t.Fatal("no output with name [", o.name, "] of type [", o.oType, "]")
				}
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			// Exec now registers the interactor itself (the former Prologue),
			// then awaits the result channel.
			flows, err := ut.Exec(ctx, mrenv)
			require.NoError(t, err)
			require.Len(t, flows, 0)
		})
}

func TestUserTaskExecErrors(t *testing.T) {
	data.CreateDefaultStates()

	newUT := func(t *testing.T) *activities.UserTask {
		t.Helper()

		r, err := consinp.NewRenderer(consinp.WithMessager("hello", "hello"))
		require.NoError(t, err)

		ut, err := activities.NewUserTask("ut",
			activities.WithRenderer(r),
			activities.WithOutput("name", "string", true),
			activities.WithoutParams())
		require.NoError(t, err)

		return ut
	}

	t.Run("no RenderRegistrator",
		func(t *testing.T) {
			ut := newUT(t)

			mrenv := mockrenv.NewMockRuntimeEnvironment(t)
			mrenv.EXPECT().RenderRegistrator().Return(nil).Once()
			mrenv.EXPECT().InstanceID().Return("ut-err").Maybe()

			_, err := ut.Exec(context.Background(), mrenv)
			require.Error(t, err)
		})

	t.Run("interactor registration failure",
		func(t *testing.T) {
			ut := newUT(t)

			mrr := mockinteractor.NewMockRegistrator(t)
			mrr.EXPECT().Register(ut).Return(nil, errors.New("boom")).Once()

			mrenv := mockrenv.NewMockRuntimeEnvironment(t)
			mrenv.EXPECT().RenderRegistrator().Return(mrr).Once()

			_, err := ut.Exec(context.Background(), mrenv)
			require.Error(t, err)
		})

	t.Run("result data adding failure",
		func(t *testing.T) {
			ut := newUT(t)

			dCh := make(chan data.Data)
			go func() {
				dCh <- data.MustParameter("name",
					data.MustItemAwareElement(
						data.MustItemDefinition(values.NewVariable("John")),
						data.ReadyDataState))
				close(dCh)
			}()

			mrr := mockinteractor.NewMockRegistrator(t)
			mrr.EXPECT().Register(ut).Return(dCh, nil).Once()

			mrenv := mockrenv.NewMockRuntimeEnvironment(t)
			mrenv.EXPECT().RenderRegistrator().Return(mrr).Once()
			mrenv.EXPECT().AddData(mock.Anything, mock.Anything).
				Return(errors.New("boom")).Once()

			_, err := ut.Exec(context.Background(), mrenv)
			require.Error(t, err)
		})
}
