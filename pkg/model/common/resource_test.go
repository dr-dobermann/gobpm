package common_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/stretchr/testify/require"
)

func TestResource(t *testing.T) {
	t.Run("error_params",
		func(t *testing.T) {
			// no name
			_, err := common.NewResource("")
			require.Error(t, err)

			// no parameters
			_, err = common.NewResource("no_params")
			require.Error(t, err)

			_, err = common.NewResource("no_params", nil)
			require.Error(t, err)
		})

	t.Run("normal",
		func(t *testing.T) {
			rpp := []*common.ResourceParameter{
				common.MustResourceParameter("param_1", "int", true),
				common.MustResourceParameter("param_2", "string", false),
				common.MustResourceParameter("param_3", "bool", true),
			}

			r, err := common.NewResource("resource_test", rpp[0], nil, rpp[1])
			require.NoError(t, err)
			require.Len(t, r.Parameters(), 2)
			pp := r.Parameters()

			for i, p := range pp {
				require.Equal(t, rpp[i].Name(), p.Name())
				require.Equal(t, rpp[i].Type(), p.Type())
				require.Equal(t, rpp[i].IsRequired(), p.IsRequired())
			}
		})
}

func TestResourceParameters(t *testing.T) {
	t.Run("error_params",
		func(t *testing.T) {
			// no name
			_, err := common.NewResourceParameter("", "string", false)
			require.Error(t, err)

			// no type
			_, err = common.NewResourceParameter("param", "", true)
			require.Error(t, err)

			// must with no name
			require.Panics(t,
				func() {
					_ = common.MustResourceParameter("", "int", true)
				})

			// must with no type
			require.Panics(t,
				func() {
					_ = common.MustResourceParameter("invalid_param", "", false)
				})
		})

	t.Run("normal",
		func(t *testing.T) {
			p, err := common.NewResourceParameter("param", "bool", true)
			require.NoError(t, err)

			require.Equal(t, "param", p.Name())
			require.Equal(t, "bool", p.Type())
			require.True(t, p.IsRequired())
		})
}
