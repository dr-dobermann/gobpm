package instance

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/errs"
)

// TestDatumErr covers FIX-026: the commit-path failure classifier carries
// the datum name and path.
func TestDatumErr(t *testing.T) {
	err := datumErr("value", "total", scope.EmptyDataPath,
		errs.New(errs.M("inner")))
	require.Error(t, err)
	require.Contains(t, err.Error(), "value datum")
	require.Contains(t, err.Error(), "total")
}
