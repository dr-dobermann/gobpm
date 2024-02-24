package values_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/stretchr/testify/require"
)

func TestArray(t *testing.T) {
	t.Run("empty array",
		func(t *testing.T) {
			a := values.NewArray[int]()

			require.NotEmpty(t, a)

			require.Equal(t, "int", a.Type())
			require.Equal(t, -1, a.Index())
			require.Equal(t, 0, a.Count())

			require.Error(t, a.Delete(0))
			require.Error(t, a.GoTo(0))
			require.Error(t, a.Update(5))
			require.Error(t, a.Insert(2, 0))
			require.Error(t, a.Next(data.StepForward))
		})

	t.Run("normal array",
		func(t *testing.T) {
			a := values.NewArray[int](1, 2, 3, 4, 5)

			require.NotEmpty(t, a)

			require.Equal(t, 5, a.Count())
			require.Equal(t, 0, a.Index())
			require.Equal(t, 1, a.Get())

			// get at normal index
			v, err := a.GetAt(1)
			require.NoError(t, err)
			require.Equal(t, 2, v)

			// get at index out of range
			v, err = a.GetAt(6)
			require.Error(t, err)

			// add value
			require.NoError(t, a.Add(6))
			require.Equal(t, 6, a.Count())
			require.NoError(t, a.GoTo(5))
			require.Equal(t, 6, a.Get())

			require.Error(t, a.Add("six"))

			// delete value
			require.NoError(t, a.Delete(4))
			require.Equal(t, 5, a.Count())
			require.Equal(t, 4, a.Index())
			require.Error(t, a.Delete(7))
			require.Equal(t, 6, a.Get())
		})
}

func TestVariable(t *testing.T) {
	t.Run("int",
		func(t *testing.T) {
			v := values.NewVariable[int](42)

			// check value
			require.Equal(t, 42, v.Get())
			require.Equal(t, 42, v.GetT())

			// update value
			require.NoError(t, v.Update(10))
			require.Equal(t, 10, v.Get())
			require.Equal(t, 10, v.GetT())

			require.NoError(t, v.UpdateT(15))
			require.Equal(t, 15, v.Get())
			require.Equal(t, 15, v.GetT())
		})

	t.Run("struct with pointer",
		func(t *testing.T) {
			type test_struct struct {
				int_v    int
				string_v string
			}

			v := values.NewVariable[test_struct](
				test_struct{42, "meaning of life"})

			t.Log(v.Type())

			vp := v.GetP()
			v.Lock()
			vp.int_v = 10
			v.Unlock()

			require.Equal(t, 10, v.GetT().int_v)

		})
}
