package sqlite

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/repository"
)

// closedRepo returns a migrated Repo whose pool has been shut, so every
// database call fails. It is how the store's error paths are reached without
// a fault-injecting driver: the adapter's job on a broken database is to
// classify and report, and a closed pool breaks every statement identically.
func closedRepo(t *testing.T) *Repo {
	t.Helper()

	repo, err := Open(filepath.Join(t.TempDir(), "closed.db"))
	require.NoError(t, err)
	require.NoError(t, repo.Migrate(context.Background()))
	require.NoError(t, repo.Close())

	return repo
}

// TestEveryOperationReportsABrokenDatabase: no method may return nil, or
// panic, when the store underneath it is gone.
func TestEveryOperationReportsABrokenDatabase(t *testing.T) {
	repo := closedRepo(t)
	ctx := context.Background()

	rec := repository.InstanceRecord{
		ID: "i1", Group: "g", Status: repository.StatusActive,
	}

	for name, call := range map[string]func() error{
		"Save": func() error { return repo.Save(ctx, rec) },
		"Load": func() error {
			_, _, err := repo.Load(ctx, "i1")

			return err
		},
		"Delete": func() error { return repo.Delete(ctx, "i1") },
		"ListInFlight": func() error {
			_, err := repo.ListInFlight(ctx, "g", time.Now())

			return err
		},
		"RegisterGroup": func() error { return repo.RegisterGroup(ctx, "g") },
		"GroupExists": func() error {
			_, err := repo.GroupExists(ctx, "g")

			return err
		},
		"Migrate": func() error { return repo.Migrate(ctx) },
	} {
		t.Run(name, func(t *testing.T) {
			require.Error(t, call(), "%s must report a broken database", name)
		})
	}
}

// TestSaveUpdatePathReportsABrokenTable covers the UPDATE branch's failure,
// which a closed pool cannot reach.
//
// The first version of this test used closedRepo and a non-zero RecVersion,
// claiming to take the other road through Save. It did not: Save calls
// GroupExists first, that query fails on a closed pool, and the function
// returns before RecVersion is ever examined. require.Error was satisfied by
// the wrong failure — the test passed while covering nothing it named.
//
// Reaching the update path needs a database that is HEALTHY enough to answer
// GroupExists and resolve the tenant, and broken only where the update lands.
// Dropping the instances table leaves exactly that.
func TestSaveUpdatePathReportsABrokenTable(t *testing.T) {
	repo, err := OpenMemory()
	require.NoError(t, err)

	t.Cleanup(func() { require.NoError(t, repo.Close()) })

	ctx := context.Background()
	require.NoError(t, repo.Migrate(ctx))
	require.NoError(t, repo.RegisterGroup(ctx, "g"))

	rec := repository.InstanceRecord{
		ID: "i1", Group: "g", Status: repository.StatusActive,
	}
	require.NoError(t, repo.Save(ctx, rec))

	// groups and tenants survive; only the update's target is gone
	_, err = repo.db.ExecContext(ctx, "DROP TABLE instances")
	require.NoError(t, err)

	rec.RecVersion = 1

	err = repo.Save(ctx, rec)
	require.Error(t, err, "the update path must report a broken table")
	require.Contains(t, err.Error(), "update",
		"the error must name the operation that failed, so it is "+
			"distinguishable from the create path's")
}

// TestCASRejectsAStaleVersion covers casOutcome's zero-rows branch through the
// public surface: the fencing every adapter reports identically.
func TestCASRejectsAStaleVersion(t *testing.T) {
	repo, err := OpenMemory()
	require.NoError(t, err)

	t.Cleanup(func() { require.NoError(t, repo.Close()) })

	ctx := context.Background()
	require.NoError(t, repo.Migrate(ctx))
	require.NoError(t, repo.RegisterGroup(ctx, "g"))

	rec := repository.InstanceRecord{
		ID: "i1", Group: "g", Status: repository.StatusActive,
	}
	require.NoError(t, repo.Save(ctx, rec))

	// a second create of the same id: the id exists, so the insert touches no
	// rows and the writer is fenced.
	require.Error(t, repo.Save(ctx, rec))

	// and an update from a version that is no longer current
	stale := rec
	stale.RecVersion = 99
	require.Error(t, repo.Save(ctx, stale))
}

// TestExplicitTenantIsEnsured covers resolveTenant's non-default branch.
func TestExplicitTenantIsEnsured(t *testing.T) {
	repo, err := OpenMemory()
	require.NoError(t, err)

	t.Cleanup(func() { require.NoError(t, repo.Close()) })

	ctx := context.Background()
	require.NoError(t, repo.Migrate(ctx))
	require.NoError(t, repo.RegisterGroup(ctx, "g"))

	rec := repository.InstanceRecord{
		ID: "i1", Group: "g", Tenant: "acme", Status: repository.StatusActive,
	}
	require.NoError(t, repo.Save(ctx, rec))

	got, ok, err := repo.Load(ctx, "i1")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "acme", got.Tenant,
		"an explicit tenant must survive the round trip, not be replaced by "+
			"the group's default")
}

// TestConstructorGuards covers the constructors' refusals.
func TestConstructorGuards(t *testing.T) {
	t.Run("an empty path", func(t *testing.T) {
		_, err := Open("   ")
		require.Error(t, err)
	})

	t.Run("a nil *sql.DB", func(t *testing.T) {
		_, err := New(nil)
		require.Error(t, err)
	})

	t.Run("a nil logger", func(t *testing.T) {
		_, err := OpenMemory(WithLogger(nil))
		require.Error(t, err)
	})

	t.Run("an unopenable path", func(t *testing.T) {
		// a directory that does not exist cannot hold a database file
		_, err := Open(filepath.Join(t.TempDir(), "nope", "x.db"))
		require.Error(t, err)
	})
}

// TestCloseIsSafeForABorrowedPool: a Repo built by New never closes a pool it
// does not own, and Close on such a Repo is a no-op rather than an error.
func TestCloseIsSafeForABorrowedPool(t *testing.T) {
	repo, err := OpenMemory()
	require.NoError(t, err)

	borrowed := &Repo{db: repo.db, logger: repo.logger, q: repo.q}
	require.NoError(t, borrowed.Close())

	// the real owner still works, which is the point
	require.NoError(t, repo.Migrate(context.Background()))
	require.NoError(t, repo.Close())
}

// TestDSNSeparatorAndLayout covers the two encoding helpers whose failures are
// silent rather than loud.
func TestDSNSeparatorAndLayout(t *testing.T) {
	t.Run("a plain path takes ?", func(t *testing.T) {
		require.Contains(t, dsn("app.db"), "app.db?_pragma=")
	})

	t.Run("a path with a query takes &", func(t *testing.T) {
		got := dsn("file:app.db?cache=shared")
		require.Contains(t, got, "cache=shared&")
		require.Equal(t, 1, strings.Count(got, "?"),
			"a second ? would fold the pragmas into the previous value")
	})

	t.Run("time round-trips", func(t *testing.T) {
		in := time.Date(2026, 8, 11, 10, 30, 0, 450000000, time.UTC)

		out, err := decodeTime(encodeTime(in))
		require.NoError(t, err)
		require.True(t, out.Equal(in))
	})

	t.Run("an empty time decodes to the zero instant", func(t *testing.T) {
		out, err := decodeTime("")
		require.NoError(t, err)
		require.True(t, out.IsZero())
	})

	t.Run("garbage is reported", func(t *testing.T) {
		_, err := decodeTime("not-a-time")
		require.Error(t, err)
	})
}

// TestMigrationNamingIsEnforced covers parseMigrations' rejection: a file that
// does not carry a version cannot be ordered, and applying migrations out of
// order is how a schema ends up half-built.
func TestMigrationNamingIsEnforced(t *testing.T) {
	_, err := parseMigrations([]string{"0001_init.sql", "oops.sql"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "NNNN_*.sql")

	mm, err := parseMigrations([]string{"0002_b.sql", "0001_a.sql"})
	require.NoError(t, err)
	require.Equal(t, 1, mm[0].version, "migrations must sort by version")
	require.Equal(t, 2, mm[1].version)
}

// TestNextPendingStopsAtTheCurrentVersion covers the up-to-date branch.
func TestNextPendingStopsAtTheCurrentVersion(t *testing.T) {
	mm := []migration{{name: "0001_a.sql", version: 1}}

	_, ok := nextPending(mm, 0)
	require.True(t, ok)

	_, ok = nextPending(mm, 1)
	require.False(t, ok, "an applied migration is not pending again")
}
