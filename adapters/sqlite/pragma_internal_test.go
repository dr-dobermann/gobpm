package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
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

// TestForeignKeysEnforcedOnEveryConnection is SRD-091 T-3, rewritten after
// the independent review found the first version could not fail for the
// reason it claimed.
//
// That version called repo.Save with an unregistered group — but Save checks
// GroupExists itself and returns ObjectNotFound BEFORE any INSERT, so the
// statement never reached the database and the foreign key was never
// exercised. It asserted require.Error, which the application-level check
// satisfied on its own: the test passed identically with foreign_keys OFF.
//
// This one INSERTs directly through the pool, bypassing Save, so the only
// thing that can refuse the row is the constraint under test. It repeats
// across held connections because the pragma is per connection: a pool with
// one configured connection and three unconfigured ones is exactly the state
// this exists to catch.
func TestForeignKeysEnforcedOnEveryConnection(t *testing.T) {
	repo, err := Open(filepath.Join(t.TempDir(), "fk.db"))
	require.NoError(t, err)

	t.Cleanup(func() { require.NoError(t, repo.Close()) })

	ctx := context.Background()
	require.NoError(t, repo.Migrate(ctx))

	const probes = 5

	var conns []*sql.Conn

	defer func() {
		for _, c := range conns {
			require.NoError(t, c.Close())
		}
	}()

	for i := range probes {
		// Hold each connection so the pool must mint a new one for the next
		// probe, rather than handing back the same configured connection.
		c, cerr := repo.db.Conn(ctx)
		require.NoError(t, cerr)

		conns = append(conns, c)

		_, ierr := c.ExecContext(ctx,
			"INSERT INTO instances"+
				" (id, engine_group, tenant_id, status, rec_version)"+
				" VALUES (?, 'never-registered', 'default', 1, 1)",
			fmt.Sprintf("i-%d", i))

		require.Error(t, ierr,
			"connection %d accepted a row naming an unregistered group: the "+
				"foreign key is not being enforced there", i)
		require.Contains(t, strings.ToLower(ierr.Error()), "foreign key",
			"connection %d refused for some OTHER reason than the constraint "+
				"under test, so this proves nothing about it", i)
	}
}
