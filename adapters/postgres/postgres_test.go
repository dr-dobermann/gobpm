package postgres_test

import (
	"database/sql"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/adapters/postgres"
	"github.com/dr-dobermann/gobpm/pkg/renv"
)

// The DSN-free half: construction validation and the capability
// declarations (NFR-2 — every public parameter checked).

func TestNewValidation(t *testing.T) {
	t.Run("a nil db is rejected", func(t *testing.T) {
		_, err := postgres.New(nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "nil *sql.DB")
	})

	t.Run("an invalid schema name is rejected", func(t *testing.T) {
		for _, bad := range []string{
			"", "Gobpm", "1st", "a.b", `x"; DROP SCHEMA public; --`,
		} {
			_, err := postgres.New(&sql.DB{}, postgres.WithSchema(bad))
			require.Error(t, err, "schema %q must be rejected", bad)
			require.Contains(t, err.Error(), "WithSchema")
		}
	})

	t.Run("a nil logger is rejected", func(t *testing.T) {
		_, err := postgres.New(&sql.DB{}, postgres.WithLogger(nil))
		require.Error(t, err)
		require.Contains(t, err.Error(), "WithLogger")
	})

	t.Run("the defaults land", func(t *testing.T) {
		repo, err := postgres.New(&sql.DB{})
		require.NoError(t, err)
		require.Equal(t, postgres.DefaultSchema, repo.Schema())
		require.Contains(t, repo.String(), postgres.DefaultSchema)
	})

	t.Run("a real logger is accepted", func(t *testing.T) {
		_, err := postgres.New(&sql.DB{},
			postgres.WithLogger(slog.Default()))
		require.NoError(t, err)
	})
}

// TestClusterDeclaration: the adapter declares itself safe to share
// between engines (renv.ClusterAware, SRD-078 FR-3).
func TestClusterDeclaration(t *testing.T) {
	repo, err := postgres.New(&sql.DB{})
	require.NoError(t, err)

	var ca renv.ClusterAware = repo

	ok, reason := ca.ClusterCompatibility()
	require.True(t, ok)
	require.NotEmpty(t, reason)
}
