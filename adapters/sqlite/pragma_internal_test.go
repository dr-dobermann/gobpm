package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestOpenSetsPragmasOnLaterConnections is T-4b: the property a post-open
// `PRAGMA` exec does NOT have.
//
// Executing the pragma once after sql.Open configures whichever connection
// served that statement. This asserts it on a connection the pool creates
// afterwards, which is the only version of the claim that means anything.
func TestOpenSetsPragmasOnLaterConnections(t *testing.T) {
	repo, err := Open(filepath.Join(t.TempDir(), "later.db"))
	require.NoError(t, err)

	t.Cleanup(func() { require.NoError(t, repo.Close()) })

	ctx := context.Background()
	require.NoError(t, repo.Migrate(ctx))

	db := repo.db

	// Hold several connections open at once so the pool must mint new ones,
	// then check the pragma on each.
	var conns []*sql.Conn

	defer func() {
		for _, c := range conns {
			require.NoError(t, c.Close())
		}
	}()

	for i := range 5 {
		c, cerr := db.Conn(ctx)
		require.NoError(t, cerr)

		conns = append(conns, c)

		var on int

		require.NoError(t,
			c.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&on))
		require.Equal(t, 1, on,
			"connection %d, created after Open, lost foreign_keys", i)

		var mode string

		require.NoError(t,
			c.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode))
		require.Equal(t, "wal", mode, "connection %d is not in WAL", i)
	}
}
