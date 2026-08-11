package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/adapters/sqlite"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/repository"
)

// TestNewRefusesAPoolWithForeignKeysOff is T-4: New verifies what it cannot
// set, and refuses rather than running with the schema's constraints inert.
//
// This is the half of §3.1 that Open does not cover — a host handing over a
// pool it built itself.
func TestNewRefusesAPoolWithForeignKeysOff(t *testing.T) {
	// deliberately no _pragma=foreign_keys(1): the naive DSN
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "naive.db"))
	require.NoError(t, err)

	t.Cleanup(func() { require.NoError(t, db.Close()) })

	_, err = sqlite.New(db)
	require.Error(t, err,
		"a pool that cannot enforce the schema's constraints must be refused")
	require.Contains(t, err.Error(), "foreign-key enforcement is off")
	require.Contains(t, err.Error(), "sqlite.Open",
		"the error must name the fix, not just the fault")
}

// TestNewAcceptsAProperlyConfiguredPool is T-4's counterpart: the refusal
// above must be about the pragma, not about New rejecting everything.
func TestNewAcceptsAProperlyConfiguredPool(t *testing.T) {
	db, err := sql.Open("sqlite",
		filepath.Join(t.TempDir(), "ok.db")+"?_pragma=foreign_keys(1)")
	require.NoError(t, err)

	t.Cleanup(func() { require.NoError(t, db.Close()) })

	repo, err := sqlite.New(db)
	require.NoError(t, err)
	require.NoError(t, repo.Migrate(context.Background()))

	// New does not own the pool, so Close must leave it usable.
	require.NoError(t, repo.Close())
	require.NoError(t, db.PingContext(context.Background()),
		"New must not close a pool it does not own")
}

// TestClusterCompatibilityReportsUnsafe is T-7 (FR-5): the engine learns the
// limit from the adapter, not from documentation.
func TestClusterCompatibilityReportsUnsafe(t *testing.T) {
	repo, err := sqlite.OpenMemory()
	require.NoError(t, err)

	t.Cleanup(func() { require.NoError(t, repo.Close()) })

	ok, reason := repo.ClusterCompatibility()
	require.False(t, ok, "an embedded single-writer store is not cluster-safe")
	require.Contains(t, reason, "single-writer")
	require.Contains(t, reason, "postgres",
		"the reason should point at the adapter that IS cluster-safe")
}

// TestMigrateIsIdempotent is T-5 (FR-4).
func TestMigrateIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mig.db")

	repo, err := sqlite.Open(path)
	require.NoError(t, err)

	t.Cleanup(func() { require.NoError(t, repo.Close()) })

	ctx := context.Background()
	require.NoError(t, repo.Migrate(ctx))
	require.NoError(t, repo.Migrate(ctx), "a second Migrate must be a no-op")

	// and the data survives a re-run
	require.NoError(t, repo.RegisterGroup(ctx, "g"))
	require.NoError(t, repo.Migrate(ctx))

	exists, err := repo.GroupExists(ctx, "g")
	require.NoError(t, err)
	require.True(t, exists, "re-migrating must not drop data")
}

// TestLeaseExpiryOrdersLexicographically is T-8: the §3.2 encoding claim.
//
// lease_expiry is TEXT, and ListInFlight compares it with `<=`. That is only
// a chronological comparison if the encoding is fixed-width UTC — which is
// why the layout pins nine fractional digits rather than using
// time.RFC3339Nano, whose trimmed fractions sort wrongly against each other.
func TestLeaseExpiryOrdersLexicographically(t *testing.T) {
	repo, err := sqlite.OpenMemory()
	require.NoError(t, err)

	t.Cleanup(func() { require.NoError(t, repo.Close()) })

	ctx := context.Background()
	require.NoError(t, repo.Migrate(ctx))
	require.NoError(t, repo.RegisterGroup(ctx, "g"))

	base := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)

	// .5s and .45s: RFC3339Nano renders these ".5" and ".45", which compare
	// in the WRONG order as text even though .45 < .5 chronologically.
	expired := base.Add(500 * time.Millisecond)
	live := base.Add(450 * time.Millisecond)

	require.NoError(t, repo.Save(ctx, repository.InstanceRecord{
		ID: "expired", Group: "g", Status: repository.StatusActive,
		Lease: repository.Lease{Owner: "e1", Expiry: expired},
	}))
	require.NoError(t, repo.Save(ctx, repository.InstanceRecord{
		ID: "live", Group: "g", Status: repository.StatusActive,
		Lease: repository.Lease{Owner: "e2", Expiry: live.Add(time.Hour)},
	}))

	// now sits between the two expiries: "expired" is claimable, "live" is not.
	ids, err := repo.ListInFlight(ctx, "g", expired.Add(time.Millisecond))
	require.NoError(t, err)
	require.Equal(t, []string{"expired"}, ids,
		"the lease comparison must be chronological, not textual")

	// and the round-trip preserves the instant
	rec, ok, err := repo.Load(ctx, "expired")
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, rec.Lease.Expiry.Equal(expired),
		"lease expiry round-tripped as %v, want %v", rec.Lease.Expiry, expired)
}

// TestConcurrentSaveSerializes is T-6, and it exists because
// repositorytest.Conformance CANNOT cover this: the published suite is
// sequential, so a green run says nothing about concurrent Save — which is
// exactly SQLite's defining constraint (SRD-091 §4.2).
//
// Two claims are made. Distinct records written at once all land, because WAL
// plus a busy timeout turns single-writer contention into waiting rather than
// SQLITE_BUSY. And CAS still fences under that contention: of many writers
// racing on ONE record from the same stored version, exactly one wins.
func TestConcurrentSaveSerializes(t *testing.T) {
	repo, err := sqlite.Open(filepath.Join(t.TempDir(), "conc.db"))
	require.NoError(t, err)

	t.Cleanup(func() { require.NoError(t, repo.Close()) })

	ctx := context.Background()
	require.NoError(t, repo.Migrate(ctx))
	require.NoError(t, repo.RegisterGroup(ctx, "g"))

	const writers = 8

	t.Run("distinct records all land", func(t *testing.T) {
		var wg sync.WaitGroup

		errCh := make(chan error, writers)

		for i := range writers {
			wg.Add(1)

			go func(n int) {
				defer wg.Done()

				errCh <- repo.Save(ctx, repository.InstanceRecord{
					ID:     fmt.Sprintf("rec-%d", n),
					Group:  "g",
					Status: repository.StatusActive,
				})
			}(i)
		}

		wg.Wait()
		close(errCh)

		for e := range errCh {
			require.NoError(t, e,
				"a contended writer must wait, not fail with SQLITE_BUSY")
		}

		ids, lerr := repo.ListInFlight(ctx, "g", time.Now())
		require.NoError(t, lerr)
		require.Len(t, ids, writers, "every concurrent write must be durable")
	})

	t.Run("CAS fences under contention", func(t *testing.T) {
		require.NoError(t, repo.Save(ctx, repository.InstanceRecord{
			ID: "contended", Group: "g", Status: repository.StatusActive,
		}))

		rec, ok, lerr := repo.Load(ctx, "contended")
		require.NoError(t, lerr)
		require.True(t, ok)

		var (
			wg       sync.WaitGroup
			won      atomic.Int32
			rejected atomic.Int32
		)

		// every writer starts from the SAME stored version
		for range writers {
			wg.Add(1)

			go func() {
				defer wg.Done()

				err := repo.Save(ctx, rec)
				if err == nil {
					won.Add(1)

					return
				}

				var ae *errs.ApplicationError
				if errors.As(err, &ae) && ae.HasClass(errs.ConcurrentUpdate) {
					rejected.Add(1)

					return
				}

				t.Errorf("unexpected Save failure: %v", err)
			}()
		}

		wg.Wait()

		require.EqualValues(t, 1, won.Load(),
			"exactly one writer may win a CAS race")
		require.EqualValues(t, writers-1, rejected.Load(),
			"every loser must be fenced with ConcurrentUpdate, not silently "+
				"dropped or promoted")
	})
}

// TestNewOnABusyHostPoolDoesNotHang is the regression pin for the blocker the
// independent review found: the constructor's pragma probe held each
// connection while acquiring the next, on an unbounded context.
//
// Handed a pool the HOST is using — the exact case New exists for — it would
// wait forever for a connection that was checked out elsewhere, and a
// repository that never returns is worse than one that verifies less. The
// probe now runs under a timeout and asserts what it could reach.
func TestNewOnABusyHostPoolDoesNotHang(t *testing.T) {
	db, err := sql.Open("sqlite",
		filepath.Join(t.TempDir(), "busy.db")+"?_pragma=foreign_keys(1)")
	require.NoError(t, err)

	t.Cleanup(func() { require.NoError(t, db.Close()) })

	// a small pool, with every connection but one already in the host's hands
	db.SetMaxOpenConns(2)

	ctx := context.Background()

	held, err := db.Conn(ctx)
	require.NoError(t, err)

	t.Cleanup(func() { require.NoError(t, held.Close()) })

	done := make(chan error, 1)

	go func() {
		_, nerr := sqlite.New(db)
		done <- nerr
	}()

	select {
	case nerr := <-done:
		require.NoError(t, nerr,
			"a pool whose connections are busy elsewhere is still usable")
	case <-time.After(30 * time.Second):
		t.Fatal("New hung waiting for connections the host is using")
	}
}
