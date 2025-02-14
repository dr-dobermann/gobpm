package values_test

import (
	"context"
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

			ctx := context.Background()

			require.Error(t, a.Delete(ctx, 0))
			require.Error(t, a.GoTo(0))
			require.Error(t, a.Update(ctx, 5))
			require.Error(t, a.Insert(ctx, 2, 0))
			require.Error(t, a.Next(data.StepForward))

			nA := a.Clone()
			require.Equal(t, "int", nA.Type())
			nAa, ok := nA.(data.Collection)
			require.True(t, ok)
			require.Equal(t, -1, nAa.Index())
			require.Equal(t, 0, nAa.Count())
		})

	t.Run("normal array",
		func(t *testing.T) {
			a := values.NewArray[int](1, 2, 3, 4, 5)

			require.NotEmpty(t, a)

			ctx := context.Background()

			// check invalid indexes
			require.Error(t, a.GoTo("invalid index"))
			require.Error(t, a.GoTo(-19))
			_, err := a.GetAt(ctx, "somewhere")
			require.Error(t, err)

			require.Equal(t, 5, a.Count())
			require.NoError(t, a.Next(data.StepForward))
			require.Equal(t, 1, a.Index())
			require.Equal(t, 2, a.Get(ctx))
			require.NoError(t, a.Next(data.StepBackward))
			require.Equal(t, 0, a.Index())
			require.Equal(t, 1, a.Get(ctx))

			// check keys
			kk := a.GetKeys()
			for i := range []int{1, 2, 3, 4, 5} {
				require.Contains(t, kk, i)
			}

			// get at normal index
			v, err := a.GetAt(ctx, 1)
			require.NoError(t, err)
			require.Equal(t, 2, v)

			// get at index out of range
			v, err = a.GetAt(ctx, 6)
			require.Error(t, err)

			// cloning
			na, ok := a.Clone().(data.Collection)
			require.True(t, ok)
			require.Equal(t, 5, na.Count())
			vv := a.GetAll(ctx)
			for _, v := range []int{1, 2, 3, 4, 5} {
				require.Contains(t, vv, v)
			}

			// add value
			require.NoError(t, a.Add(ctx, 6))
			require.Equal(t, 6, a.Count())
			require.NoError(t, a.GoTo(5))
			require.Equal(t, 6, a.Get(ctx))

			require.Error(t, a.Add(ctx, "six"))
			require.Error(t, a.Update(ctx, "none"))

			// delete value
			require.Error(t, a.Delete(ctx, "invalid index"))

			require.NoError(t, a.Delete(ctx, 4))
			require.Equal(t, 5, a.Count())
			require.Equal(t, 4, a.Index())
			require.Error(t, a.Delete(ctx, 7))
			require.Equal(t, 6, a.Get(ctx))

			// insert value and rewind
			require.Error(t, a.Insert(ctx, 10, "invalid index"))
			require.Error(t, a.Insert(ctx, "invalid value", 0))

			require.Error(t, a.Insert(ctx, 7, 7))
			require.NoError(t, a.Insert(ctx, 5, 4))
			require.Equal(t, 6, a.Count())
			v, err = a.GetAt(ctx, 4)
			require.NoError(t, err)
			require.Equal(t, 5, v)

			a.Rewind()
			require.Equal(t, 0, a.Index())
			require.Equal(t, 1, a.Get(ctx))

			// getall
			vv = a.GetAll(ctx)
			for _, i := range []int{1, 2, 3, 4, 5} {
				require.Contains(t, vv, i)
			}

			// clear values
			a.Clear()
			a.Rewind()
			require.Equal(t, 0, a.Count())
			require.Equal(t, -1, a.Index())
			require.NoError(t, a.Add(ctx, 42))
			require.Equal(t, 0, a.Index())
			require.Equal(t, 1, a.Count())

			// next
			require.Equal(t, io.EOF, a.Next(data.StepForward))
			require.Error(t, a.Next(data.StepBackward))
			require.Equal(t, 0, a.Index())

			// delete last element
			require.NoError(t, a.Delete(ctx, 0))
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
			require.Equal(t, 100, a.Get(context.Background()))
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

			ctx := context.Background()

			a := values.NewArray[int]()
			require.NoError(t, a.Register("a_tracker", updateCounter(&chCount)))
			require.Error(t, a.Register("a_tracker", updateCounter(&chCount)))
			require.Error(t, a.Register("some", nil))
			require.Error(t, a.Register("    ", updateCounter(&chCount)))

			require.NoError(t, a.Add(ctx, 10))
			require.NoError(t, a.UpdateT(42))
			require.NoError(t, a.Insert(ctx, 100, 0))

			require.NoError(t, a.DeleteT(1))

			time.Sleep(1 * time.Second)

			require.Equal(t, 4, chCount)

			a.Unregister("a_tracker")

			require.NoError(t, a.Update(ctx, 20))

			require.Equal(t, 4, chCount)
		})
}

func TestVariable(t *testing.T) {
	t.Run("int",
		func(t *testing.T) {
			v := values.NewVariable[int](42)

			ctx := context.Background()

			// check value
			require.Equal(t, 42, v.Get(ctx))
			require.Equal(t, 42, v.GetT())

			// update value
			require.Error(t, v.Update(ctx, "invalid value"))

			require.NoError(t, v.Update(ctx, 10))
			require.Equal(t, 10, v.Get(ctx))
			require.Equal(t, 10, v.GetT())

			require.NoError(t, v.UpdateT(15))
			require.Equal(t, 15, v.Get(ctx))
			require.Equal(t, 15, v.GetT())

			// cloning
			nv := v.Clone()
			require.Equal(t, "int", nv.Type())
			require.Equal(t, 15, nv.Get(ctx))
		})

	t.Run("struct with pointer",
		func(t *testing.T) {
			type test_struct struct {
				int_v    int
				string_v string
			}

			ctx := context.Background()

			v := values.NewVariable[test_struct](
				test_struct{42, "meaning of life"})
			require.Equal(t, "test_struct", v.Type())

			// cloning
			nv := v.Clone()
			require.Equal(t, "test_struct", nv.Type())

			vp := v.GetP()
			v.Lock()
			vp.int_v = 10
			v.Unlock()

			require.Equal(t, 10, v.GetT().int_v)
			require.Equal(t, "meaning of life", v.GetT().string_v)

			require.Equal(t, 42, nv.Get(ctx).(test_struct).int_v)
			require.Equal(t, "meaning of life", nv.Get(ctx).(test_struct).string_v)
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

			ctx := context.Background()

			v := values.NewVariable[int](42)
			require.NoError(t, v.Register("a_tracker", updateCounter(&chCount)))
			require.Error(t, v.Register("a_tracker", updateCounter(&chCount)))
			require.Error(t, v.Register("tracker", nil))
			require.Error(t, v.Register("  ", updateCounter(&chCount)))

			require.NoError(t, v.Update(ctx, 10))
			require.NoError(t, v.UpdateT(15))

			time.Sleep(1 * time.Second)

			require.Equal(t, 2, chCount)

			v.Unregister("a_tracker")

			require.NoError(t, v.Update(ctx, 20))

			require.Equal(t, 2, chCount)
		})
}
