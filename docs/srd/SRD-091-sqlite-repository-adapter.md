# SRD-091 — The SQLite Repository adapter

| Field | Value |
|---|---|
| Status | Draft |
| Date | 2026-08-11 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-033 v.5](../design/ADR-033-persistence-and-state.md) §2.7, §2.8 · [ADR-003 v.1](../design/ADR-003-module-layout.md) §4.2, §4.4 · [ADR-002 v.2](../design/ADR-002-extension-architecture.md) §4.2, §8.3 |
| Milestone | E2 — durable persistence |
| Issue | [#316](https://github.com/dr-dobermann/gobpm/issues/316) |

---

## 1 Context

`adapters/sqlite` has been a scaffold since SRD-001: one `doc.go`, no
implementation, no `go.sum`. Both ADR-002 §4.2 and ADR-003 §4.2 name SQLite as
a Repository adapter, so the catalogue advertises something a user cannot
reach for — the same defect SRD-088 §4.4 found in the `script` port, in the
adapter tree rather than the port tree.

### 1.1 The reason to build it is not that the scaffold is empty

**No Repository adapter's conformance suite has ever executed in CI.**
`adapters/postgres` is fully implemented and its `conformance_test.go` is one
line, but every postgres test is gated on `GOBPM_PG_TEST_DSN`
(`adapters/postgres/pg_test.go:26`) and **skips** when it is unset. CI sets
nothing, so the gate has never run it.

The consequence is not that postgres is untested — a developer runs it against
a container — but that `repositorytest.Conformance` itself has only ever been
executed against `memrepo`, the implementation it was written alongside. A
conformance suite proved against exactly one implementation encodes that
implementation's shape; SRD-088 §4.10 records the same trap being sprung in
`expressiontest`, where asserting nil-source rejection as universal turned out
to encode `lite`'s behaviour and would have failed a correct `goexpr`.

SQLite is an **embedded** database: a file or `:memory:`, no server, no
container, no DSN. Its conformance run costs a test binary and nothing else,
so it runs on every push. That makes this adapter the first real test of the
Repository *contract* as opposed to its in-memory reference.

### 1.2 What the contract actually requires

`repository.Repository` is six methods (`pkg/repository/repository.go`), and
the parts a SQL implementation must get right are stated in their doc
comments rather than left to the schema:

- **`Save` is compare-and-swap.** It stores iff `rec.RecVersion` matches the
  stored version, `0` creates, the stored version increments on success, and a
  mismatch fails with `errs.ConcurrentUpdate`.
- **`ListInFlight` is the recovery listing**, group-scoped: non-terminal, not
  suspended, no live lease at `now`. An empty group MUST fail loud; an
  unregistered one lists empty.
- **Group membership is a guarantee, not a convention.** `RegisterGroup` is
  idempotent, `GroupExists` answers the "join an existing group only" check,
  and a record referencing an unregistered group must be rejected — "a
  misspelled group must refuse, not silently mint a fresh partition".

`repositorytest.Conformance` turns those into **18 subtests**, and it is
**sequential** — no goroutines, no `t.Parallel`. That matters more for this
adapter than for postgres, and §4.2 says why.

## 2 Requirements

### Functional

- **FR-1 — `adapters/sqlite` implements `repository.Repository`** over
  `database/sql`, in the shape `adapters/postgres` established: `sqlite.go`
  (constructors, options, capability reports), `repo.go` (the six methods),
  `queries.go` (the SQL), `migrate.go` + `migrations/`. `Open(path)` is the
  primary entry point and owns its pool; `New(*sql.DB)` serves a host that
  manages its own (§3.1).
- **FR-2 — it passes `repositorytest.Conformance` unmodified**, from a
  one-line test, and that test **runs in CI** — no environment gate, no skip.
- **FR-3 — the schema carries the same guarantees as postgres'**: the engine
  groups of ADR-033 §2.8 and the tenant linkage of §2.7 expressed as foreign
  keys, and "one default tenant per group" as a partial unique index. SQLite
  enforces foreign keys only when asked, so `PRAGMA foreign_keys = ON` is
  applied per connection — see §3.3, this is the single largest correctness
  risk in the adapter.
- **FR-4 — it implements `renv.Migrator`**, applying the embedded migrations
  idempotently, as postgres does.
- **FR-5 — it reports itself NOT cluster-safe** through
  `ClusterCompatibility()`, naming the reason. A single-file database cannot
  honour the lease semantics ADR-033 v.5 §2.8 gives a cluster, and the engine
  must learn that from the adapter rather than from documentation.
- **FR-6 — `adapters/sqlite/doc.go` stops describing unbuilt work.** Its
  scaffold notice and the #316 pointer are replaced by what the package does.

### Non-functional

- **NFR-1 — the core module gains no dependency.** `modernc.org/sqlite` is
  required by `adapters/sqlite/go.mod` alone; the depguard rules of ADR-003
  §4.4 already forbid the reverse edge, and `make ci`'s `consumer-smoke` step
  proves the core still builds without it.
- **NFR-2 — no CGo.** `modernc.org/sqlite` is a pure-Go translation, so the
  adapter cross-compiles and needs no toolchain beyond Go. This is `doc.go`'s
  existing commitment, not a new decision.
- **NFR-3 — the gate stays green**, including the diff-coverage floor on
  changed lines.

## 3 Shapes

### 3.1 The constructors — the file is the normal case

```go
// Open opens (creating if absent) the SQLite database at path, with the
// pragmas this adapter's guarantees depend on, and returns a Repo over it.
// The Repo owns the pool and closes it.
func Open(path string, opts ...Option) (*Repo, error)

// OpenMemory is Open against a private in-memory database.
func OpenMemory(opts ...Option) (*Repo, error)

// New wraps an already-open *sql.DB, for a host that manages its own pool.
// It VERIFIES the required pragmas rather than setting them, and refuses a
// pool that cannot honour the schema's guarantees.
func New(db *sql.DB, opts ...Option) (*Repo, error)

func WithLogger(l observability.Logger) Option
```

**`Open` is the primary constructor, and taking a path is the point.** The
first draft of this section had only `New(*sql.DB)`, copied from
`postgres.New` — which is wrong here for a reason postgres does not have. A
postgres DSN carries connection settings; none of them decide whether the
schema's constraints are enforced. A SQLite DSN does: `foreign_keys` is
per-connection and OFF by default (§3.3), so a caller who opens the file the
obvious way gets a pool on which FR-3's group guarantee silently does not
hold.

Validating that and refusing, as the first draft did, turns a correctness
requirement into a usability trap — the likely first experience being a
constructor that rejects a perfectly ordinary `sql.Open("sqlite", "app.db")`.
Owning the DSN removes the trap instead of reporting it: `Open` sets
`_pragma=foreign_keys(1)`, WAL and a busy timeout because the adapter, not
the caller, is what knows they are load-bearing.

`New` stays for the host that already manages a pool — a server sharing one
`*sql.DB` across subsystems — and there it verifies rather than sets, because
mutating a pool it does not own is not its call. That asymmetry is the whole
design: **set what you own, verify what you are handed.**

`postgres.WithSchema` has no SQLite equivalent and is not invented. A postgres
schema namespaces tables inside one database so several tests share a server;
a SQLite database *is* the namespace, so isolation is a different path or
`OpenMemory`. A `WithSchema` mapped onto `ATTACH` would wear a familiar name
over different semantics.

### 3.2 The dialect deltas

Mechanical, but each has a failure mode if taken as cosmetic:

| postgres | sqlite | why it is not cosmetic |
|---|---|---|
| `bytea` | `BLOB` | a payload round-trip must stay byte-identical; the suite's `PayloadIsolation` subtest asserts the stored bytes are not aliased to the caller's slice |
| `timestamptz` | `TEXT` (RFC 3339 UTC) | SQLite has no date type. Lease expiry is COMPARED in `ListInFlight`, so the encoding must be lexicographically ordered — which RFC 3339 in UTC is, and a local-time or epoch-seconds encoding is not |
| `boolean` | `INTEGER` 0/1 | the partial index `WHERE is_default` needs a truthy column SQLite understands |
| `bigint` | `INTEGER` | SQLite's INTEGER is up to 8 bytes; `RecVersion` and `Incarnation` fit |
| `now()` | `CURRENT_TIMESTAMP` | only in DDL defaults; every value the engine compares is passed in by the caller, never taken from the database clock |

### 3.3 `PRAGMA foreign_keys` — the one that bites

SQLite parses `REFERENCES` and then **ignores it unless `PRAGMA foreign_keys =
ON` is set on that connection**. It is per-connection, not per-database, and
`database/sql` pools connections — so setting it once after `sql.Open` sets it
on one pooled connection and leaves the others enforcing nothing.

The failure this produces is the worst kind: the schema *looks* like postgres',
the conformance suite's `SaveUnregisteredGroupRejected` passes on whichever
connection happens to have the pragma, and a record referencing a
non-existent group is accepted on another. The guarantee degrades to a
coin flip that reads as a flake.

Which is why `Open` exists (§3.1). It builds the DSN with
`_pragma=foreign_keys(1)`, so every connection the pool ever opens carries it
— the property has to hold for connections created later, which a one-off
`db.Exec("PRAGMA …")` after `sql.Open` cannot deliver.

`New` cannot set it on a pool it does not own, so it **verifies**: it asks
`PRAGMA foreign_keys` on several connections and refuses a pool that answers
0. Refusing is the right answer there precisely because the caller chose the
DSN, and a repository that silently drops a constraint is worse than one that
will not start.

### 3.4 Concurrency, stated rather than assumed

SQLite permits one writer at a time. The conformance suite is sequential, so
it cannot discover this — which is precisely why the limit is written down
here instead of being left to the suite to catch.

The adapter opens in WAL mode with a busy timeout, which makes concurrent
readers non-blocking and a contended writer wait rather than fail immediately.
That is enough for an embedded single-engine deployment, which is the case
FR-5 already restricts this adapter to. It is **not** enough to make SQLite a
cluster store, and FR-5's `ClusterCompatibility()` is the machine-readable
form of that sentence.

## 4 Analysis

### 4.1 Driver: `modernc.org/sqlite` over `mattn/go-sqlite3`

| Option | Verdict |
|---|---|
| `modernc.org/sqlite` (pure Go) — **chosen** | No CGo: the adapter cross-compiles, `CGO_ENABLED=0` builds work, and CI needs no C toolchain. It is a large dependency, but it lives in the adapter's own module, so nobody who does not wire SQLite compiles it (ADR-003 §3.3's pay-for-what-you-use property). |
| `mattn/go-sqlite3` (CGo) | Faster and closer to upstream, at the cost of CGo in every build that touches the adapter, and a C toolchain in CI. `doc.go` already reserved this as a build-tag alternative "if needed"; nothing has established the need. |

### 4.2 The suite is sequential — a finding, not a comfort

`repositorytest.Conformance` runs its 18 subtests one at a time. A passing run
therefore says nothing about concurrent `Save` under contention — the case a
durable Repository meets constantly in a real engine, where several instances
checkpoint at once.

For postgres that gap is covered by the database. For SQLite it is the
adapter's defining constraint, so this document does **not** treat a green
conformance run as evidence of concurrency-safety: §5's T-6 adds a
contended-writer test the suite does not contain.

Noting the gap is also a finding about the suite, and it belongs upstream:
§6 files it rather than fixing it here, because widening the published
contract is a change every adapter author inherits.

## 5 Tests

| # | Test | Asserts |
|---|---|---|
| T-1 | `TestConformanceOnAFile` | `repositorytest.Conformance` passes unmodified against a database `Open`ed at a temp path, **without an environment gate** (FR-2). The file is the normal deployment, so it is the one the contract is proved on |
| T-2 | `TestConformanceInMemory` | the same suite through `OpenMemory`, proving the adapter does not depend on a file — and that the two constructors agree |
| T-3 | `TestForeignKeysEnforcedOnEveryConnection` | with the pool forced to several connections, a record naming an unregistered group is refused on each — the §3.3 failure mode, which a single-connection test cannot see |
| T-4 | `TestNewRefusesAPoolWithForeignKeysOff` | `New` fails when handed a `*sql.DB` whose DSN omits the pragma, rather than silently running without the guarantee (§3.1's verify half) |
| T-4b | `TestOpenSetsTheRequiredPragmas` | a database `Open`ed by the adapter reports `foreign_keys=1` and WAL **on a connection the pool created later**, which is the property a post-open `PRAGMA` exec does not have (§3.1's set half) |
| T-5 | `TestMigrateIsIdempotent` | applying the migrations twice leaves the same schema and no error (FR-4) |
| T-6 | `TestConcurrentSaveSerializes` | N goroutines saving distinct records under WAL + busy timeout all succeed, and CAS still rejects a stale version under contention — the case §4.2 says the conformance suite cannot reach |
| T-7 | `TestClusterCompatibilityReportsUnsafe` | `ClusterCompatibility()` returns false with a reason naming the single-writer limit (FR-5) |
| T-8 | `TestLeaseExpiryOrdersLexicographically` | a lease written at a time whose RFC 3339 form sorts before another is listed accordingly — the §3.2 encoding claim, which a same-day test would not distinguish |

## 6 Follow-ups (filed, not deferred)

- **`repositorytest` has no concurrency subtest** (§4.2). Every Repository
  adapter is exposed to concurrent `Save` and the published suite is silent on
  it. Widening the contract is a change every adapter author inherits, so it
  gets its own document rather than riding here.

- **No ADR owns adapter connection ownership.** §3.1's rule — an adapter that
  depends on correctness-critical connection settings should OWN the
  connection (`Open`), and only VERIFY one it is handed (`New`) — is a
  convention with no home: ADR-002 owns the adapter-module conventions but
  says nothing about it, and a grep of `docs/design/` for DSN or pool
  ownership returns nothing. It is not promoted to ADR-002 here because it has
  one data point. Postgres does not need it, since no postgres DSN setting
  decides whether the schema's constraints hold, so this branch cannot tell a
  general rule from a SQLite peculiarity. Promote it when a second adapter
  meets the same decision — and note that an SRD is the wrong long-term home,
  since a one-shot document is not where a later adapter author looks.

## 7 Cross-document references

| Doc | Version | Used for |
|---|---|---|
| [ADR-033](../design/ADR-033-persistence-and-state.md) | v.5 | §2.7 the storage-composition rule and its tenant-linkage principle, §2.8 engine groups and cluster-safe locking |
| [ADR-003](../design/ADR-003-module-layout.md) | v.1 | §4.2 the adapter catalogue, §4.4 import direction |
| [ADR-002](../design/ADR-002-extension-architecture.md) | v.2 | §4.2 the Repository extension, §8.3 optional capabilities |
| [SRD-078](SRD-078-postgres-repository-adapter.md) | — | the postgres adapter this one mirrors |

## 8 Definition of Done

- FR-1…FR-6 landed and wired; T-1…T-8 green.
- `make ci` PASS end to end (verdict in `.ci/last-run.json`).
- Diff-coverage ≥95% on changed lines.
- The conformance suite runs **in CI**, unskipped — demonstrated by the run's
  own output, not asserted.
- `adapters/sqlite/doc.go` describes the package, and #316 is closed by the
  PR rather than left open against a landed adapter.

## 9 Open questions

None. The driver is settled by §4.1 and `doc.go`'s existing commitment; the
absent `WithSchema` is settled by §3.1; and the suite's concurrency gap is
filed as §6 rather than left hanging.
