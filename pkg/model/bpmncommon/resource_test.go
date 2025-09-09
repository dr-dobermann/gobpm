package bpmncommon_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/stretchr/testify/require"
)

func TestResource(t *testing.T) {
	t.Run("error_params",
		func(t *testing.T) {
			// no name
			_, err := bpmncommon.NewResource("")
			require.Error(t, err)

			// no parameters
			_, err = bpmncommon.NewResource("no_params")
			require.Error(t, err)

			_, err = bpmncommon.NewResource("no_params", nil)
			require.Error(t, err)
		})

	t.Run("normal",
		func(t *testing.T) {
			rpp := []*bpmncommon.ResourceParameter{
				bpmncommon.MustResourceParameter("param_1", "int", true),
				bpmncommon.MustResourceParameter("param_2", "string", false),
				bpmncommon.MustResourceParameter("param_3", "bool", true),
			}

			r, err := bpmncommon.NewResource("resource_test", rpp[0], nil, rpp[1])
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
			_, err := bpmncommon.NewResourceParameter("", "string", false)
			require.Error(t, err)

			// no type
			_, err = bpmncommon.NewResourceParameter("param", "", true)
			require.Error(t, err)

			// must with no name
			require.Panics(t,
				func() {
					_ = bpmncommon.MustResourceParameter("", "int", true)
				})

			// must with no type
			require.Panics(t,
				func() {
					_ = bpmncommon.MustResourceParameter("invalid_param", "", false)
				})
		})

	t.Run("normal",
		func(t *testing.T) {
			p, err := bpmncommon.NewResourceParameter("param", "bool", true)
			require.NoError(t, err)

			require.Equal(t, "param", p.Name())
			require.Equal(t, "bool", p.Type())
			require.True(t, p.IsRequired())
		})
}
