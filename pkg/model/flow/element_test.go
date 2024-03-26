package flow_test

import (
	"strconv"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
)

func TestElementsContainer(t *testing.T) {
	c, err := flow.NewContainer(foundation.WithId("test container"))
	require.NoError(t, err)
	require.NotEmpty(t, c)

	// create test elements
	elements := make([]*flow.Element, 2)
	for i := 0; i < 2; i++ {
		elements[i] = flow.MustElement("element_"+strconv.Itoa(i+1),
			foundation.WithId("element#"+strconv.Itoa(i+1)))
	}

	// add elements
	require.Equal(t, 3, c.Add(
		elements[0],
		elements[0],
		nil,
		elements[1]))

	// get elements
	ee := c.Elements()
	require.Equal(t, 2, len(ee))

	// check elements
	require.True(t, c.Contains("element#1"))
	require.False(t, c.Contains("invalid_id"))

	// remove element
	require.Equal(t, 1, c.Remove("element#1", "invalid_id"))
	require.False(t, c.Contains("element#1"))
}
