package errs

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestOptionApplyNilConfig: an Option applied to no configuration reports it
// instead of dereferencing nil. New never passes nil, so only a direct call
// reaches the guard.
func TestOptionApplyNilConfig(t *testing.T) {
	err := M("unused").apply(nil)

	require.EqualError(t, err, "empty error configuration")
}
