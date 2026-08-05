package postgres

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestParseMigrations pins the NNNN_*.sql naming gate and the version
// ordering the embedded set relies on.
func TestParseMigrations(t *testing.T) {
	t.Run("valid names sort by version", func(t *testing.T) {
		mm, err := parseMigrations(
			[]string{"0002_more.sql", "0001_init.sql"})
		require.NoError(t, err)
		require.Len(t, mm, 2)
		require.Equal(t, 1, mm[0].version)
		require.Equal(t, 2, mm[1].version)
	})

	t.Run("a malformed name is rejected", func(t *testing.T) {
		for _, bad := range []string{
			"init.sql", "01_short.sql", "0001.sql", "0001_x.txt",
		} {
			_, err := parseMigrations([]string{bad})
			require.Error(t, err, "%q must be rejected", bad)
			require.Contains(t, err.Error(), "NNNN_*.sql")
		}
	})

	t.Run("the embedded set parses", func(t *testing.T) {
		mm, err := loadMigrations()
		require.NoError(t, err)
		require.NotEmpty(t, mm)
		require.Equal(t, 1, mm[0].version)
	})
}
