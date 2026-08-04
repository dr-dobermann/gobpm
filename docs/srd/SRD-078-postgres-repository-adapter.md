# SRD-078 — The postgres Repository adapter, engine groups and tenant-ready storage

| Field | Value |
|---|---|
| Status | Draft |
| Date | 2026-08-04 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-033 v.3](../design/ADR-033-persistence-and-state.md) §2.7 (the storage-composition rule, the `Migrator` capability, the tenant-linkage principle), §2.8 (engine groups, CAS fencing, claim-first wake) |
| Upstream | [ADR-002 v.2](../design/ADR-002-extension-architecture.md) §8.3 (optional capability interfaces), §8.4 (adapter conformance testing), [ADR-003 v.1](../design/ADR-003-module-layout.md) §4.4 (adapter modules, no cross-module imports) |
| Related | [SRD-070](SRD-070-instance-checkpoint-and-restart-recovery.md) (the record this adapter stores), [SRD-071 v.2.5](SRD-071-instance-dehydration-and-wake-on-trigger.md) (the wake claim path this group-scopes) |
| Tracking | #276 |

The first **second implementation** of the Repository port: a durable,
PostgreSQL-backed instance-checkpoint store in its own
`adapters/postgres` module — plus everything validating a port's
second implementation shakes loose: the **engine-group** partition on
the port (ADR-033 v.3 §2.8), the **tenant linkage** in the schema
(§2.7), the `ClusterAware`/`Migrator` capability interfaces that so
far existed only in ADR-002's prose, and the **Repository conformance
suite** that makes the next adapter cheap.

## §1 Background (verified)

- **The port today** (`pkg/repository/repository.go:74`):
  `Save`/`Load`/`Delete`/`ListInFlight` over
  `InstanceRecord{ID, Payload, Lease, RecVersion, Status}` — no group,
  no tenant. `Save` is CAS on `RecVersion` (mismatch →
  `errs.ConcurrentUpdate`, `pkg/errs/errors.go:54`); `ListInFlight`
  returns non-terminal records with expired leases.
- **One implementation exists**: `memrepo`
  (`pkg/repository/memrepo/memrepo.go`) — in-memory, value copies,
  terminal-record eviction cap. Its known defect (audit
  `docs/audit/remediation-status.md` row 11, deferred "until
  persistence lands"): a terminal→Active re-save leaves the id in
  `termSet`/`termOrder` (`memrepo.go:87` adds, nothing removes on a
  non-terminal re-save), so `evictTerminalLocked` can delete an
  **Active** record.
- **The engine writes records in three places**, all stamping lease
  owner `t.id`/`inst.cpOwner`: the loop checkpoint
  (`internal/instance/checkpoint_capture.go:98`), the recovery claim
  (`pkg/thresher/recovery.go:53`), the wake claim
  (`pkg/thresher/wake.go:249`). A claim is a CAS `Save` under
  incarnation+1 — the port has no separate claim method, CAS **is** the
  fencing.
- **The engine has an id but no group**: `thresher.New(id, …)`
  (`pkg/thresher/thresher.go:206`); lease TTL via `WithLeaseTTL`
  (`pkg/thresher/options.go:387`, default 30s).
- **`ClusterAware` and the conformance helper exist only on paper**:
  ADR-002 §8.3/§8.4 sketch them; `grep -rn ClusterCompatibility
  --include='*.go'` is empty; `pkg/renv`'s doc anticipates the hooks
  as "deferred" (`engineruntime.go:24`). The `Migrator` convention
  (ADR-033 §2.7) likewise has no code.
- **Adapter-module conventions** (from `adapters/dtable`,
  `adapters/lua`): own `go.mod` with `replace
  github.com/dr-dobermann/gobpm => ../..`, toolchain `go1.25.12`;
  `Makefile` auto-discovers any `go.mod` into `CORE_MODULES`
  (`Makefile:77`), so a new adapter module is tidy/lint/build/test/
  cover/vuln-gated the day it appears. `adapters/sqlite` is a
  scaffold reservation (doc.go + go.mod only) — out of scope here, its
  own flow.
- **Docs to sync**: `docs/guides/operating/persistence.md`,
  `docs/guides/extending/repository.md`, `CHANGELOG.md` `[Unreleased]`.

## §2 Requirements

### §2.1 Functional

- **FR-1 — the port grows the partitions** (`pkg/repository`).
  `InstanceRecord` gains `Group string` (the creator engine's group,
  ADR-033 v.3 §2.8 — **never empty**: an ungrouped engine's group is
  its own engine id; adapters reject an empty group loud) and `Tenant
  string` (the owning tenant's id, §2.7; `""` = the default tenant).
  `ListInFlight` becomes group-scoped: `ListInFlight(ctx, group
  string, now time.Time)` — a non-empty `group`, only claimable
  records of that group return. Claimable is defined **by exclusion**
  (non-terminal AND not suspended AND lease-expired), never by
  enumerating in-flight statuses — the status vocabulary is
  append-only, and a new non-terminal member (the parallel incidents
  work, SRD-079 Draft, adds `StatusActiveIncidents`) must list without
  every store changing. The port also grows the **group registry**
  (ADR-033 v.3 §2.8): `RegisterGroup(ctx, group)` (idempotent
  establish) and `GroupExists(ctx, group) (bool, error)`; `Save` MUST
  reject a record whose group is not registered — records reference
  established groups only; `ListInFlight` over an unregistered group
  returns empty, not an error.
- **FR-2 — the engine carries its group, solo by default.** Two
  options: `thresher.WithEngineGroup(name string)` — join,
  **establishing** the group if absent — and
  `thresher.WithExistingEngineGroup(name string)` — join only: at
  `Run` (before recovery) the engine calls `GroupExists`, and an
  absent group is a loud startup failure naming it (the typo-guard:
  a misspelled group must refuse, not silently mint a fresh
  partition). Both trim; empty after trim is rejected; setting both
  (or either twice) is rejected. Unset → the engine's group **is its
  engine id** (ADR-033 v.3 §2.8: clustering is explicit opt-in, never
  accidental), registered idempotently at `Run`; the engine id is
  thereby documented as a stable, operator-chosen identity. The group
  is stamped on every record the engine writes (checkpoint
  capture, recovery claim, wake claim) and passed to `ListInFlight` at
  recovery. Every claim path verifies the loaded record's group equals
  the engine's — a cross-group record reached by id is **refused loud**
  (a wiring mistake, not a race). The engine stamps `Tenant: ""` (the
  default tenant) until the Multi-tenancy ADR wires real assignment.
- **FR-3 — the capability interfaces** (in `pkg/renv` — the
  engine↔extension contract package, whose doc already anticipates the
  ADR-002 §8.3 hooks, `pkg/renv/engineruntime.go:24`; a unified
  `pkg/extension` was **explicitly rejected** by ADR-003 §3.1):
  `ClusterAware` (`ClusterCompatibility() (bool, string)`)
  and `Migrator` (`Migrate(ctx context.Context) error` — "prepare your
  own objects", ADR-033 §2.7). Adapters satisfy them structurally — no
  import needed to implement. `thresher.Run` walks its wired
  repository before recovery: implements `Migrator` → call it, an
  error aborts `Run` loud (a half-created schema must never look like
  an empty store). `memrepo` declares `(false, "in-memory; state is
  not shared across nodes")`. The runtime-level cluster-mode *check*
  stays with the Distribution & Scale ADR — this SRD lands the
  vocabulary and the declarations.
- **FR-4 — the `adapters/postgres` module.** `postgres.New(db *sql.DB,
  opts ...Option) (*Repo, error)` — the handle is user-owned (ADR-033
  §2.7; the module imports `database/sql` only, no driver). Options:
  `WithSchema(name)` (the namespaced schema, default `gobpm`),
  `WithLogger`. `Repo` implements the full grown contract: CAS `Save`
  (`INSERT … ON CONFLICT`/`UPDATE … WHERE rec_version = $expected`;
  zero rows affected → `errs.ConcurrentUpdate`), `Load` (copy
  semantics are free — rows are values), `Delete` (idempotent),
  group-scoped `ListInFlight` (`status = active AND engine_group = $1
  AND (lease_owner = '' OR lease_expiry <= $2)`, ordered by id for
  determinism). `Repo` declares `ClusterCompatibility() (true,
  "shared PostgreSQL store; CAS+lease fencing per ADR-033 §2.8")` and
  implements `Migrator`.
- **FR-5 — schema & migrations.** Embedded, versioned SQL migrations
  (an `embed.FS` of `NNNN_*.sql` + a `schema_version` table in the
  adapter's schema; each file applies in a transaction, recorded by
  number; re-running is a no-op). Migration 0001 creates: `tenants`
  (`tenant_id`, `engine_group`, `is_default bool`, `created_at`;
  **partial unique index** `UNIQUE (engine_group) WHERE is_default` —
  the database enforces "one default tenant per group") and
  `instances` (`id` PK, `engine_group`, `tenant_id` + FK to
  `tenants`, `status`, `payload bytea`, `rec_version bigint`,
  `lease_owner`, `lease_incarnation bigint`, `lease_expiry
  timestamptz`, `updated_at`; index on `(engine_group, status,
  lease_expiry)` for the recovery listing; deliberately **no CHECK
  constraint on `status`** — the vocabulary is append-only and DDL
  must not reject a status a newer engine writes). Migration 0001 also
  creates `groups` (`group_name` PK, `created_at`) — the FR-1
  registry; `instances.engine_group` and `tenants.engine_group`
  reference it, so the FR-1 "records reference established groups
  only" rule is a database guarantee. The listing query follows the
  FR-1 exclusion rule (`status NOT IN (terminal…, suspended)`), never
  `status = active`. A record arriving with
  `Tenant == ""` resolves to its group's flag-designated default
  tenant row, created idempotently on first use (group membership is
  unknown at migration time).
- **FR-6 — the conformance suite** (new package
  `pkg/repository/repositorytest` — the `<pkg>test` sibling ADR-003
  §4.2 prescribes for it; ADR-002 §8.4's `RepositoryConformance`
  sketch, named `Conformance` here since the package already says
  "repository"): `Conformance(t *testing.T,
  factory func(t *testing.T) repository.Repository)` runs the full
  contract as subtests — create/update CAS accept, stale-version
  reject (`ConcurrentUpdate` classified), load-after-save fidelity
  (payload, status, lease, group, tenant), payload isolation (mutating
  a loaded/saved slice never changes the store), delete idempotency,
  `ListInFlight` filtering (terminal excluded, suspended excluded,
  live-lease excluded, expired-lease included, **group-scoped** — two
  groups over one store never cross-list), an empty group rejected
  loud (on `Save`, `ListInFlight`, `RegisterGroup` and `GroupExists`),
  the group registry (`RegisterGroup` idempotent; `GroupExists`
  before/after; a `Save` into an unregistered group rejected; an
  unregistered group lists empty), deterministic ordering.
  `memrepo` and postgres both pass it — one truth, two backends.
- **FR-7 — zombie-engine fencing (the ADR-033 §2.8 proof).** Against
  real postgres: engine A owns an instance; its lease lapses; engine B
  claims (incarnation+1); A's next save — carrying the stale
  RecVersion — is rejected with `ConcurrentUpdate`. The lapsed owner
  can never overwrite the new owner's state.
- **FR-8 — kill-and-resume e2e.** Against real postgres: thresher A
  (group `g`) runs a process to a timer park, checkpoints, is stopped
  (the "kill"); thresher B, same database, same group, boots →
  recovery lists/claims/restores → the timer fires at the recorded
  deadline → the instance completes; the `Recovered` fact observed. A
  second run variant: an engine in group `h` over the same database
  recovers **nothing**.
- **FR-9 — memrepo evict-Active fix** (audit row 11 — "no
  pre-existing errors"): a `Save` that transitions a tracked terminal
  id back to non-terminal removes it from `termSet`/`termOrder`; an
  Active record can never be evicted. The audit row flips to fixed in
  this branch.
- **FR-10 — test infrastructure.** Postgres-backed tests read
  `GOBPM_PG_TEST_DSN`; unset → `t.Skip` with a pointer to `make
  pg-up`. `make pg-up`/`pg-down` run a disposable `postgres:17-alpine`
  docker container with a fixed port/password matching the DSN the
  README documents (a plain docker container by decision — no
  testcontainers dependency). CI: the `check` job gains a
  `services: postgres` container and exports the DSN, so the
  conformance/fencing/e2e tests and the diff-coverage gate run the
  postgres paths on every PR.

### §2.2 Non-functional

- **NFR-1** — the adapter's runtime dependency set is `database/sql`
  + stdlib only; the postgres driver (`jackc/pgx/v5/stdlib`) and
  testify are test-only dependencies of the module.
- **NFR-2** — validate-all-params; no `Must*` in library paths; loud
  classified errors (`POSTGRES_REPO` error class) per the errs idiom.
- **NFR-3** — every SQL touch honors `ctx`; no connection pooling
  logic of our own (the pool is `*sql.DB`'s).
- **NFR-4** — `make ci` green (the new module rides `CORE_MODULES`
  automatically); diff-coverage ≥95% (aim 100%); touched functions
  ≥80%.

## §3 Models

```go
// pkg/repository — the grown record (FR-1)
type InstanceRecord struct {
	ID         string
	Payload    []byte
	Lease      Lease
	RecVersion int64
	Status     Status
	Group      string // creator engine's group; never empty (solo engine → its id)
	Tenant     string // owning tenant; "" = the default tenant
}

type Repository interface {
	Save(ctx context.Context, rec InstanceRecord) error
	Load(ctx context.Context, id string) (InstanceRecord, bool, error)
	Delete(ctx context.Context, id string) error
	ListInFlight(ctx context.Context, group string, now time.Time) ([]string, error)
	RegisterGroup(ctx context.Context, group string) error // idempotent establish
	GroupExists(ctx context.Context, group string) (bool, error)
}

// pkg/renv — the capability vocabulary (FR-3)
type ClusterAware interface {
	ClusterCompatibility() (compatible bool, reason string)
}
type Migrator interface {
	Migrate(ctx context.Context) error
}

// adapters/postgres (FR-4)
func New(db *sql.DB, opts ...Option) (*Repo, error)
func WithSchema(name string) Option
func WithLogger(l observability.Logger) Option
```

```sql
-- migration 0001 (schema-qualified; default schema "gobpm")
CREATE TABLE groups (
    group_name text PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE tenants (
    tenant_id    text PRIMARY KEY,
    engine_group text NOT NULL REFERENCES groups (group_name),
    is_default   boolean NOT NULL DEFAULT false,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX tenants_one_default_per_group
    ON tenants (engine_group) WHERE is_default;

CREATE TABLE instances (
    id                text PRIMARY KEY,
    engine_group      text NOT NULL REFERENCES groups (group_name),
    tenant_id         text NOT NULL REFERENCES tenants (tenant_id),
    status            smallint NOT NULL,
    payload           bytea NOT NULL,
    rec_version       bigint NOT NULL,
    lease_owner       text NOT NULL DEFAULT '',
    lease_incarnation bigint NOT NULL DEFAULT 0,
    lease_expiry      timestamptz,
    updated_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX instances_recovery
    ON instances (engine_group, status, lease_expiry);

CREATE TABLE schema_version (
    version    integer PRIMARY KEY,
    applied_at timestamptz NOT NULL DEFAULT now()
);
```

Worked trace (T-8's shape): thresher A (`New("engine-a",
WithEngineGroup("billing"), WithRepository(pgRepo))`) starts; `Run`
detects `Migrator` → schema/tables exist; a process parks on a timer →
the checkpoint `INSERT`s `instances` row (`engine_group='billing'`,
`tenant_id=<billing's default>`, `rec_version=1`); A stops; thresher B
(same group, same db) boots → `Migrate` no-ops → `ListInFlight(ctx,
"billing", now)` returns the id → CAS claim (`rec_version 1→2`,
`lease_incarnation 0→1`) → restore → the timer fires at the recorded
deadline → terminal checkpoint (`status=Completed`). An engine in
any other group over the same database lists nothing throughout — and
an ungrouped thresher C (`New("engine-c", …)`, no `WithEngineGroup`)
runs in group `engine-c`, equally blind to `billing`'s instances.

## §4 Analysis & decisions

- **§4.1 The group is a port parameter, not adapter config.** Decided
  at ADR level (v.3 §2.8): the group is engine identity, so the record
  carries it and the listing filters by it. The rejected alternative —
  a group-scoped adapter handle (`postgres.WithEngineGroup`) — kept the
  port untouched but hid a partition the contract depends on inside
  one adapter's wiring; the second implementation exists precisely to
  surface such contract gaps before 1.0.
- **§4.2 No `Claim` method.** A claim stays a CAS `Save` under
  incarnation+1 (the shipped SRD-070/071 discipline —
  `recovery.go:53`, `wake.go:249`). A dedicated method would be a
  second way to do the same thing and another surface every adapter
  must fence identically.
- **§4.3 Tenant resolution is storage-side.** The engine stamps `""`;
  the adapter maps `""` → the group's flag-designated default-tenant
  row (idempotent ensure on first use — group membership is unknown at
  migration time). The flag, not a reserved id, designates the default
  (an operator-chosen id can never collide with it); the partial
  unique index makes "one default per group" a database guarantee, not
  a code promise. memrepo carries `Tenant` as an opaque field — a
  registry table in a process-lifetime store would be ceremony.
- **§4.4 Simple embedded migrations.** A `schema_version` table + an
  ordered `embed.FS` of SQL files, each applied in a transaction. The
  rejected alternative (golang-migrate/goose) buys down/redo/CLI
  tooling this module doesn't need at the price of a heavyweight
  dependency in a library adapter; two tables don't justify it.
  `Migrate` is idempotent and safe to run on every boot.
- **§4.5 DSN-gated postgres tests, docker not testcontainers** (user
  decision). `t.Skip` without `GOBPM_PG_TEST_DSN` keeps a plain
  checkout's `go test ./...` green with no docker anywhere;
  `make pg-up` + the CI `services:` container provide the real
  database where it matters. The diff-coverage gate runs in CI with
  the DSN set, so the adapter's changed lines are measured, not
  skipped — locally, `make ci` documents the `pg-up` prerequisite.
- **§4.6 The capabilities live in `pkg/renv`.** ADR-003 §3.1
  explicitly rejected a unified `pkg/extension` ("becomes a
  god-package … doesn't match Go-stdlib convention"), and `pkg/renv`
  is the engine↔extension contract package that already names the
  ADR-002 §8.3 hooks as its deferred residents
  (`engineruntime.go:24`). Two interfaces land now
  (`Starter`/`Stopper`/`HealthChecker` arrive with consumers);
  adapters implement them structurally, without importing `renv`. `thresher.Run` calling `Migrate` before recovery is
  the ADR-033 §2.7 "bootstrap walks the wired extensions" rule at its
  minimal useful size (one wired extension today).
- **§4.7 Solo by default — no implicit cluster.** Three semantics for
  an unset group were weighed: (A) all ungrouped engines share one
  implicit "default" cluster — rejected: two unrelated applications
  wired to one database would silently steal each other's instances,
  with deployment parity by luck; (B) a mandatory group whenever a
  repository is wired — rejected: pure ceremony for the primary
  single-engine embedded audience; (C) **chosen** — an ungrouped
  engine's group is its own engine id: restart recovery works with
  zero configuration (the id is stable across restarts), while
  cluster membership requires deliberately sharing a group name.
  Documented corner: renaming an ungrouped engine strands its old
  instances under the old group — production guides recommend an
  explicit group. The **group registry** closes the remaining silent
  failure: a misspelled group name would mint a fresh partition, so
  membership in an existing group is assertable —
  `WithExistingEngineGroup` refuses at `Run` when the group is absent.
  Two options rather than a `mustExist bool` parameter keep call sites
  self-documenting; "exists" is a registry fact, not an inference from
  records (an established-but-idle cluster must not read as absent).
- **§4.8 The e2e "kill" is a stop, not a SIGKILL.** The property under
  test is that **nothing in-memory is needed for recovery** — a
  clean-stopped engine and a killed one leave the same durable state
  by construction (checkpoints are transition-synchronous). A
  process-level SIGKILL harness would test the OS, not the contract;
  the zombie test (FR-7) covers the crash-specific hazard (a survivor
  writing stale state).

## §5 API deltas

| Surface | Change | Compat |
|---|---|---|
| `repository.InstanceRecord` | + `Group`, `Tenant` | additive fields; zero values = defaults |
| `repository.Repository.ListInFlight` | + `group` param | **breaking** for implementers/callers (one of each in-repo; the consumer-smoke gate proves embedder impact) |
| `repository.Repository` | + `RegisterGroup`, `GroupExists` | **breaking** for implementers (the group registry, FR-1) |
| `thresher` | + `WithEngineGroup(string)`, `WithExistingEngineGroup(string)` | additive |
| `pkg/renv` | + `ClusterAware`, `Migrator` capability interfaces | additive |
| `pkg/repository/repositorytest` | new package: `Conformance(t, factory)` | new |
| `adapters/postgres` | new module: `New`, `WithSchema`, `WithLogger` | new |
| `memrepo` | group filter, tenant field, evict-Active fix, `ClusterAware` | behavioral fix (FR-9) |

## §6 Test scenarios

| # | Test | Verifies |
|---|---|---|
| T-1 | conformance vs memrepo (`pkg/repository/repositorytest` driven from `memrepo`) | FR-1/FR-6: the full contract incl. group scoping and tenant round-trip on the in-memory reference |
| T-2 | conformance vs postgres (`adapters/postgres`, DSN-gated) | FR-4/FR-6: the identical suite against the real schema |
| T-3 | zombie fencing (`adapters/postgres`, DSN-gated) | FR-7: lapsed owner's save → `ConcurrentUpdate`; new owner's state intact |
| T-4 | migrations (`adapters/postgres`, DSN-gated) | FR-5: fresh-db `Migrate` creates all objects; re-run no-ops; `schema_version` recorded; a second default tenant for one group is **rejected by the index**; default-tenant ensure is idempotent and group-local |
| T-5 | memrepo evict-Active regression (`pkg/repository/memrepo`) | FR-9: terminal→Active re-save untracked; cap pressure never evicts Active |
| T-6 | engine group options (`pkg/thresher`) | FR-2: `WithEngineGroup`/`WithExistingEngineGroup` validation/trim (empty or both-set rejected); unset → group = engine id (the solo default, §4.7), registered at `Run`; `WithExistingEngineGroup` fails `Run` loud when the group is absent, joins when present; records stamped; recovery lists own group only — a restarted same-id engine recovers, a differently-named neighbor sees nothing; a cross-group record refused loud on claim |
| T-7 | migrator hook (`pkg/thresher`) | FR-3: `Run` calls a wired `Migrator` before recovery; its error aborts `Run`; a non-Migrator repository unaffected; memrepo's `ClusterCompatibility` declaration |
| T-8 | kill-and-resume e2e (`adapters/postgres`, DSN-gated) | FR-8: the §3 worked trace — park/stop/recover/fire/complete over real postgres; the other-group engine recovers nothing |

## §7 Milestones

- **M1 — the port growth + the engine group.** FR-1/FR-2/FR-9 (+ the
  memrepo group/tenant support); conformance suite package (FR-6);
  T-1/T-5/T-6. Includes the audit-row flip.
  `feat(repository): group- and tenant-carrying records, the conformance suite (SRD-078 M1)`.
- **M2 — the capability vocabulary.** FR-3; T-7.
  `feat(renv): ClusterAware and Migrator capabilities (SRD-078 M2)`.
- **M3 — the adapter.** FR-4/FR-5/FR-10 (module, schema, migrations,
  CAS/lease SQL, docker targets, CI service); T-2/T-3/T-4.
  `feat(adapters/postgres): the durable Repository (SRD-078 M3)`.
- **M4 — the proof + docs.** FR-7/FR-8; T-8 (and T-3 if not landed in
  M3); guides (`operating/persistence.md`, `extending/repository.md`),
  README feature note, CHANGELOG.
  `feat(adapters/postgres): zombie fencing and kill-and-resume e2e (SRD-078 M4)`.

## §8 Cross-doc

- Implements **ADR-033 v.3** §2.7/§2.8 (the first durable adapter; the
  group/tenant partitions on the record; `Migrator`; the fencing
  proof).
- Upstream: **ADR-002 v.2** §8.3/§8.4 (capabilities, conformance),
  **ADR-003 v.1** §4.4 (adapter-module layout).
- Related: **SRD-070** (the checkpoint record and CAS discipline this
  stores), **SRD-071 v.2.5** (the wake claim path FR-2 group-scopes).
- **#276**: closes all deliverables except `adapters/sqlite`
  (explicitly a separate flow) and the durable DataStore adapter
  (ADR-030 v.1's deferred workstream — filed as a follow-up issue at
  the PR handover).

## §9 Definition of Done

- [ ] FR-1…FR-10 implemented; every §6 test exists and passes (the
      DSN-gated ones against `make pg-up`'s container and in CI).
- [ ] `make ci` green incl. the new module; diff-coverage ≥95% (aim
      100%); touched functions ≥80%.
- [ ] Audit row 11 flipped with the FR-9 fix referenced.
- [ ] Guides/README/CHANGELOG synced; the follow-up DataStore issue
      filed.
- [ ] §10 filled.

## §10 Implementation summary

*Post-landing placeholder.*

## Open questions

*None — §4 records the resolved design points (group locus, claim
shape, tenant resolution, migration tooling, test infrastructure,
e2e kill semantics).*
