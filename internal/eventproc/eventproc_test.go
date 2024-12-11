package eventproc_test

import (
	"fmt"
	"testing"

	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/stretchr/testify/require"
)

func TestWaiterState(t *testing.T) {
	require.Equal(t, "Ready", eventproc.WSReady.String())

	require.Panics(t, func() {
		const invalidState = 1000

		fmt.Println(eventproc.EventWaiterState(invalidState).String())
	})
}
