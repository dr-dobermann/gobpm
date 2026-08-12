package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
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

// TestSaveCreatePathReportsABrokenTable is the create-path twin of the test
// above, and it exists for the same reason that one does.
//
// TestEveryOperationReportsABrokenDatabase appears to cover Save on a broken
// store, but a closed pool fails Save's opening GroupExists, so it returns
// before the INSERT is ever attempted: the create path's error handling was
// never executed by any test. The update path was caught and fixed; this was
// the same defect one branch over.
func TestSaveCreatePathReportsABrokenTable(t *testing.T) {
	repo, err := OpenMemory()
	require.NoError(t, err)

	t.Cleanup(func() { require.NoError(t, repo.Close()) })

	ctx := context.Background()
	require.NoError(t, repo.Migrate(ctx))
	require.NoError(t, repo.RegisterGroup(ctx, "g"))

	// groups and tenants survive, so GroupExists and the tenant resolution
	// both succeed and the failure lands on the insert itself
	_, err = repo.db.ExecContext(ctx, "DROP TABLE instances")
	require.NoError(t, err)

	err = repo.Save(ctx, repository.InstanceRecord{
		ID: "i1", Group: "g", Status: repository.StatusActive,
	})
	require.Error(t, err, "the create path must report a broken table")
	require.Contains(t, err.Error(), "create",
		"the error must name the operation that failed, so it is "+
			"distinguishable from the update path's")
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
//
// It goes through New. The first version built &Repo{...} by hand, which set
// ownsDB to false by Go's ZERO VALUE rather than by the constructor — so it
// asserted the struct literal's default, not New's behaviour, and would have
// kept passing if New started claiming ownership of pools it was handed.
func TestCloseIsSafeForABorrowedPool(t *testing.T) {
	db, err := sql.Open(driverName,
		dsn(filepath.Join(t.TempDir(), "borrowed.db")))
	require.NoError(t, err)

	t.Cleanup(func() { require.NoError(t, db.Close()) })

	borrowed, err := New(db)
	require.NoError(t, err)

	require.NoError(t, borrowed.Close())

	// the pool the caller owns is untouched, which is the point
	require.NoError(t, db.PingContext(context.Background()),
		"New must not close a pool it does not own")

	// and it is still usable through the Repo that did not close it
	require.NoError(t, borrowed.Migrate(context.Background()))
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

// TestMigrateReportsAnUnwritableDatabase covers the BEGIN IMMEDIATE failure
// and the rollback path around it.
//
// BEGIN IMMEDIATE takes the write lock at the start of the transaction, so a
// database that cannot be written fails THERE rather than part-way through a
// migration — which is the behaviour that makes the explicit BEGIN worth
// having over the DEFERRED default.
func TestMigrateReportsAnUnwritableDatabase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ro.db")

	repo, err := Open(path)
	require.NoError(t, err)
	require.NoError(t, repo.Migrate(context.Background()))
	require.NoError(t, repo.Close())

	// make the DIRECTORY read-only too: SQLite needs to write the -wal and
	// -shm companions, so a read-only file alone is not enough.
	require.NoError(t, os.Chmod(path, 0o444))
	require.NoError(t, os.Chmod(dir, 0o555))

	t.Cleanup(func() {
		//nolint:errcheck // restoring permissions so TempDir can clean up
		_ = os.Chmod(dir, 0o755)
	})

	// One of the two must refuse. Returning early when Open fails would make
	// this test assert nothing at all on that path — the failure mode it
	// exists to catch elsewhere.
	ro, err := Open(path)
	if err == nil {
		t.Cleanup(func() { _ = ro.Close() }) //nolint:errcheck // best effort

		err = ro.Migrate(context.Background())
	}

	require.Error(t, err,
		"an unwritable database must be refused at Open or at BEGIN, "+
			"never silently accepted")
}

// TestMigrateReportsAFailedMigration covers the apply-failure branch and the
// rollback that follows it.
//
// Dropping the ledger while leaving the objects behind makes the adapter
// believe it is at version 0 and re-run 0001, whose CREATE TABLE then
// collides with the tables that are still there.
func TestMigrateReportsAFailedMigration(t *testing.T) {
	repo, err := OpenMemory()
	require.NoError(t, err)

	t.Cleanup(func() { require.NoError(t, repo.Close()) })

	ctx := context.Background()
	require.NoError(t, repo.Migrate(ctx))

	_, err = repo.db.ExecContext(ctx, "DROP TABLE schema_version")
	require.NoError(t, err)

	err = repo.Migrate(ctx)
	require.Error(t, err, "re-applying a migration over existing objects must "+
		"report, not half-apply")
	require.Contains(t, err.Error(), "0001",
		"the error must name the migration that failed")
}

// TestMigrateReportsAnUnreadableLedger covers the version-read failure.
//
// A schema_version table that exists but carries no version column leaves
// CREATE TABLE IF NOT EXISTS satisfied and the SELECT broken — which is the
// shape of a ledger written by something other than this adapter.
func TestMigrateReportsAnUnreadableLedger(t *testing.T) {
	repo, err := OpenMemory()
	require.NoError(t, err)

	t.Cleanup(func() { require.NoError(t, repo.Close()) })

	ctx := context.Background()

	_, err = repo.db.ExecContext(ctx,
		"CREATE TABLE schema_version (something_else TEXT)")
	require.NoError(t, err)

	err = repo.Migrate(ctx)
	require.Error(t, err, "an unreadable ledger must be reported, not treated "+
		"as version zero — which would re-run every migration")
	require.Contains(t, err.Error(), "schema version")
}

// TestMigrateReportsAContendedWriteLock covers the BEGIN IMMEDIATE failure.
//
// It is the branch that only exists because the transaction is explicit: with
// the DEFERRED default, a second migrator would get this far and deadlock
// later, mid-migration. Taking the write lock at BEGIN turns that into a
// clean, reportable refusal — which is the whole argument for the explicit
// statement, so it is worth a test that actually contends.
func TestMigrateReportsAContendedWriteLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "contended.db")

	// Migrate FIRST, so the ledger exists. Otherwise Migrate's opening
	// CREATE TABLE IF NOT EXISTS is itself a write, blocks on the holder, and
	// fails there — the test would pass without ever reaching BEGIN
	// IMMEDIATE, which is the branch it exists for.
	seed, err := Open(path)
	require.NoError(t, err)
	require.NoError(t, seed.Migrate(context.Background()))
	require.NoError(t, seed.Close())

	// the holder: a plain connection sitting in a write transaction
	holderDB, oerr := sql.Open(driverName, dsn(path))
	require.NoError(t, oerr)

	t.Cleanup(func() { require.NoError(t, holderDB.Close()) })

	ctx := context.Background()

	holder, err := holderDB.Conn(ctx)
	require.NoError(t, err)

	t.Cleanup(func() { require.NoError(t, holder.Close()) })

	_, err = holder.ExecContext(ctx, "BEGIN IMMEDIATE")
	require.NoError(t, err)

	defer func() {
		//nolint:errcheck // releasing the lock at the end of the test
		_, _ = holder.ExecContext(ctx, "ROLLBACK")
	}()

	// the migrator: a short busy timeout, so the contention resolves in
	// milliseconds rather than the five seconds Open would give it
	busyDB, err := sql.Open(driverName,
		path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(50)")
	require.NoError(t, err)

	t.Cleanup(func() { require.NoError(t, busyDB.Close()) })

	repo, err := New(busyDB)
	require.NoError(t, err)

	merr := repo.Migrate(ctx)
	require.Error(t, merr,
		"a migrator that cannot take the write lock must report it, not "+
			"proceed and deadlock part-way through")
	require.Contains(t, merr.Error(), "migration transaction",
		"the failure must be the transaction's, not an earlier write's — "+
			"otherwise this test never reaches BEGIN IMMEDIATE")
}

// TestTransactionCanceledMidFlightLeavesThePoolUsable pins the defect an
// independent review found in the hand-driven transaction. It is a
// database-wide one, not a migration-local one.
//
// Rolling back with the SAME context that just failed does nothing when that
// context is WHY it failed: ExecContext returns immediately on a canceled
// context without sending the statement. The connection then returns to the
// pool still inside a write transaction, so every later BEGIN on that pool
// fails with "cannot start a transaction within a transaction" — while the
// connection holds SQLite's single write lock, which stops the whole database
// accepting writes. sql.Tx.Rollback guards against exactly this; driving the
// transaction by hand moved the obligation onto withImmediateTx.
//
// It drives withImmediateTx directly because the failure has to land BETWEEN
// BEGIN and COMMIT: a context canceled before the call never opens a
// transaction (db.Conn rejects it first), so it would exercise nothing.
//
// The pool is capped at one connection so the damage is observable here rather
// than later — with a larger pool the next checkout may be a different,
// healthy connection, and the poisoned one resurfaces somewhere else entirely.
func TestTransactionCanceledMidFlightLeavesThePoolUsable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "canceled.db")

	db, err := sql.Open(driverName, dsn(path))
	require.NoError(t, err)

	t.Cleanup(func() { require.NoError(t, db.Close()) })

	db.SetMaxOpenConns(1)

	// The log is captured because the two guards here are independent, and
	// asserting only the outcome cannot tell them apart: discarding the
	// connection ALSO leaves the pool healthy, so a rollback that silently
	// did nothing would pass on outcome alone. The warning is what
	// distinguishes "rolled back" from "threw the connection away".
	var logged strings.Builder

	repo, err := New(db, WithLogger(slog.New(
		slog.NewTextHandler(&logged, &slog.HandlerOptions{}))))
	require.NoError(t, err)
	require.NoError(t, repo.Migrate(context.Background()))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// the transaction opens, writes, and then its context dies under it
	canceled := false

	terr := repo.withImmediateTx(ctx, func(conn *sql.Conn) error {
		_, eerr := conn.ExecContext(ctx,
			"INSERT INTO groups (group_name) VALUES ('g')")
		require.NoError(t, eerr,
			"the write must land, or the body returns before cancelling and "+
				"the test exercises an uncanceled rollback")

		cancel()

		canceled = true

		return errors.New("the work failed once its context was gone")
	})
	require.Error(t, terr)
	require.True(t, canceled, "the body must have reached the cancel")

	require.NotContains(t, logged.String(), "rollback failed",
		"the ROLLBACK must actually reach the database — a rollback issued "+
			"on the dead context returns without sending anything, and the "+
			"connection is then only saved by being discarded")

	// The single pooled connection must be usable — it is not if one was
	// returned mid-transaction.
	require.NoError(t, repo.RegisterGroup(context.Background(), "after"),
		"the pool must not hold a connection stuck inside a transaction")

	// and the write lock must have been released, so another pool can write
	fresh, ferr := Open(path)
	require.NoError(t, ferr)

	t.Cleanup(func() { require.NoError(t, fresh.Close()) })

	require.NoError(t, fresh.RegisterGroup(context.Background(), "elsewhere"),
		"the canceled transaction must not still hold the write lock")

	// the rolled-back work must be absent — a poisoned connection would also
	// have left this uncommitted, so assert the rollback, not just liveness
	exists, gerr := repo.GroupExists(context.Background(), "g")
	require.NoError(t, gerr)
	require.False(t, exists, "the failed transaction must have rolled back")
}

// TestOpenMemoryDoesNotWarnAboutWAL: a warning that always fires is not a
// warning.
//
// An in-memory database cannot use WAL at all — it reports journal_mode
// "memory" and there is no file to journal beside — so the concurrency check
// flagged every single OpenMemory call. The reader learns to skip the
// adapter's warnings, and the busy_timeout warning next to it goes with them.
func TestOpenMemoryDoesNotWarnAboutWAL(t *testing.T) {
	var logged strings.Builder

	repo, err := OpenMemory(WithLogger(slog.New(
		slog.NewTextHandler(&logged, &slog.HandlerOptions{}))))
	require.NoError(t, err)

	t.Cleanup(func() { require.NoError(t, repo.Close()) })

	require.NotContains(t, logged.String(), "journal_mode is not WAL",
		"OpenMemory cannot be in WAL, so warning about it every time says "+
			"nothing the caller can act on")
}

// TestAFileWithoutWALStillWarns is the counterpart: silencing the in-memory
// case must not silence the case the check exists for — a host pool on a real
// file, in the rollback-journal default, where readers really do block behind
// a writer.
func TestAFileWithoutWALStillWarns(t *testing.T) {
	db, err := sql.Open(driverName,
		filepath.Join(t.TempDir(), "nowal.db")+"?_pragma=foreign_keys(1)")
	require.NoError(t, err)

	t.Cleanup(func() { require.NoError(t, db.Close()) })

	var logged strings.Builder

	_, err = New(db, WithLogger(slog.New(
		slog.NewTextHandler(&logged, &slog.HandlerOptions{}))))
	require.NoError(t, err)

	require.Contains(t, logged.String(), "journal_mode is not WAL",
		"a file-backed pool outside WAL is exactly what this warns about")
}

// TestIdentityAndLoggerOption covers the two surfaces a host actually sees at
// wiring time: what the adapter calls itself in the engine's startup
// configuration line, and that a supplied logger is accepted.
func TestIdentityAndLoggerOption(t *testing.T) {
	repo, err := OpenMemory(WithLogger(slog.Default()))
	require.NoError(t, err)

	t.Cleanup(func() { require.NoError(t, repo.Close()) })

	require.Equal(t, "sqlite.Repo", repo.String(),
		"the engine prints this in its configuration banner")
}

// TestDefaultTenantIdTakenByANonDefaultTenant covers the edge where the
// group's default cannot be minted because something else already holds the
// id.
//
// It is reachable, and the failure would otherwise be baffling: Save with an
// empty Tenant mints "default" idempotently, then selects the row FLAGGED as
// default. If a regular tenant already occupies that id, the insert no-ops,
// the select finds nothing, and the record has nowhere to go — so the adapter
// must say precisely that rather than surface a bare no-rows error.
func TestDefaultTenantIdTakenByANonDefaultTenant(t *testing.T) {
	repo, err := OpenMemory()
	require.NoError(t, err)

	t.Cleanup(func() { require.NoError(t, repo.Close()) })

	ctx := context.Background()
	require.NoError(t, repo.Migrate(ctx))
	require.NoError(t, repo.RegisterGroup(ctx, "g"))

	// a REGULAR tenant squatting on the default id
	_, err = repo.db.ExecContext(ctx,
		"INSERT INTO tenants (tenant_id, engine_group, name, is_default)"+
			" VALUES ('default', 'g', 'squatter', 0)")
	require.NoError(t, err)

	err = repo.Save(ctx, repository.InstanceRecord{
		ID: "i1", Group: "g", Status: repository.StatusActive,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "non-default tenant",
		"the error must name the cause, not just report a missing row")
}
