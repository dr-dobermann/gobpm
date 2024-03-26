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
	n, err := c.AddElements(
		elements...)
	require.Equal(t, 2, n)
	require.NoError(t, err)

	// add invalid elements
	n, err = c.AddElements(nil, elements[0])
	require.Equal(t, 0, n)
	require.Error(t, err)

	// get elements
	ee := c.Elements()
	require.Equal(t, 2, len(ee))

	// check elements
	require.True(t, c.Contains("element#1"))
	require.False(t, c.Contains("invalid_id"))

	// remove element
	require.NoError(t, c.RemoveById("element#1"))
	require.False(t, c.Contains("element#1"))
	require.Empty(t, elements[0].Container())

	// remove element with invalid id
	require.Error(t, c.RemoveById("    "))
	require.Error(t, c.RemoveById("invalid_id"))

	// invalid container
	ic := flow.ElementsContainer{}
	require.Error(t, ic.Add(elements[0]))
	n, err = ic.AddElements(elements...)
	require.Equal(t, 0, n)
	require.Error(t, err)
	require.Error(t, ic.RemoveById(elements[0].Id()))
	require.Panics(t, func() { ic.Contains(elements[0].Id()) })
	require.Panics(t, func() { ic.Elements() })

	// conflict container
	cc, err := flow.NewContainer()
	require.NoError(t, err)
	n, err = cc.AddElements(elements...)
	require.Error(t, err)
	require.Equal(t, 1, n)
}
