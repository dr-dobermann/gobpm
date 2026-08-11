package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"embed"
	"regexp"
	"sort"
	"strconv"

	"github.com/dr-dobermann/gobpm/pkg/errs"
)

// migrationsFS embeds the versioned SQL migrations: NNNN_*.sql, applied in
// order, each in its own transaction, recorded in schema_version by number.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// migration is one embedded migration file.
type migration struct {
	name    string
	version int
}

// migRx pins the migration naming convention: NNNN_*.sql.
var migRx = regexp.MustCompile(`^(\d{4})_.+\.sql$`)

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

// parseMigrations validates the NNNN_*.sql naming and sorts by version.
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
			return nil, errs.Invariant(
				"migration %q: version prefix: %v", name, err)
		}

		mm = append(mm, migration{name: name, version: v})
	}

	sort.Slice(mm, func(i, j int) bool { return mm[i].version < mm[j].version })

	return mm, nil
}

// Migrate creates or upgrades the adapter's objects (renv.Migrator): the
// schema_version ledger, then every pending migration, each in one
// transaction that re-checks the current version. Re-running over an
// up-to-date database is a no-op.
//
// There is no advisory lock, unlike the postgres adapter: SQLite permits a
// single writer, so two engines migrating one file serialize on the database
// itself. That holds only because each transaction is opened with an explicit
// BEGIN IMMEDIATE (see withImmediateTx), never on the DSN's _txlock — which
// this adapter cannot verify on a pool it was handed.
//
// The default is DEFERRED, under which a transaction takes no write lock at
// BEGIN. Two concurrent migrators would then both read the current version
// under a shared lock and both try to upgrade while the other holds one,
// which SQLite cannot grant — they deadlock rather than serialize, and the
// loser gets SQLITE_BUSY rather than waiting its turn. IMMEDIATE takes the
// write lock up front, which is what turns "one writer" into "one at a
// time".
func (r *Repo) Migrate(ctx context.Context) error {
	if _, err := r.db.ExecContext(ctx,
		"CREATE TABLE IF NOT EXISTS schema_version ("+
			" version INTEGER PRIMARY KEY,"+
			" applied_at TEXT NOT NULL"+
			" DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')))"); err != nil {
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

// withImmediateTx runs body inside a BEGIN IMMEDIATE transaction on ONE
// connection, committing if body returns nil and rolling back otherwise.
//
// The transaction is driven with explicit statements rather than through
// sql.Tx because BEGIN IMMEDIATE is the whole point and database/sql gives no
// way to choose the BEGIN it issues. Relying on the DSN's _txlock instead
// would make serialization conditional on a flag this adapter may not have
// written: New is handed pools whose connection string it cannot inspect,
// since _txlock is a driver parameter rather than a PRAGMA. The failure mode
// would be two engines deadlocking at boot, which is a poor way to learn about
// a missing flag.
//
// Hand-driving it also means hand-handling what sql.Tx.Rollback does for free
// — see the rollback below.
func (r *Repo) withImmediateTx(
	ctx context.Context,
	body func(conn *sql.Conn) error,
) error {
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return opErr("acquiring a migration connection", "", err)
	}

	poisoned := false

	defer func() {
		// A connection still inside a transaction must NOT go back to the
		// pool. database/sql pools whatever Close hands back, and the next
		// checkout would then fail every BEGIN with "cannot start a
		// transaction within a transaction" — while holding SQLite's single
		// write lock, so the whole database stops accepting writes. Raw with a
		// driver.ErrBadConn is how a connection is discarded rather than
		// returned.
		if poisoned {
			//nolint:errcheck // discarding a connection we must not pool
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}

		//nolint:errcheck // returning the connection to the pool
		_ = conn.Close()
	}()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return opErr("beginning a migration transaction", "", err)
	}

	if err := body(conn); err != nil {
		r.rollback(ctx, conn, &poisoned)

		return err
	}

	if _, cerr := conn.ExecContext(ctx, "COMMIT"); cerr != nil {
		r.rollback(ctx, conn, &poisoned)

		return opErr("committing the migration transaction", "", cerr)
	}

	return nil
}

// rollback ends an uncommitted transaction, flagging the connection as unfit
// for the pool when it cannot.
//
// It rolls back under WithoutCancel, because the usual reason to be rolling
// back at all is that ctx died — and ExecContext on a canceled context returns
// immediately WITHOUT sending the statement, which would leave the transaction
// open on a connection about to be pooled.
func (r *Repo) rollback(ctx context.Context, conn *sql.Conn, poisoned *bool) {
	if _, err := conn.ExecContext(
		context.WithoutCancel(ctx), "ROLLBACK",
	); err != nil {
		*poisoned = true

		r.logger.Warn("migration rollback failed; discarding the connection "+
			"rather than returning an open transaction to the pool",
			"error", err.Error())
	}
}

// applyNext applies the single next pending migration, reporting whether one
// was applied (false: up to date).
func (r *Repo) applyNext(ctx context.Context, mm []migration) (bool, error) {
	applied := false

	err := r.withImmediateTx(ctx, func(conn *sql.Conn) error {
		var current int
		if serr := conn.QueryRowContext(ctx,
			"SELECT COALESCE(MAX(version), 0) FROM schema_version").
			Scan(&current); serr != nil {
			return opErr("reading the current schema version", "", serr)
		}

		next, ok := nextPending(mm, current)
		if !ok {
			return nil
		}

		body, rerr := migrationsFS.ReadFile("migrations/" + next.name)
		if rerr != nil {
			return opErr("reading migration "+next.name, "", rerr)
		}

		if _, aerr := conn.ExecContext(ctx, string(body)); aerr != nil {
			return opErr("applying migration "+next.name, "", aerr)
		}

		if _, ierr := conn.ExecContext(ctx,
			"INSERT INTO schema_version (version) VALUES (?)",
			next.version); ierr != nil {
			return opErr("recording migration "+next.name, "", ierr)
		}

		applied = true

		r.logger.Info("sqlite migration applied",
			"migration", next.name, "version", next.version)

		return nil
	})
	if err != nil {
		return false, err
	}

	return applied, nil
}

// nextPending returns the lowest migration above current.
func nextPending(mm []migration, current int) (migration, bool) {
	for _, m := range mm {
		if m.version > current {
			return m, true
		}
	}

	return migration{}, false
}
