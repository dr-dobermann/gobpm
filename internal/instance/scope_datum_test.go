package instance

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
)

// TestBindValueAtBadName covers FIX-026 §4.1.2: an invalid datum name fails
// the commit with a classified error instead of the pre-fix MustParameter
// panic (the name arrives from model data — e.g. a Multi-Instance output
// name — so this path is reachable with real input).
func TestBindValueAtBadName(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	inst, _, host := miSeqFixture(t)

	require.NotPanics(t, func() {
		err := inst.sc.bindValueAt(
			host.scopePath, "", values.NewVariable(1))
		require.Error(t, err)
		require.Contains(t, err.Error(), "couldn't build value datum")
	})
}
