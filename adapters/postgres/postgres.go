// Package postgres provides the durable Repository adapter (SRD-078,
// ADR-033 v.3): Process Instance checkpoints in a user-owned PostgreSQL
// database, under a namespaced schema the adapter migrates itself
// (renv.Migrator). The adapter implements the full grown contract —
// CAS saves, ownership leases, the group registry and group-scoped
// recovery listing — and declares itself cluster-compatible
// (renv.ClusterAware): fencing is the database's CAS, shared by every
// engine over the same store.
//
// The module imports database/sql only; the driver is the embedder's
// choice (tests use jackc/pgx/v5/stdlib).
package postgres

import (
	"database/sql"
	"fmt"
	"log/slog"
	"regexp"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/renv"
	"github.com/dr-dobermann/gobpm/pkg/repository"
)

const errorClass = "POSTGRES_REPO"

// DefaultSchema is the schema the adapter's objects live in unless
// WithSchema overrides it.
const DefaultSchema = "gobpm"

// schemaRx validates a schema name as a plain lowercase identifier —
// the name is interpolated into DDL/queries, so nothing else may pass.
var schemaRx = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// Repo is the PostgreSQL repository.Repository. Build it with New.
type Repo struct {
	db     *sql.DB
	logger observability.Logger
	schema string
	q      queries
}

// Option configures a Repo at New.
type Option func(*Repo) error

// WithSchema overrides the namespaced schema the adapter's objects
// live in (default: DefaultSchema). The name must be a plain lowercase
// identifier.
func WithSchema(name string) Option {
	return func(r *Repo) error {
		if !schemaRx.MatchString(name) {
			return errs.New(
				errs.M("WithSchema: %q isn't a plain lowercase identifier",
					name),
				errs.C(errorClass, errs.InvalidParameter))
		}

		r.schema = name

		return nil
	}
}

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

// New builds a Repo over the user-owned database handle (ADR-033 §2.7
// — the pool, the credentials and the driver belong to the embedder).
// Run Migrate (or wire the Repo into thresher, which migrates at Run)
// before the first Save.
func New(db *sql.DB, opts ...Option) (*Repo, error) {
	if db == nil {
		return nil, errs.New(
			errs.M("New: a nil *sql.DB isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	r := &Repo{
		db:     db,
		logger: slog.Default(),
		schema: DefaultSchema,
	}

	for _, o := range opts {
		if err := o(r); err != nil {
			return nil, err
		}
	}

	r.q = buildQueries(r.schema)

	return r, nil
}

// ClusterCompatibility declares the adapter safe to share between
// engines (renv.ClusterAware, SRD-078 FR-3).
func (r *Repo) ClusterCompatibility() (bool, string) {
	return true, "shared PostgreSQL store; CAS+lease fencing per ADR-033 §2.8"
}

// t qualifies a table name with the configured schema.
func (r *Repo) t(table string) string {
	return r.schema + "." + table
}

// opErr wraps a database failure into the classified error idiom; id
// tags the record when the operation has one ("" omits the detail).
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

var (
	_ repository.Repository = (*Repo)(nil)
	_ renv.ClusterAware     = (*Repo)(nil)
	_ renv.Migrator         = (*Repo)(nil)
	_ fmt.Stringer          = (*Repo)(nil)
)

// String identifies the adapter and its schema in logs.
func (r *Repo) String() string {
	return "postgres repository (schema " + r.schema + ")"
}

// Schema returns the schema the adapter's objects live in.
func (r *Repo) Schema() string { return r.schema }
