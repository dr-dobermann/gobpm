package activities

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockscope"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	dataobjects "github.com/dr-dobermann/gobpm/pkg/model/data_objects"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestOptions(t *testing.T) {
	t.Run("no name",
		func(t *testing.T) {
			_, err := newTask("")
			require.Error(t, err)
		})

	t.Run("invalid options",
		func(t *testing.T) {
			_, err := newTask("invalid_options",
				options.WithName("error"),
				WithoutParams())
			require.Error(t, err)
		})

	t.Run("normal",
		func(t *testing.T) {
			task, err := newTask("no options",
				WithoutParams())
			require.NoError(t, err)

			require.False(t, task.IsMultyinstance())

			mtask, err := newTask("multyinstance",
				WithMultyInstance(),
				WithoutParams())
			require.NoError(t, err)

			require.True(t, mtask.IsMultyinstance())
			require.Equal(t, flow.TaskActivity, mtask.ActivityType())
		})
}

func TestTaskData(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	t.Run("properties",
		func(t *testing.T) {
			type User struct {
				Name string
				Age  uint
			}

			props := []*data.Property{
				data.MustProperty(
					"x",
					data.MustItemDefinition(
						values.NewVariable(42)),
					data.ReadyDataState),
				data.MustProperty(
					"user",
					data.MustItemDefinition(
						values.NewVariable(User{
							Name: "Jon Doe",
							Age:  27,
						})),
					data.ReadyDataState),
			}

			task, err := newTask(
				"test",
				data.WithProperties(props...),
				WithSet("input set", "",
					data.Input, data.DefaultSet,
					[]*data.Parameter{
						data.MustParameter("y param",
							data.MustItemAwareElement(
								data.MustItemDefinition(
									values.NewVariable(100.500),
									foundation.WithId("y")),
								data.ReadyDataState)),
					}),
				WithSet("output set", "",
					data.Output, data.DefaultSet,
					[]*data.Parameter{
						data.MustParameter(
							"y param",
							data.MustItemAwareElement(
								data.MustItemDefinition(
									values.NewVariable(0.0),
									foundation.WithId("y")),
								nil)),
					}))
			require.NoError(t, err)

			// set task data path
			dp, err := scope.NewDataPath("/task")
			require.NoError(t, err)

			s := mockscope.NewMockScope(t)
			s.On("LoadData", task,
				mock.AnythingOfType("*data.Property"),
				mock.AnythingOfType("*data.Property"),
				mock.AnythingOfType("*data.Parameter")).
				Return(
					func(ndl scope.NodeDataLoader, dd ...data.Data) error {
						for _, d := range dd {
							t.Log("   >> got data: ", d.Name(), " = ",
								d.Value().Get(context.Background()))

							switch dv := d.(type) {
							case *data.Property:
								if idx := slices.IndexFunc(
									props,
									func(prop *data.Property) bool {
										return dv.Id() == prop.Id()
									}); idx == -1 {
									return fmt.Errorf("couldn't find property %q", d.Name())
								}
							case *data.Parameter:
								if dv.Name() != "y param" {
									return fmt.Errorf("invalid parameter name")
								}
							default:
								return fmt.Errorf("TEST: invalid type of data: %s",
									reflect.TypeOf(d).String())
							}

						}

						return nil
					})
			s.EXPECT().
				GetDataById(dp, "y").
				Return(data.MustItemAwareElement(
					data.MustItemDefinition(
						values.NewVariable(23.02),
						foundation.WithId("y")),
					data.ReadyDataState), nil)

			err = task.RegisterData(dp, s)
			require.NoError(t, err)

			// add association to DataObject
			inpDO, err := dataobjects.New(
				"input data object",
				data.MustItemDefinition(
					values.NewVariable(23.02),
					foundation.WithId("y")),
				data.ReadyDataState)
			require.NoError(t, err)
			require.NoError(t, inpDO.AssociateTarget(task, nil))

			outDO, err := dataobjects.New(
				"output data object",
				data.MustItemDefinition(
					values.NewVariable(11.09),
					foundation.WithId("y")),
				nil)
			require.NoError(t, err)
			require.NoError(t, outDO.AssociateSource(task, []string{"y"}, nil))

			require.NoError(t, task.LoadData(context.Background()))

			ctx := context.Background()

			// check input parameters
			inParams, err := task.IoSpec.Parameters(data.Input)
			require.NoError(t, err)
			require.Len(t, inParams, 1)
			require.Equal(t, 23.02, inParams[0].Subject().Structure().Get(ctx))

			require.NoError(t, task.UploadData(ctx, s))
			require.Equal(t, 23.02, outDO.Subject().Structure().Get(ctx))
		})

	t.Run("data associations",
		func(t *testing.T) {
			input := data.MustItemAwareElement(
				data.MustItemDefinition(
					values.NewVariable(42),
					foundation.WithId("x")),
				nil)

			output := data.MustItemAwareElement(
				data.MustItemDefinition(
					values.NewVariable(84),
					foundation.WithId("x")),
				data.ReadyDataState)

			task, err := newTask(
				"Task 1",
				WithSet(
					"inputs",
					"input_set_id",
					data.Input,
					data.DefaultSet,
					[]*data.Parameter{
						data.MustParameter(
							"x",
							input),
					}),
				WithSet(
					"outputs",
					"output_set_id",
					data.Output,
					data.DefaultSet,
					[]*data.Parameter{
						data.MustParameter(
							"x",
							output),
					}))
			require.NoError(t, err)

			// check input binding
			ia, err := data.NewAssociation(
				input,
				data.WithSource(
					data.MustItemAwareElement(
						data.MustItemDefinition(
							values.NewVariable(100),
							foundation.WithId("x")),
						data.ReadyDataState)))
			require.NoError(t, err)

			err = task.BindIncoming(ia)
			require.NoError(t, err)

			ctx := context.Background()

			v, err := ia.Value(context.Background())
			require.NoError(t, err)
			require.Equal(t, 100, v.Structure().Get(ctx))

			require.NoError(t, err)

			// check inputs
			ipp := task.Inputs()
			require.Len(t, ipp, 1)
			require.True(t,
				slices.ContainsFunc(
					ipp,
					func(iae *data.ItemAwareElement) bool {
						return iae.ItemDefinition().Id() == "x"
					}))

			require.Equal(t, 100, ipp[0].ItemDefinition().Structure().Get(ctx))

			// check output binding
			outRes := data.MustItemAwareElement(
				data.MustItemDefinition(
					values.NewVariable(0),
					foundation.WithId("x")),
				nil)

			oa, err := data.NewAssociation(
				outRes,
				data.WithSource(output))
			require.NoError(t, err)

			err = task.BindOutgoing(oa)
			require.NoError(t, err)

			vo, err := oa.Value(ctx)
			require.NoError(t, err)
			require.Equal(t, 84, vo.Structure().Get(ctx))

			// check outputs
			opp := task.Outputs()
			require.Len(t, opp, 1)

			require.True(t,
				slices.ContainsFunc(
					opp,
					func(iae *data.ItemAwareElement) bool {
						return iae.ItemDefinition().Id() == "x"
					}))

			require.Equal(t, 84, opp[0].ItemDefinition().Structure().Get(ctx))
		})
}
