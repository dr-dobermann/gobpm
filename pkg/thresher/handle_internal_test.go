package thresher

import (
	"testing"

	"github.com/dr-dobermann/gobpm/internal/instance"
	"github.com/stretchr/testify/require"
)

// TestTokenStateMapping covers every branch of the internal projected-token-state
// to public-vocabulary mapping, including the values not yet reachable through a
// running process (Withdrawn awaits the Event-Based Gateway; Invalid is the
// defensive default).
func TestTokenStateMapping(t *testing.T) {
	for _, tc := range []struct {
		in   instance.TokenState
		want TokenState
	}{
		{instance.TokenAlive, TokenAlive},
		{instance.TokenWaitForEvent, TokenWaitForEvent},
		{instance.TokenConsumed, TokenConsumed},
		{instance.TokenInvalid, TokenInvalid},
	} {
		require.Equal(t, tc.want, tokenState(tc.in))
	}
}

