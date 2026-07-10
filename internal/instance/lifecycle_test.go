package instance

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRunNilContext: Run rejects a nil context before touching any state —
// the public-API validate-all-parameters rule.
func TestRunNilContext(t *testing.T) {
	inst := &Instance{}

	require.Error(t, inst.Run(nil))
	require.Equal(t, Created, inst.State())
}
