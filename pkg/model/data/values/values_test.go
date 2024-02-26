package values_test

import (
	"io"
	"testing"
	"time"

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

			// check invalid indexes
			require.Error(t, a.GoTo("invalid index"))
			require.Error(t, a.GoTo(-19))
			_, err := a.GetAt("somewhere")
			require.Error(t, err)

			require.Equal(t, 5, a.Count())
			require.NoError(t, a.Next(data.StepForward))
			require.Equal(t, 1, a.Index())
			require.Equal(t, 2, a.Get())
			require.NoError(t, a.Next(data.StepBackward))
			require.Equal(t, 0, a.Index())
			require.Equal(t, 1, a.Get())

			// check keys
			kk := a.GetKeys()
			for i := range []int{1, 2, 3, 4, 5} {
				require.Contains(t, kk, i)
			}

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
			require.Error(t, a.Update("none"))

			// delete value
			require.Error(t, a.Delete("invalid index"))

			require.NoError(t, a.Delete(4))
			require.Equal(t, 5, a.Count())
			require.Equal(t, 4, a.Index())
			require.Error(t, a.Delete(7))
			require.Equal(t, 6, a.Get())

			// insert value and rewind
			require.Error(t, a.Insert(10, "invalid index"))
			require.Error(t, a.Insert("invalid value", 0))

			require.Error(t, a.Insert(7, 7))
			require.NoError(t, a.Insert(5, 4))
			require.Equal(t, 6, a.Count())
			v, err = a.GetAt(4)
			require.NoError(t, err)
			require.Equal(t, 5, v)

			a.Rewind()
			require.Equal(t, 0, a.Index())
			require.Equal(t, 1, a.Get())

			// getall
			vv := a.GetAll()
			for _, i := range []int{1, 2, 3, 4, 5} {
				require.Contains(t, vv, i)
			}

			// clear values
			a.Clear()
			a.Rewind()
			require.Equal(t, 0, a.Count())
			require.Equal(t, -1, a.Index())
			require.NoError(t, a.Add(42))
			require.Equal(t, 0, a.Index())
			require.Equal(t, 1, a.Count())

			// next
			require.Equal(t, io.EOF, a.Next(data.StepForward))
			require.Error(t, a.Next(data.StepBackward))
			require.Equal(t, 0, a.Index())

			// delete last element
			require.NoError(t, a.Delete(0))
			require.Equal(t, 0, a.Count())
			require.Equal(t, -1, a.Index())
		})

	t.Run("typed array",
		func(t *testing.T) {
			a := values.NewArray[int](1, 2, 3, 4, 5)

			require.NotEmpty(t, a)

			// invalid indexes
			require.Error(t, a.GoToT(-10))

			require.Equal(t, 5, a.Count())
			require.Equal(t, 0, a.Index())
			require.Equal(t, 1, a.GetT())

			// check keys
			kk := a.GetKeysT()
			for i := range []int{1, 2, 3, 4, 5} {
				require.Contains(t, kk, i)
			}
			// getall

			vv := a.GetAllT()
			for _, i := range []int{1, 2, 3, 4, 5} {
				require.Contains(t, vv, i)
			}
			// get at normal index
			v, err := a.GetAtT(1)
			require.NoError(t, err)
			require.Equal(t, 2, v)

			// get at index out of range
			v, err = a.GetAtT(6)
			require.Error(t, err)

			// add value
			require.NoError(t, a.AddT(6))
			require.Equal(t, 6, a.Count())
			require.NoError(t, a.GoToT(5))
			require.Equal(t, 6, a.GetT())

			// delete value
			require.NoError(t, a.DeleteT(4))
			require.Equal(t, 5, a.Count())
			require.Equal(t, 4, a.IndexT())
			require.Error(t, a.DeleteT(7))
			require.Equal(t, 6, a.GetT())

			// insert value and rewind
			require.Error(t, a.InsertT(7, 7))
			require.NoError(t, a.InsertT(5, 4))
			require.Equal(t, 6, a.Count())
			v, err = a.GetAtT(4)
			require.NoError(t, err)
			require.Equal(t, 5, v)

			a.Rewind()
			require.Equal(t, 0, a.Index())
			require.Equal(t, 1, a.GetT())

			// clear values
			a.Clear()
			require.Error(t, a.UpdateT(-1))

			require.Equal(t, 0, a.Count())
			require.Equal(t, -1, a.Index())
			require.NoError(t, a.AddT(42))
			require.Equal(t, 0, a.Index())
			require.Equal(t, 1, a.Count())

			// direct pointer access
			vp := a.GetP()
			a.Lock()
			*vp = 100
			a.Unlock()
			require.Equal(t, 100, a.Get())
		})

	t.Run("update_check",
		func(t *testing.T) {
			chCount := 0
			updateCounter := func(counter *int) data.UpdateCallback {
				return func(
					when time.Time,
					chType data.ChangeType,
					index any,
				) {
					t.Log("value updated[", index, "]: ", chType, " at: ", when)
					*counter++
				}
			}

			a := values.NewArray[int]()
			require.NoError(t, a.Register("a_tracker", updateCounter(&chCount)))
			require.Error(t, a.Register("a_tracker", updateCounter(&chCount)))
			require.Error(t, a.Register("some", nil))
			require.Error(t, a.Register("    ", updateCounter(&chCount)))

			require.NoError(t, a.Add(10))
			require.NoError(t, a.UpdateT(42))
			require.NoError(t, a.Insert(100, 0))

			require.NoError(t, a.DeleteT(1))

			time.Sleep(1 * time.Second)

			require.Equal(t, 4, chCount)

			a.Unregister("a_tracker")

			require.NoError(t, a.Update(20))

			require.Equal(t, 4, chCount)

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
			require.Error(t, v.Update("invalid value"))

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
			require.Equal(t, "meaning of life", v.GetT().string_v)
		})

	t.Run("update check",
		func(t *testing.T) {
			chCount := 0
			updateCounter := func(counter *int) data.UpdateCallback {
				return func(
					when time.Time,
					chType data.ChangeType,
					index any,
				) {
					t.Log("value updated: ", chType, " at: ", when)
					*counter++
				}
			}

			v := values.NewVariable[int](42)
			require.NoError(t, v.Register("a_tracker", updateCounter(&chCount)))
			require.Error(t, v.Register("a_tracker", updateCounter(&chCount)))
			require.Error(t, v.Register("tracker", nil))
			require.Error(t, v.Register("  ", updateCounter(&chCount)))

			require.NoError(t, v.Update(10))
			require.NoError(t, v.UpdateT(15))

			time.Sleep(1 * time.Second)

			require.Equal(t, 2, chCount)

			v.Unregister("a_tracker")

			require.NoError(t, v.Update(20))

			require.Equal(t, 2, chCount)

		})
}
