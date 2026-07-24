package artifacts_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/artifacts"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// TestConstructorsRejectInvalidOptions covers FIX-026 §4.1.7: the artifact
// constructors validate caller options with an error instead of the pre-fix
// Must* panic (the validate-all-params rule on public APIs).
func TestConstructorsRejectInvalidOptions(t *testing.T) {
	t.Run("NewCategory",
		func(t *testing.T) {
			require.NotPanics(t, func() {
				_, err := artifacts.NewCategory("c", foundation.WithID(""))
				require.Error(t, err)
			})
		})

	t.Run("NewCategoryValue",
		func(t *testing.T) {
			require.NotPanics(t, func() {
				_, err := artifacts.NewCategoryValue("v", foundation.WithID(""))
				require.Error(t, err)
			})
		})

	t.Run("MustCategory and MustCategoryValue panic for fixtures",
		func(t *testing.T) {
			require.Panics(t, func() {
				artifacts.MustCategory("c", foundation.WithID(""))
			})
			require.Panics(t, func() {
				artifacts.MustCategoryValue("v", foundation.WithID(""))
			})
		})
}
