package values_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/stretchr/testify/require"
)

func TestInt(t *testing.T) {
	i := values.Int(10)

	require.Equal(t, 10, i.Get())

	require.NoError(t, i.Update(15))

	require.Equal(t, 15, i.Get())
}

func TestStrings(t *testing.T) {
	s := values.String("this is a test")

	require.Equal(t, "this is a test", s.Get())

	require.NoError(t, s.Update("another test"))

	require.Equal(t, "another test", s.Get())
}

func TestArray(t *testing.T) {
	t.Run("empty array",
		func(t *testing.T) {
			a := values.NewArray[int]()

			require.NotEmpty(t, a)

			require.Equal(t, "int", a.Type())
			require.Equal(t, -1, a.Index())
			require.Equal(t, 0, a.Len())

			require.Error(t, a.Delete(0))
			require.Error(t, a.GoTo(0))
			require.Error(t, a.Update(5))
			require.Error(t, a.Insert(2, 0))
			require.Error(t, a.Next(1))
		})

	t.Run("normal array",
		func(t *testing.T) {
			a := values.NewArray[int](1, 2, 3, 4, 5)

			require.NotEmpty(t, a)

			require.Equal(t, 5, a.Len())
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
			a.Add(6)
			require.Equal(t, 6, a.Len())
			require.NoError(t, a.GoTo(5))
			require.Equal(t, 6, a.Get())

			// delete value
			require.NoError(t, a.Delete(4))
			require.Equal(t, 5, a.Len())
			require.Equal(t, 4, a.Index())
			require.Error(t, a.Delete(7))
			require.Equal(t, 6, a.Get())
		})
}
