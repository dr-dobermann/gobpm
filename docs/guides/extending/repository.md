---
title: Custom repository
description: Back instance checkpoints with your own store — CAS, leases, recovery.
---

# Custom repository

The **repository** is the engine's **instance-checkpoint port** (ADR-033):
the durable record of every running (and recently finished) instance.
gobpm ships an in-memory default; implement `repository.Repository` and
hand it to the engine when instance state must **survive a restart** —
configuring one *arms checkpointing*: every instance then writes a
consistent-cut checkpoint at its lifecycle transitions, and `Run`
recovers what the store says is unfinished (see
[Persistence & recovery](../operating/persistence.md)).

> One port among peers, deliberately narrow (ADR-033 §2.7): the
> repository holds instance checkpoints ONLY. Other storage-backed
> modules (a Data Store, an AuthZ plugin) define their own ports and
> share the user-owned backend handle — never this interface.

## The Repository contract

`repository.Repository` is a compare-and-set store over
`InstanceRecord`, the group-scoped recovery listing, and the
engine-group registry (ADR-033 v.5 §2.8):

```go
type Repository interface {
    // Save stores the record under its ID iff rec.RecVersion matches the
    // stored version (0 creates). The stored RecVersion increments on
    // success; a mismatch fails with errs.ConcurrentUpdate.
    Save(ctx context.Context, rec InstanceRecord) error
    // Load returns the record for id; the bool is false when none exists.
    Load(ctx context.Context, id string) (InstanceRecord, bool, error)
    // Delete removes the record for id (a no-op if it is absent).
    Delete(ctx context.Context, id string) error
    // ListInFlight returns the IDs of the CLAIMABLE in-flight instances
    // of the given engine group: non-terminal, not suspended, and with
    // no live lease at now.
    ListInFlight(
        ctx context.Context, group string, now time.Time,
    ) ([]string, error)
    // RegisterGroup establishes the engine group, idempotently.
    RegisterGroup(ctx context.Context, group string) error
    // GroupExists reports whether the group is established.
    GroupExists(ctx context.Context, group string) (bool, error)
}
```

| Method | Implement it to |
|---|---|
| `Save` | **compare-and-set** upsert: accept only when `rec.RecVersion` equals the stored version (0 creates); increment the stored version on success; reject a mismatch with an `errs.ConcurrentUpdate`-classified error — the split-brain fencing every adapter implements identically. Reject a record with an empty `Group` or an unregistered one. |
| `Load` | fetch a **value copy** by id; return `false` (not an error) when absent. |
| `Delete` | remove by id; a no-op when the id is unknown — do not error. |
| `ListInFlight` | list only the given group's **claimable** records, by EXCLUSION: not terminal, not `StatusSuspended`, and with an expired-or-absent lease at `now` (`rec.Lease.Expired(now)`). The exclusion form matters — the status vocabulary is append-only, and a status your adapter has never heard of must still list. An empty group fails loud; an unregistered one lists empty. |
| `RegisterGroup` | establish the group in your registry, idempotently; reject an empty name loud. |
| `GroupExists` | report membership — the assertion behind `WithExistingEngineGroup`'s "join existing groups only". |

## The record

```go
type Lease struct {
    Expiry      time.Time
    Owner       string
    Incarnation int64
}

type InstanceRecord struct {
    ID         string
    Payload    []byte // the schema-versioned checkpoint document, opaque
    Group      string // the creator engine's group — never empty
    Tenant     string // "" = the group's default tenant
    Lease      Lease
    RecVersion int64  // the CAS version
    Status     Status
}
```

| Field | Meaning |
|---|---|
| `ID` | the instance id — your primary key. |
| `Payload` | the engine's **schema-versioned checkpoint document**, opaque bytes. The serialization model is the engine's; the storage's job is bytes. Store and return **copies** — never alias the caller's slice. |
| `Lease` | the **ownership claim** (ADR-033 §2.8): the engine running the instance, its fencing incarnation, and the claim's expiry. A zero lease means "unowned"; `Lease.Expired(now)` reports whether it still holds. |
| `Group` | the creator engine's group (ADR-033 v.5 §2.8) — **never empty**: an ungrouped engine forms a single-engine group under its own id, and a store MUST reject a group-less record. |
| `Tenant` | the owning tenant (ADR-033 v.5 §2.7). `""` means the group's default tenant; resolution is the store's concern (the postgres adapter resolves it to the group's flag-designated default row). The engine stamps `""` until the Multi-tenancy ADR lands. |
| `RecVersion` | the compare-and-set version — see `Save`. |
| `Status` | `StatusActive`, `StatusSuspended` (in-flight, refuses triggers — reserved for the suspend/resume slice), or terminal `StatusCompleted` / `StatusTerminated` (`Status.IsTerminal()`). |

## Registering it

```go
eng, err := thresher.New("engine-A",
    thresher.WithRepository(myStore),          // arms checkpointing + recovery
    thresher.WithLeaseTTL(30*time.Second),     // the ownership window (default 30s)
)
```

**Configuring a repository changes engine behavior**: it is the signal
that this store is the state of record, so instances checkpoint into it
and `Run` recovers claimable records. The zero-config default (no
`WithRepository`) keeps the in-memory `memrepo` as a dormant slot —
volatile, zero overhead.

## Prove it: the conformance suite

Every adapter runs the same published contract suite the bundled
stores pass — one truth, N backends:

```go
func TestConformance(t *testing.T) {
    repositorytest.Conformance(t, func(t *testing.T) repository.Repository {
        return newYourStore(t) // a fresh, isolated store per subtest
    })
}
```

`pkg/repository/repositorytest` covers the CAS discipline, group
scoping, the registry, lease and tenant round-trips, payload isolation
and the listing filters. A green run is the definition of "implements
the contract".

## Optional capabilities

Two `pkg/renv` interfaces an adapter may additionally satisfy —
structurally, no import needed:

- **`renv.Migrator`** — `Migrate(ctx) error`: prepare your own objects
  in the shared, user-owned backend, idempotently. `thresher.Run`
  calls it on the wired repository **before** recovery; an error
  aborts the start loud.
- **`renv.ClusterAware`** — `ClusterCompatibility() (bool, string)`:
  declare whether the store may back a multi-engine deployment, with
  the reason. `memrepo` says `(false, "in-memory; state is not shared
  across nodes")`; the postgres adapter says `(true, …)`.

## Implementation notes for a durable adapter

- **CAS must be atomic** in your store's terms (a transaction, a
  conditional update, an `UPDATE … WHERE rec_version = ?`); the
  in-memory reference (`repository/memrepo`) shows the exact accept /
  increment / reject semantics to mirror, including the
  `errs.ConcurrentUpdate` class the engine's fencing matches on.
- **Copy payloads both ways** — `memrepo` value-copies on `Save` and
  `Load`; a durable adapter gets this for free by serializing.
- **Respect the lease filter in `ListInFlight`** — returning a record
  another engine holds makes two engines run one instance; the CAS
  fencing protects the state, but the losing engine wastes the work.
- `memrepo`'s extras worth mirroring: terminal records are capped
  (`memrepo.WithMaxTerminal`, default 1024, oldest evicted;
  `memrepo.WithLogger` routes the eviction warning) while active
  records are retained unconditionally.

## How the engine uses it

The full trace lives in [Persistence & recovery](../operating/persistence.md):
the instance loop `Save`s a consistent-cut document after each
observable transition (renewing its lease), a terminal outcome flips
the status, and a starting engine `ListInFlight`s + claims (CAS,
incarnation+1) + `Load`s + restores. A save from an engine whose lease
was reclaimed fails with `ConcurrentUpdate` — the zombie sees a
`CheckpointDeferred` warning and never corrupts the new owner's state.

The same port is what makes **dehydration** possible: an instance idle on held
waits releases its goroutines and is rebuilt from its checkpoint when a trigger
arrives. One consequence matters to an adapter author: a dehydrated instance has
no loop, so **its lease lapses by design** — the record stays in-flight and will
list as claimable. That is intended, not a leak; restart recovery reclaims it if
the engine that owned it died, and a live engine keeps waking it from memory.

## See also

- Reference implementations: `repository/memrepo` (in-memory) and
  [`adapters/postgres`](../../../adapters/postgres/) (durable, with
  embedded migrations)
- Operating guide: [Persistence & recovery](../operating/persistence.md)
- Related: [Custom Data Store](data-store.md) · [The engine (Thresher)](../concepts/engine.md)
- Design: [ADR-033 — Persistence & State](../../design/ADR-033-persistence-and-state.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/repository`
