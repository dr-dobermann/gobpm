package foundation

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestBaseConfigValidate covers baseConfig.Validate, which is only reachable
// from inside the package (the type is unexported).
func TestBaseConfigValidate(t *testing.T) {
	require.NoError(t, (&baseConfig{}).Validate())
}
