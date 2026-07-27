---
title: Custom repository
description: Back process registration with your own store.
---

# Custom repository

The **repository** is the engine's persistence slot for process-instance
state — the record of each running (and recently finished) instance. gobpm ships
with an in-memory default; implement `repository.Repository` and hand it to the
engine when you want instance state to live somewhere durable (a database, a
key-value store) so in-flight instances survive a restart.

> This is the *minimal* skeleton contract — enough to run today's in-memory
> BPMN. Durable serialization, versioning/CAS, transactions, history and inbox
> are deliberately out of scope here; they belong to the dedicated Persistence &
> State ADR (see the package doc). Treat this page as the seam, not a
> production storage design.

## The Repository contract

`repository.Repository` is four methods — a keyed CRUD over `InstanceRecord`
plus a rehydration listing:

```go
type Repository interface {
    // Save stores (or replaces) the record under its ID.
    Save(ctx context.Context, rec InstanceRecord) error
    // Load returns the record for id; the bool is false when none exists.
    Load(ctx context.Context, id string) (InstanceRecord, bool, error)
    // Delete removes the record for id (a no-op if it is absent).
    Delete(ctx context.Context, id string) error
    // ListInFlight returns the IDs of all non-terminal (Active) instances, for
    // rehydration after a restart.
    ListInFlight(ctx context.Context) ([]string, error)
}
```

| Method | Implement it to |
|---|---|
| `Save` | store or replace one record by its `ID` (upsert). |
| `Load` | fetch by id; return `false` (not an error) when absent. |
| `Delete` | remove by id; a no-op when the id is unknown — do not error. |
| `ListInFlight` | list the ids of every `StatusActive` record, so a restart can find what to resume. |

## The record

The unit you persist is `repository.InstanceRecord`:

```go
type InstanceRecord struct {
    State  any
    ID     string
    Status Status
}
```

| Field | Meaning |
|---|---|
| `ID` | the instance id — your primary key. |
| `State` | the engine's instance snapshot, **opaque** to the repository. The in-memory default stores it by reference; a durable adapter owns its own serialization (that model is the Persistence & State ADR's, not this contract's). |
| `Status` | lifecycle status — `StatusActive`, `StatusCompleted`, or `StatusTerminated`. |

`Status` mirrors the runtime instance lifecycle. `StatusActive` is in-flight;
the two terminal states satisfy `Status.IsTerminal()`:

| Constant | State |
|---|---|
| `StatusActive` | in-flight (what `ListInFlight` returns). |
| `StatusCompleted` | finished normally. |
| `StatusTerminated` | canceled / terminated. |

## Registering it

Pass your repository to the engine with `thresher.WithRepository`:

```go
func WithRepository(r repository.Repository) Option
```

```go
eng, err := thresher.New("engine",
    thresher.WithRepository(myStore),
)
```

The default, when you pass no option, is the in-memory store —
`thresher.WithRepository(r repository.Repository) Option` documents it as
"in-memory, non-durable".

## A minimal implementation

A map-backed store guarded by a mutex satisfies the whole contract. Note the
two contract subtleties baked in: `Load` returns `false` (not an error) on a
miss, and `Delete` is a silent no-op on an unknown id.

```go
type MemStore struct {
    mu   sync.RWMutex
    recs map[string]repository.InstanceRecord
}

func NewMemStore() *MemStore {
    return &MemStore{recs: map[string]repository.InstanceRecord{}}
}

func (s *MemStore) Save(_ context.Context, rec repository.InstanceRecord) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    s.recs[rec.ID] = rec
    return nil
}

func (s *MemStore) Load(_ context.Context, id string) (repository.InstanceRecord, bool, error) {
    s.mu.RLock()
    defer s.mu.RUnlock()
    rec, ok := s.recs[id]
    return rec, ok, nil
}

func (s *MemStore) Delete(_ context.Context, id string) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    delete(s.recs, id) // no-op when absent
    return nil
}

func (s *MemStore) ListInFlight(_ context.Context) ([]string, error) {
    s.mu.RLock()
    defer s.mu.RUnlock()
    var ids []string
    for id, rec := range s.recs {
        if rec.Status == repository.StatusActive {
            ids = append(ids, id)
        }
    }
    return ids, nil
}
```

## The reference implementation

The built-in default lives in the `repository/memrepo` sibling package. It is
the store the engine uses when you pass no `WithRepository`, and the model to
copy for a real adapter. Beyond the bare contract it adds a
**bounded-in-memory** discipline: active instances are retained unconditionally
(their count is real load), while terminal records are capped — the oldest are
evicted past `memrepo.DefaultMaxTerminal` (1024) so lookup history cannot grow
unbounded.

```go
func New(opts ...Option) *Repo
```

Its options mirror what a durable adapter typically wants to tune:

| Option | Effect |
|---|---|
| `memrepo.WithLogger(l observability.Logger)` | route the eviction warning through your logger (default `slog.Default()`). |
| `memrepo.WithMaxTerminal(n int)` | change the terminal-record cap. |

## How the engine uses it

The engine holds one repository for its lifetime and exposes it through the
runtime. Conceptually the store is the **write-behind of the instance
lifecycle**: as an instance is created and advances it is `Save`d under its id;
on a terminal outcome its `Status` flips to `StatusCompleted` /
`StatusTerminated`; `Load` fetches a record by id; and after a restart
`ListInFlight` enumerates the `StatusActive` ids so the engine knows what to
resume.

> Reach for a custom repository when instance state must **outlive the
> process** — durable persistence and restart-time rehydration. If everything
> you run is in-process and ephemeral, the default `memrepo` is enough; you do
> not need this seam. The wiring that turns `Save`/`Load`/`ListInFlight` into
> full durable recovery is owned by the Persistence & State ADR and is future
> work on top of this skeleton — see [ADR-001 — execution model](../../design/ADR-001-execution-model.md).

## See also

- Reference implementation: `repository/memrepo`
- Related guides: [Custom Data Store](data-store.md) · [The engine (Thresher)](../concepts/engine.md)
- Design: [ADR-001 — execution model](../../design/ADR-001-execution-model.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/repository`
