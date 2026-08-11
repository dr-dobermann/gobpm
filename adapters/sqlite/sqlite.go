// Package sqlite provides the embedded Repository adapter (SRD-091,
// ADR-033 v.5): Process Instance checkpoints in a SQLite database the
// adapter migrates itself (renv.Migrator). It implements the full grown
// contract — CAS saves, ownership leases, the group registry and the
// group-scoped recovery listing — and declares itself NOT cluster-safe
// (renv.ClusterAware), because a single-file database cannot honor the
// lease semantics ADR-033 §2.8 gives a store shared between engines.
//
// The driver is modernc.org/sqlite: a pure-Go translation, so the adapter
// needs no CGo and no C toolchain, and cross-compiles like the rest of the
// tree.
//
// # Which constructor
//
// Open owns its pool and is what most callers want:
//
//	repo, err := sqlite.Open("gobpm.db")
//	defer repo.Close()
//
// It exists because SQLite, unlike PostgreSQL, has connection settings this
// adapter's guarantees DEPEND on. PRAGMA foreign_keys is OFF by default and
// applies per connection, so a pool opened the obvious way enforces none of
// the schema's group and tenant constraints. Open builds the DSN that fixes
// that; New, handed a pool it does not own, verifies instead and refuses one
// that cannot honor them.
//
// Set what you own, verify what you are handed.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/renv"
	"github.com/dr-dobermann/gobpm/pkg/repository"

	_ "modernc.org/sqlite" // the pure-Go driver this adapter is built on
)

const errorClass = "SQLITE_REPO"

// driverName is modernc.org/sqlite's registered name.
const driverName = "sqlite"

// probeTimeout bounds the constructor's pragma verification. It is short
// because the probe competes with the HOST for connections of a pool it does
// not own; waiting longer would trade a slow start for a hung one.
const probeTimeout = 2 * time.Second

// requiredPragmas are the connection settings the adapter's guarantees rest
// on. They are DSN parameters rather than post-open statements because
// database/sql opens connections lazily: a PRAGMA executed once after
// sql.Open lands on whichever connection served it, and every connection the
// pool creates afterwards is left unconfigured.
//
//   - foreign_keys makes the schema's REFERENCES real. Without it the DDL
//     parses and enforces nothing, and a record naming an unregistered group
//     is accepted — the exact rule ADR-033 §2.8 asks the store to guarantee.
//   - journal_mode=WAL lets readers proceed while a writer holds the write
//     lock, which is what makes concurrent engine work tolerable at all.
//   - busy_timeout makes a contended writer WAIT rather than fail instantly;
//     SQLite's default is to return SQLITE_BUSY immediately.
var requiredPragmas = []string{
	"foreign_keys(1)",
	"journal_mode(WAL)",
	"busy_timeout(5000)",
}

// Repo is the SQLite repository.Repository. Build it with Open, OpenMemory
// or New.
type Repo struct {
	db     *sql.DB
	logger observability.Logger
	q      queries
	// ownsDB records whether this Repo opened the pool, and therefore
	// whether Close may shut it down. A pool handed to New belongs to the
	// caller, who may be sharing it with other subsystems.
	ownsDB bool
}

// Option configures a Repo at construction.
type Option func(*Repo) error

// WithLogger sets the structured logger (default: slog.Default()).
func WithLogger(l observability.Logger) Option {
	return func(r *Repo) error {
		if l == nil {
			return errs.New(
				errs.M("WithLogger: a nil Logger isn't allowed"),
				errs.C(errorClass, errs.EmptyNotAllowed))
		}

		r.logger = l

		return nil
	}
}

// Open opens (creating it if absent) the SQLite database at path and returns
// a Repo that owns the pool. Close it when done.
//
// The DSN carries the pragmas of requiredPragmas, which is the whole reason
// this constructor exists rather than leaving every caller to assemble one.
func Open(path string, opts ...Option) (*Repo, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errs.New(
			errs.M("Open: an empty database path isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	return openDSN(dsn(path), nil, opts...)
}

// OpenMemory opens a private in-memory database and returns a Repo that owns
// it. The database lives as long as the Repo and is not shared with any other
// connection — which is why the pool is pinned to one connection: a second
// connection to ":memory:" would get its OWN empty database, and the adapter
// would appear to lose every record written through the first.
func OpenMemory(opts ...Option) (*Repo, error) {
	// The limit is applied BEFORE the pool opens anything. Each connection to
	// ":memory:" gets its OWN database, so a pool free to open several would
	// scatter writes across databases that cannot see each other — and the
	// limit must be in place before the constructor's pragma probe runs, or
	// the probe itself creates the extra ones.
	return openDSN(dsn(":memory:"), func(db *sql.DB) {
		db.SetMaxOpenConns(1)
	}, opts...)
}

// openDSN is the shared body of Open and OpenMemory. tune configures the pool
// before any connection exists.
func openDSN(
	dataSource string, tune func(*sql.DB), opts ...Option,
) (*Repo, error) {
	db, err := sql.Open(driverName, dataSource)
	if err != nil {
		return nil, opErr("opening the database", "", err)
	}

	if tune != nil {
		tune(db)
	}

	r, err := newRepo(db, true, opts...)
	if err != nil {
		//nolint:errcheck // the constructor's error is the one to report
		_ = db.Close()

		return nil, err
	}

	return r, nil
}

// dsn builds the connection string for path, appending the required pragmas
// and the transaction mode.
//
// The separator is chosen rather than assumed: SQLite accepts URI filenames
// that already carry parameters ("file:app.db?cache=shared"), and appending a
// second "?" would fold every pragma into the previous parameter's VALUE —
// leaving the settings silently unapplied, which for foreign_keys means the
// schema's constraints quietly stop being enforced.
func dsn(path string) string {
	q := make(url.Values, len(requiredPragmas)+1)
	for _, p := range requiredPragmas {
		q.Add("_pragma", p)
	}

	// Migrations must take the write lock at BEGIN rather than upgrading to
	// it mid-transaction — see Migrate.
	q.Set("_txlock", "immediate")

	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}

	return path + sep + q.Encode()
}

// New wraps an already-open *sql.DB, for a host that manages its own pool.
//
// It VERIFIES the settings Open would have set, and refuses a pool that
// cannot honor the schema's guarantees. Refusing is the right answer here
// precisely because the caller chose the DSN: a repository that silently
// drops a constraint is worse than one that will not start.
func New(db *sql.DB, opts ...Option) (*Repo, error) {
	if db == nil {
		return nil, errs.New(
			errs.M("New: a nil *sql.DB isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	return newRepo(db, false, opts...)
}

// newRepo assembles the Repo and checks the pool it was given.
func newRepo(db *sql.DB, owns bool, opts ...Option) (*Repo, error) {
	r := &Repo{db: db, logger: slog.Default(), ownsDB: owns, q: buildQueries()}

	for _, o := range opts {
		if err := o(r); err != nil {
			return nil, err
		}
	}

	ctx := context.Background()

	if err := r.checkForeignKeys(ctx); err != nil {
		return nil, err
	}

	r.warnMissingConcurrencyPragmas(ctx)

	return r, nil
}

// checkForeignKeys asserts that foreign-key enforcement is on.
//
// It probes on SEVERAL connections, not one. The setting is per connection,
// so a single probe can pass on a connection that happens to be configured
// while others in the same pool are not — which is the failure this check
// exists to catch, and a one-connection probe would report as healthy.
func (r *Repo) checkForeignKeys(ctx context.Context) error {
	probes := 4

	// Never ask for more connections than the pool may open. A pool capped at
	// one — which OpenMemory needs — would otherwise block on the second
	// probe, since this holds each connection while acquiring the next.
	if maxConns := r.db.Stats().MaxOpenConnections; maxConns > 0 &&
		maxConns < probes {
		probes = maxConns
	}

	// And never wait indefinitely for one. New may be handed a pool the HOST
	// is using, whose connections are checked out elsewhere; holding what we
	// get while blocking for more would hang the constructor, and a
	// repository that never returns is worse than one that verifies less.
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	conns := make([]*sql.Conn, 0, probes)

	defer func() {
		for _, c := range conns {
			//nolint:errcheck // releasing probes back to the pool, best effort
			_ = c.Close()
		}
	}()

	for i := range probes {
		// Hold each connection while probing the next, so the pool must hand
		// out a DIFFERENT one each time instead of returning the same idle
		// connection four times over.
		c, err := r.db.Conn(ctx)
		if err != nil {
			if i == 0 {
				return opErr("acquiring a connection", "", err)
			}

			// The pool would not give us another within the timeout — the
			// host is using them. What we could reach was configured, which
			// is the most this constructor can honestly assert.
			r.logger.Debug("sqlite: verified foreign keys on a subset of the"+
				" pool", "checked", i, "wanted", probes)

			return nil
		}

		conns = append(conns, c)

		var on int
		if err := c.QueryRowContext(
			ctx, "PRAGMA foreign_keys",
		).Scan(&on); err != nil {
			return opErr("reading PRAGMA foreign_keys", "", err)
		}

		if on != 1 {
			return errs.New(
				errs.M("foreign-key enforcement is off on pooled connection "+
					"%d of %d: the schema's group and tenant constraints "+
					"would not hold. Open the database with sqlite.Open, or "+
					"add %q to the DSN", i+1, probes, "_pragma=foreign_keys(1)"),
				errs.C(errorClass, errs.InvalidState))
		}
	}

	return nil
}

// warnMissingConcurrencyPragmas reports, without refusing, a pool that lacks
// the settings which make contention survivable.
//
// The asymmetry with foreign_keys is deliberate and is about what each pragma
// costs to get wrong. foreign_keys decides whether the schema's constraints
// exist at all, so a pool without it is REFUSED — the store would silently
// accept rows it must reject. WAL and busy_timeout decide how the store
// behaves when two writers meet: without them a contended write fails
// immediately with SQLITE_BUSY instead of waiting. That is a degraded store,
// not an incorrect one, and refusing a host's pool over it would be this
// adapter overruling a choice that is legitimately the host's.
func (r *Repo) warnMissingConcurrencyPragmas(ctx context.Context) {
	var mode string
	if err := r.db.QueryRowContext(
		ctx, "PRAGMA journal_mode",
	).Scan(&mode); err == nil && !acceptableJournalMode(mode) {
		r.logger.Warn("sqlite: journal_mode is not WAL; readers will block "+
			"behind a writer", "journal_mode", mode)
	}

	var busy int
	if err := r.db.QueryRowContext(
		ctx, "PRAGMA busy_timeout",
	).Scan(&busy); err == nil && busy == 0 {
		r.logger.Warn("sqlite: busy_timeout is 0; a contended write will fail " +
			"immediately instead of waiting")
	}
}

// journalModesNotWorthWarningAbout are the modes for which "this is not WAL"
// is not news.
//
// An in-memory database CANNOT be in WAL — WAL needs a file to journal
// alongside — so OpenMemory would warn on every single call. A warning that
// always fires is not a warning; it teaches the reader to skip the adapter's
// output, which costs more than the one it was meant to raise.
var journalModesNotWorthWarningAbout = map[string]struct{}{
	"wal":    {}, // what the check wants
	"memory": {}, // an in-memory database, where WAL is not available at all
}

// acceptableJournalMode reports whether mode should pass without a warning.
func acceptableJournalMode(mode string) bool {
	_, ok := journalModesNotWorthWarningAbout[strings.ToLower(mode)]

	return ok
}

// ClusterCompatibility declares the adapter unsafe to share between engines
// (renv.ClusterAware).
//
// SQLite permits one writer at a time over a local file; ADR-033 §2.8's
// claim-first recovery assumes a store several engines write to concurrently.
// An operator wanting cluster mode selects a different Repository adapter.
func (r *Repo) ClusterCompatibility() (bool, string) {
	return false, "embedded single-writer SQLite database; ADR-033 §2.8 " +
		"cluster semantics need a shared store — use adapters/postgres"
}

// Close releases the pool, if this Repo opened it. A Repo built by New does
// not close a pool it does not own.
func (r *Repo) Close() error {
	if !r.ownsDB {
		return nil
	}

	if err := r.db.Close(); err != nil {
		return opErr("closing the database", "", err)
	}

	return nil
}

// String identifies the adapter in the engine's startup configuration line.
func (r *Repo) String() string { return "sqlite.Repo" }

// opErr wraps a database failure into the classified error idiom; id tags the
// record when the operation has one ("" omits the detail).
func opErr(op, id string, err error) error {
	if id == "" {
		return errs.New(
			errs.M("%s failed", op),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err))
	}

	return errs.New(
		errs.M("%s failed", op),
		errs.C(errorClass, errs.OperationFailed),
		errs.D("id", id),
		errs.E(err))
}

// interface checks.
//
// The two renv capabilities are asserted here on purpose: they are OPTIONAL
// and satisfied structurally, so the engine detects them by type assertion
// and simply does not call a Repo whose signature has drifted. Nothing would
// fail — Migrate would stop running and the cluster verdict would stop being
// asked — which is why FR-4 and FR-5 need a compile-time claim rather than a
// method that merely looks right.
var (
	_ repository.Repository = (*Repo)(nil)
	_ renv.ClusterAware     = (*Repo)(nil)
	_ renv.Migrator         = (*Repo)(nil)
	_ fmt.Stringer          = (*Repo)(nil)
)
