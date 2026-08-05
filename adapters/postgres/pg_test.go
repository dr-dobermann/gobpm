package postgres_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib" // the test-only driver (NFR-1)
	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/adapters/postgres"
)

// dsnEnv gates every postgres-backed test (SRD-078 FR-10): unset →
// skip with a pointer to the disposable container target.
const dsnEnv = "GOBPM_PG_TEST_DSN"

var (
	dbOnce   sync.Once
	sharedDB *sql.DB
	dbErr    error

	// schemaSeq mints unique schema names, so every test gets an
	// isolated namespace over the one shared database.
	schemaSeq atomic.Int64
)

// openDB returns the shared pool for the DSN-gated tests, skipping
// when no database is configured.
func openDB(t *testing.T) *sql.DB {
	t.Helper()

	dsn := os.Getenv(dsnEnv)
	if dsn == "" {
		t.Skipf("%s isn't set — run `make pg-up` and export the DSN it prints",
			dsnEnv)
	}

	dbOnce.Do(func() {
		sharedDB, dbErr = sql.Open("pgx", dsn)
		if dbErr == nil {
			dbErr = sharedDB.Ping()
		}
	})
	require.NoError(t, dbErr, "the %s database must be reachable", dsnEnv)

	return sharedDB
}

// newRepo builds a migrated Repo in a fresh schema, dropped at
// cleanup.
func newRepo(t *testing.T) *postgres.Repo {
	t.Helper()

	db := openDB(t)
	schema := fmt.Sprintf("gobpm_test_%d_%d",
		os.Getpid(), schemaSeq.Add(1))

	repo, err := postgres.New(db, postgres.WithSchema(schema))
	require.NoError(t, err)
	require.NoError(t, repo.Migrate(context.Background()))

	t.Cleanup(func() {
		_, err := db.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE")
		require.NoError(t, err)
	})

	return repo
}
