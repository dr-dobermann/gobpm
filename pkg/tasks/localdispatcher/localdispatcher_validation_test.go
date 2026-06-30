package localdispatcher

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/tasks"
	"github.com/stretchr/testify/require"
)

// TestRegisterRejectsNilHandlerAndEmptyType pins FIX-010 §3.2.5: Register must
// reject a nil handler and an empty job type at the boundary, so the failure
// surfaces here instead of as a deferred nil-call panic in Dispatch.
func TestRegisterRejectsNilHandlerAndEmptyType(t *testing.T) {
	d := New(1)

	require.ErrorIs(t, d.Register("job", nil), ErrNilHandler)
	require.ErrorIs(t, d.Register("", func(context.Context, tasks.Job) (any, error) {
		return nil, nil
	}), ErrEmptyJobType)

	// a valid registration still succeeds.
	require.NoError(t, d.Register("job", func(context.Context, tasks.Job) (any, error) {
		return nil, nil
	}))
}
