package postgres

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"regexp"
	"sort"
	"strconv"

	"github.com/dr-dobermann/gobpm/pkg/errs"
)

// migrationsFS embeds the versioned SQL migrations (SRD-078 FR-5):
// NNNN_*.sql, applied in order, each in its own transaction, recorded
// in schema_version by number.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// migration is one embedded migration file.
type migration struct {
	name    string
	version int
}

// loadMigrations lists the embedded migrations sorted by version.
func loadMigrations() ([]migration, error) {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return nil, opErr("listing the embedded migrations", "", err)
	}

	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}

	return parseMigrations(names)
}

// migRx pins the migration naming convention: NNNN_*.sql.
var migRx = regexp.MustCompile(`^(\d{4})_.+\.sql$`)

// parseMigrations validates the NNNN_*.sql naming and sorts by the
// version prefix.
func parseMigrations(names []string) ([]migration, error) {
	mm := make([]migration, 0, len(names))

	for _, name := range names {
		m := migRx.FindStringSubmatch(name)
		if m == nil {
			return nil, errs.New(
				errs.M("migration %q isn't named NNNN_*.sql", name),
				errs.C(errorClass, errs.InvalidObject))
		}

		v, err := strconv.Atoi(m[1])
		if err != nil { // the regex guarantees digits
			return nil, errs.Invariant("migration %q: version prefix: %v", name, err)
		}

		mm = append(mm, migration{name: name, version: v})
	}

	sort.Slice(mm, func(i, j int) bool {
		return mm[i].version < mm[j].version
	})

	return mm, nil
}

// Migrate creates or upgrades the adapter's objects (renv.Migrator,
// SRD-078 FR-5): the schema itself, the schema_version ledger, then
// every pending migration — each in one transaction that holds an
// advisory lock and re-checks the current version, so concurrently
// booting engines serialize instead of colliding on DDL. Re-running
// over an up-to-date schema is a no-op.
func (r *Repo) Migrate(ctx context.Context) error {
	if _, err := r.db.ExecContext(ctx,
		"CREATE SCHEMA IF NOT EXISTS "+r.schema); err != nil {
		return opErr("creating schema "+r.schema, "", err)
	}

	if _, err := r.db.ExecContext(ctx,
		"CREATE TABLE IF NOT EXISTS "+r.t("schema_version")+
			" (version integer PRIMARY KEY,"+
			" applied_at timestamptz NOT NULL DEFAULT now())"); err != nil {
		return opErr("creating the schema_version ledger", "", err)
	}

	mm, err := loadMigrations()
	if err != nil {
		return err
	}

	for {
		applied, err := r.applyNext(ctx, mm)
		if err != nil {
			return err
		}

		if !applied {
			return nil
		}
	}
}

// applyNext applies the single next pending migration under the
// advisory lock, reporting whether one was applied (false: up to date).
func (r *Repo) applyNext(ctx context.Context, mm []migration) (bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, opErr("beginning a migration transaction", "", err)
	}
	defer func() {
		if rbErr := tx.Rollback(); rbErr != nil &&
			!errors.Is(rbErr, sql.ErrTxDone) {
			r.logger.Warn("migration rollback failed", "error", rbErr.Error())
		}
	}()

	// serialize concurrent migrators of THIS schema; the lock releases
	// with the transaction.
	if _, lerr := tx.ExecContext(ctx,
		"SELECT pg_advisory_xact_lock(hashtextextended($1, 0))",
		"gobpm:"+r.schema); lerr != nil {
		return false, opErr("taking the migration lock", "", lerr)
	}

	var current int
	if serr := tx.QueryRowContext(ctx,
		"SELECT COALESCE(MAX(version), 0) FROM "+r.t("schema_version")).
		Scan(&current); serr != nil {
		return false, opErr("reading the current schema version", "", serr)
	}

	next, ok := nextPending(mm, current)
	if !ok {
		return false, nil
	}

	body, err := migrationsFS.ReadFile("migrations/" + next.name)
	if err != nil {
		return false, opErr("reading migration "+next.name, "", err)
	}

	if _, err := tx.ExecContext(ctx,
		"SET LOCAL search_path TO "+r.schema); err != nil {
		return false, opErr("setting the migration search_path", "", err)
	}

	if _, err := tx.ExecContext(ctx, string(body)); err != nil {
		return false, opErr("applying migration "+next.name, "", err)
	}

	if _, err := tx.ExecContext(ctx,
		"INSERT INTO schema_version (version) VALUES ($1)",
		next.version); err != nil {
		return false, opErr("recording migration "+next.name, "", err)
	}

	if err := tx.Commit(); err != nil {
		return false, opErr("committing migration "+next.name, "", err)
	}

	r.logger.Info("postgres repository migration applied",
		"schema", r.schema, "migration", next.name)

	return true, nil
}

// nextPending returns the first migration above the current version.
func nextPending(mm []migration, current int) (migration, bool) {
	for _, m := range mm {
		if m.version > current {
			return m, true
		}
	}

	return migration{}, false
}
