package set_test

import (
	"reflect"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/set"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	t.Run("int", func(t *testing.T) {
		s := set.New[int](1, 2, 3, 3, 4, 5)
		expect := []int{1, 2, 3, 4, 5}

		require.True(t, reflect.DeepEqual(s.All(), expect))
	})

	t.Run("string", func(t *testing.T) {
		s := set.New[string]("one", "two", "two", "three", "four", "five")
		expect := []string{"one", "two", "three", "four", "five"}

		require.True(t, reflect.DeepEqual(s.All(), expect))
	})
}

func TestHas(t *testing.T) {
	cases := []struct {
		name   string
		value  int
		result bool
	}{
		{"has", 1, true},
		{"doesn't has", 10, false},
	}

	s := set.New[int](1, 2, 3, 4, 4, 5)

	for _, c := range cases {
		t.Run(
			c.name,
			func(t *testing.T) {
				require.Equal(t, c.result, s.Has(c.value))
			})
	}
}

func TestAdd(t *testing.T) {
	cases := []struct {
		name   string
		values []int
		result []int
	}{
		{
			name:   "normal",
			values: []int{4, 5},
			result: []int{1, 2, 3, 4, 5},
		},
		{
			name:   "duplicate",
			values: []int{3, 4, 5},
			result: []int{1, 2, 3, 4, 5},
		},
		{
			name:   "empty",
			values: []int{},
			result: []int{1, 2, 3},
		},
	}

	for _, c := range cases {
		t.Run(
			c.name,
			func(t *testing.T) {
				s := set.New[int](1, 2, 3)

				s.Add(c.values...)

				require.True(t,
					reflect.DeepEqual(s.All(), c.result))
			})
	}
}

func TestRemove(t *testing.T) {
	cases := []struct {
		name   string
		values []int
		result []int
	}{
		{
			name:   "normal",
			values: []int{2, 3, 4, 5},
			result: []int{1},
		},
		{
			name:   "duplicate",
			values: []int{2, 2, 3, 4, 5},
			result: []int{1},
		},
		{
			name:   "empty",
			values: []int{},
			result: []int{1, 2, 3},
		},
	}

	for _, c := range cases {
		t.Run(
			c.name,
			func(t *testing.T) {
				s := set.New[int](1, 2, 3)

				s.Remove(c.values...)

				require.True(t,
					reflect.DeepEqual(s.All(), c.result))
			})
	}
}
