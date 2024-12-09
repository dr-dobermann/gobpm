package waiters_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/internal/eventproc/eventhub/waiters"
	"github.com/stretchr/testify/require"
)

func TestNewWaiter(t *testing.T) {
	// empty event definition
	_, err := waiters.CreateWaiter(nil, nil)
	require.Error(t, err)
}
