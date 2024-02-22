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
