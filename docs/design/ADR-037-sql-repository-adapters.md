# ADR-037 — SQL Repository adapters

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-08-11 |
| Owner | Ruslan Gabitov |
| Supersedes | — |
| Refines | [ADR-033 v.5 §2.7, §2.8](ADR-033-persistence-and-state.md) · [ADR-002 v.2 §4.2, §8.3](ADR-002-extension-architecture.md) |

---

## 1. Context

There are now **two** SQL implementations of `repository.Repository` —
`adapters/postgres` and `adapters/sqlite` — and everything they share was
decided twice, independently, by reading one another's code. That is the
condition an ADR exists for: a contract discovered by repetition rather than
declared up front.

The trigger is specific. Building the SQLite adapter surfaced a rule with no
home: **an adapter that depends on correctness-critical connection settings
must own the connection**. It was written into the SRD that landed the
adapter, which is the wrong place — an SRD is a one-shot landing record, not
where the author of the third adapter looks. With one adapter it was a
generalisation from a single case; with two it is a contract.

ADR-033 owns the persistence MODEL (what a checkpoint is, what a lease means,
what claim-first recovery requires). ADR-002 owns the extension architecture
(ports, options, optional capabilities). Neither says anything about how a SQL
adapter is built, and this fills exactly that gap — nothing here overrides
either.

## 2. Decision

### 2.1 The shape

A SQL Repository adapter is its own Go module under `adapters/<name>/`, and
lays out as:

| File | Holds |
|---|---|
| `<name>.go` | constructors, options, `ClusterCompatibility`, the error wrapper |
| `repo.go` | the six `repository.Repository` methods |
| `queries.go` | the SQL, assembled once |
| `migrate.go` + `migrations/NNNN_*.sql` | `renv.Migrator` over embedded, versioned migrations |
| `conformance_test.go` | `repositorytest.Conformance`, one line |

The split is not filing tidiness: `queries.go` isolates the dialect, which is
what makes a second adapter a translation rather than a rewrite.

### 2.2 Connection ownership — set what you own, verify what you are handed

An adapter offers a constructor that OWNS its pool whenever any connection
setting decides correctness, and MAY offer one that borrows a pool from the
host.

- **Owning** (`Open(dsn)` / `Open(path)`): the adapter builds the connection
  string, including every setting its guarantees rest on, and closes the pool
  it opened.
- **Borrowing** (`New(*sql.DB)`): the adapter must NOT mutate a pool it does
  not own. It VERIFIES the settings it requires, REFUSES what breaks
  correctness, and WARNS about what merely degrades. It never closes the pool.

The asymmetry is the point. Postgres needs no owning constructor, because no
postgres DSN parameter decides whether the schema's constraints are enforced.
SQLite does: `foreign_keys` is OFF by default and applies per connection,
while `database/sql` opens connections lazily — so a pool built the obvious
way enforces none of the schema's references, and a `PRAGMA` executed once
after `sql.Open` lands on whichever connection served it.

**Verification must probe more than one connection**, for the same reason: a
single probe passes on a configured connection while the rest of the pool
enforces nothing. And it must be **bounded** — a borrowed pool's connections
may be checked out by the host, and a constructor that waits forever for one
is worse than a constructor that verifies less and says so.

### 2.3 Refuse what is wrong; warn about what is slow

A required setting is classified by what its absence costs:

| Class | Example | Response |
|---|---|---|
| Correctness | `foreign_keys` — without it the schema's constraints do not exist | **Refuse** to construct |
| Behaviour under contention | WAL, `busy_timeout` — without them a contended write fails instead of waiting | **Warn**, construct anyway |

Refusing on the second class would mean the adapter overruling a deployment
choice that is legitimately the host's. Accepting the first silently would
mean a store that takes rows it is required to reject.

### 2.4 Portable value encodings

Where a dialect lacks a type, the encoding is chosen for the OPERATION the
engine performs on it, not for readability:

- **Instants that are COMPARED** — a lease expiry, which `ListInFlight`
  filters with `<=` — must encode so that the comparison the database performs
  is chronological. In SQLite that means fixed-width RFC 3339 in UTC; a
  variable-width fraction sorts wrongly against itself, and a local offset
  sorts as text while meaning nothing.
- **Payloads** must round-trip byte-identically and must not alias the
  caller's slice.
- **Status is stored as its integer, with no CHECK constraint.** The
  vocabulary is append-only, and DDL must not reject a status a newer engine
  writes.

### 2.5 Cluster compatibility is declared, not documented

Every SQL adapter implements `renv.ClusterAware` and answers with a reason the
operator can act on:

```go
// postgres
return true, "shared PostgreSQL store; CAS+lease fencing per ADR-033 §2.8"

// sqlite
return false, "embedded single-writer SQLite database; ADR-033 §2.8 " +
    "cluster semantics need a shared store — use adapters/postgres"
```

A negative verdict names the alternative. The engine learns the constraint by
asking, not by someone having read the package comment.

### 2.6 Migration serialization is per-dialect and must be stated

Concurrently booting engines must not collide on DDL, and how that is achieved
differs:

- **Postgres** takes an advisory lock for the duration of the migration
  transaction.
- **SQLite** relies on the single-writer lock — but only because the
  connection opens `_txlock=immediate`. The DEFERRED default takes no write
  lock at `BEGIN`, so two migrators read the version under shared locks and
  then deadlock trying to upgrade, rather than serializing.

The rule the two share: **the mechanism is named in the migrator's doc
comment.** "The database serializes us" is a claim about a specific lock mode,
and it was wrong in the SQLite adapter until an external review caught it.

## 3. Alternatives considered

| Option | Verdict |
|---|---|
| **A shared `adapters/sqlcommon` package** | Rejected. Adapters may not import each other (ADR-003 §4.4), so the shared code would have to live in core — putting SQL scaffolding in a module that holds to stdlib + `uuid`, for the benefit of code outside it. |
| **Generate adapters from one template** | Rejected. The differences that matter are exactly the ones a template hides: dialect, locking, and which settings are correctness-critical. |
| **Leave it to each SRD** | Rejected — this is what happened, and it is why the rule was written into a one-shot landing record that the next adapter's author would not read. |
| **Document the convention, contract nothing** — chosen | An ADR is where a repeated decision belongs. It binds new SQL adapters and leaves the two existing ones conformant as written. |

## 4. Consequences

**Good.** A third SQL adapter has a shape to follow and a checklist for the
non-obvious parts. `repositorytest.Conformance` stays the arbiter of
behaviour; this covers what conformance cannot — construction, migration and
the operational declarations.

**Costs.** Two documents now govern a SQL adapter — ADR-033 for the model,
this for the construction — and a reader must know which answers what. §1
states the boundary for that reason.

**Not settled here.** The published conformance suite has no concurrency
subtest, so it cannot check §2.6's serialization or contended `Save` at all;
each adapter tests that itself today. Widening the suite is a change every
adapter author inherits and needs its own document.

## 5. Verification

- Both existing adapters conform to §2.1–§2.6 as written, which is what makes
  this a record of a decision rather than a proposal.
- `adapters/sqlite` proves §2.2's owning path and §2.3's split with
  `TestNewRefusesAPoolWithForeignKeysOff`,
  `TestForeignKeysEnforcedOnEveryConnection` and
  `TestNewOnABusyHostPoolDoesNotHang`.
- §2.4's comparison rule is pinned by
  `TestLeaseExpiryOrdersLexicographically`.
- §2.5 is pinned by `TestClusterCompatibilityReportsUnsafe`.

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-08-11 | Ruslan Gabitov | Initial Draft, written once a SECOND SQL Repository adapter made the shared decisions a contract rather than one adapter's habit. Records the module shape, connection ownership ("set what you own, verify what you are handed"), the refuse/warn split, portable value encodings, the cluster declaration and per-dialect migration serialization. |
