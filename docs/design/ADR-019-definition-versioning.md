# ADR-019 — Definition versioning and the registration handle

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-06-28 |
| Owner | Ruslan Gabitov |
| Refines | [SAD-001 v.1 Vision & Architecture](SAD-001-vision-and-architecture.md) |

> **Draft** — to be landed by its implementing SRDs.
> Decides **definition versioning**: registering a process produces an immutable,
> **versioned** snapshot addressed by a **registration handle**, rather than a
> single mutable-by-accident registration keyed on the process id. Re-registering
> the same process id yields a *new version*; older versions keep running their
> already-started instances. The **latest** version always owns auto-start —
> registering a newer version supersedes the prior one's starters, and
> unregistering the latest promotes the now-newest version back to live. The process
> model stays **mutable** after registration (no freeze) — the engine is isolated
> from it by a snapshot taken at registration time. The concrete model-editing API
> (`Unlink`, `Remove`, `Clear`, …) is the sibling **rich-editing ADR**; the
> Thresher registry's concurrency discipline (audit §2.6) is owned by the
> implementing SRD.

## 1. Context & problem

[SAD-001 v.1](SAD-001-vision-and-architecture.md) states the engine's input
contract plainly: the **Snapshot** is an *"immutable, validated representation of
a Process definition. Engine accepts a Snapshot, not a mutable model."* Today the
engine honours the *immutable* half only by convention, and the *identity* of a
registration is ambiguous. Three gaps follow.

### 1.1 Registration identity is ambiguous and silently single

`Thresher.RegisterProcess` takes a `*process.Process`, builds a snapshot, and
stores it keyed by the process **id**. `Thresher.StartProcess` then takes that
**id** (a `string`) and starts whatever snapshot is stored under it. Two problems
compound:

1. **Re-registration is a silent no-op.** Registering a process whose id is
   already present keeps the first registration and discards the second ("first
   registration wins"). A user who edits a process and re-registers it expecting
   the engine to run the new shape gets the *old* one, with no error and no
   signal. The mismatch surfaces only as wrong runtime behaviour.

2. **The start key is the process's own id, not a registration receipt.** The
   caller addresses a registration by reaching back into the model object for its
   id. There is no first-class token that says *"this exact registered
   definition."* When more than one shape of a process could legitimately be
   registered over time, "start the process with id X" cannot say *which* X.

### 1.2 The snapshot is not actually isolated from the model — a silent leak

A process model is **mutable after registration**. Its public surface lets a
caller keep adding and rewiring elements once the snapshot has been taken, and
nothing rejects or even notices it. Worse, the snapshot does **not** take its own
copy of the definition's node graph at registration: it shares the model's node
objects by reference and only clones them later, once per running instance. The
consequence is a class of silent, shape-dependent corruption:

- **Structural edits are invisible.** A node or sequence flow added to the model
  after registration never enters the already-taken snapshot, so the started
  instance runs a definition the caller can no longer see in their own object.
- **In-place edits leak backward.** Rewiring or mutating an *existing* shared node
  reaches into the snapshot through the shared pointer, so the next instance born
  from that snapshot silently picks the change up — and does so racing any
  instance already cloning the snapshot, with no lock on that path.

This is the architecture audit's finding **§3.3** ("validation and mutability of
the process model"): the model can be mutated after the snapshot is taken and
during execution. The audit's proposed remedy was to *freeze* the model after the
first snapshot. This ADR reaches the same safety by the opposite, more composable
route — see §2.3 and §4.1.

### 1.3 Prior art — Camunda definition versioning

Camunda (a mature BPMN engine) resolves exactly this ambiguity with **definition
versioning**, and its model is widely understood by BPMN practitioners:

- Redeploying a changed definition under the same **key** creates a *new version*;
  versions increment as integers. *"If you redeploy a changed process definition,
  you get a new version in the database."*
- **Running instances are unaffected:** *"Running process instances will continue
  to run in the version they were started in. New process instances will run in
  the new version — unless specified explicitly."*
- **Starting picks a version deterministically:** by **key** starts *"an instance
  of the latest deployed version"*; by a specific **id** starts that exact
  version.
- **Auto-start subscriptions move to the latest version:** *"Upon deployment of a
  new version of a process definition, the signal subscriptions of the previous
  version are canceled"* — so only the latest version starts new instances from a
  message/signal start trigger; older versions merely finish their in-flight work.

This ADR adopts that model, mapped onto gobpm's snapshot/instance-starter
vocabulary. (SAD-001 already lists platform **versioning** as a tracked epic;
this is the in-memory registration foundation for it, not the durable
deployment/migration story, which stays deferred — §2.8.)

## 2. Decision

### 2.1 Registration is versioned by the process id (the BPMN key)

The **versioning key** is the process **id** — BPMN's stable element identity. A
caller fixes it explicitly (the BPMN `id` attribute; in gobpm, the process-id
option). Two registrations carrying the same id are two **versions** of one
logical definition; versions increment as integers (v1, v2, …) per key in
registration order. A version number is **monotonic and never reused** for the
lifetime of the key: removing a version (§2.5) leaves its number retired, so the
sequence may carry gaps (v1, v3, …) and a later registration always takes a fresh
higher number rather than refilling a hole. A version number therefore names one
definition unambiguously for as long as the key lives.

A process registered **without** an explicit id keeps gobpm's default — a
generated unique id — and is therefore its own singleton key at version 1. That
is the correct outcome: an anonymous definition has no shared identity to form a
version lineage with, and silently merging two unrelated anonymous processes
would be worse than treating each as distinct.

The id is chosen over the process **name** deliberately. BPMN cross-references
elements **by `id`** — every `(ref)` in the model is an id reference to another
element — whereas `name` is a plain optional attribute with no referential role.
The `id` is therefore the element's identity and `name` is display text; keying
versions on a display name would collide two genuinely different processes that
happen to share a label into one lineage. (See §4.2 for the name-keyed
alternative and why it is rejected.)

### 2.2 `RegisterProcess` returns a registration handle

`RegisterProcess` **returns a value** — a **registration handle** — instead of
only an error. The handle is the first-class receipt for *this exact registered
version*. It carries (conceptually) the **key** (process id), the integer
**version**, and an opaque **registration id**, and it is what the caller passes
to start or unregister that version.

```
reg, err := th.RegisterProcess(p)   // reg names a specific (key, version)
inst, err := th.StartProcess(reg)   // starts THAT version, unambiguously
err = th.Unregister(reg)            // removes THAT version
```

This removes the §1.1 ambiguity at the root: the caller no longer reaches into
the model for an id and hopes the engine still holds the shape they mean — they
hold a receipt for the precise version they registered.

### 2.3 A version is frozen at registration — snapshot isolation

Taking the snapshot at registration **deep-copies the definition's node graph**,
so the snapshot owns its own nodes and flows, fully independent of the model
object. From that instant the version is genuinely immutable: later edits to the
model — structural or in-place — touch nothing the engine holds, and a started
instance always runs the shape that existed at *its* registration.

This is the mechanism that lets the model stay mutable **without** the audit's
freeze. Isolation, not prohibition, is what makes "the engine accepts an
immutable Snapshot" (SAD-001) actually true, and it closes the §1.2 / audit §3.3
leak and its attendant data race by construction: there is no longer any shared
mutable object between the caller's editing goroutine and the engine's cloning
path. (Immutable per-definition configuration that an instance never rewrites —
e.g. declared properties — may still be shared by reference; only the mutable
graph must be copied. The per-instance clone that an running instance mutates is
unchanged and still taken from the frozen snapshot.)

### 2.4 Starting — three addressing modes

A caller can name the version to start in three ways, ordered from most to least
specific:

- **By handle** — `StartProcess(reg)` starts the exact version the handle names.
  The canonical, unambiguous path, and the only one that needs no lookup.
- **By key + version** — `StartVersion(key, version)` starts that specific
  integer version of the key, or errors if the key or version is unknown. It
  addresses by the version **number**, not a slice position, so it stays correct
  across the gaps a removal can leave (§2.5) — re-running v3 after v2 was pruned
  resolves v3, and the retired v2 errors. This addresses a particular historical
  version *without* holding its handle (the human-friendly `(key, version)` rather
  than the opaque registration id).
- **By key (latest)** — `StartLatest(key)` starts the **latest** registered
  version of the key, or errors if the key is unknown. This mirrors Camunda's
  common "just run the current one" case, so the everyday call need not thread a
  handle or track a version number.

All three return the per-instance observation handle that `StartProcess` returns
today (unchanged — distinct from the registration handle of §2.2). The exact
method names/signatures are finalized in the implementing SRD; conceptually the
engine exposes handle-addressed, `(key, version)`-addressed, and latest-of-key
starts.

Because removals can leave gaps in a key's version sequence (§2.5), the engine
also exposes a **discovery** path: enumerating a key's registered versions
(returning their handles) so a caller can see which versions exist — and pick one
to start or unregister — rather than guessing a number. Enumeration is read-only
discovery, the registration-side analogue of the SRD-019 instance/starter views.

### 2.5 Auto-start follows the latest version — supersede on register, promote on remove

For a process registered in auto-instantiation mode (a message/signal start
trigger spawns instances — [ADR-015 v.1](ADR-015-event-triggered-instantiation.md)),
registering a **newer version of the same key** transfers auto-start to it: the
previous version's instance-starters are torn down from the engine event bus and
the new version's are registered in their place. Only the **latest** version
spawns new instances from a trigger; instances already running under older
versions continue to completion on their own frozen snapshots.

This is the direct analogue of Camunda's *"the signal subscriptions of the
previous version are canceled"* (§1.3), and it resolves the otherwise-sharp edge
of "do all registered versions react to the same incoming message?" — they do
not; the latest version owns the trigger. Manual-start versions register no
starters, so there is nothing to supersede.

The supersession is symmetric on removal: **un**registering the latest version
**promotes** the now-newest remaining version — its starters are registered in
turn, so it becomes the live auto-start version. The invariant *the latest
registered version is the live auto-start version* therefore holds at all times,
in both directions. This mirrors Camunda's delete-deployment behaviour (the
previous version's start-event subscriptions re-activate) and gives users a
direct lever to re-activate an earlier version's auto-start: remove the later
versions until the wanted one is latest again. A previous (non-latest) version
remains startable, but **only manually** via `StartVersion(key, n)` — it holds
no hub subscriptions while a newer version exists.

Removal comes in two granularities, named for their scope: removing **one
version** (by handle) versus removing the **whole process** — every version of a
key — in one operation. A *process* is the whole keyed definition; a *version* is
one snapshot of it, so the bulk operation carries the `Process` name and the
single-version one is explicitly a *version* removal. Both leave running instances
untouched (they own their snapshots); only the live latest's starters are ever
torn down. Whole-process removal also retires the key's version counter, so a
later registration of that id restarts at v1.

Correlation/dedup of an in-flight conversation ([ADR-016
v.1](ADR-016-message-correlation.md)) remains keyed within the spawning version's
lineage; because only the latest version auto-starts, create-or-join stays
coherent. Cross-version correlation (a conversation begun under v1 routed into a
v2 instance) is **not** a goal here — §2.8.

### 2.6 The model stays mutable — editing is a sibling decision

The process model is **not** frozen by registration. A caller may keep building or
revising it and re-register to mint the next version. This ADR commits to that
*principle*; it deliberately does **not** specify the editing operations
themselves. The first-class model-editing surface — un-linking flows, removing
nodes, clearing a process, and the validation/ordering rules those imply — is a
distinct decision with its own object-model reasoning and is owned by the sibling
**rich process-model editing ADR**. Snapshot isolation (§2.3) is the precondition
that makes such editing safe to land independently: once registration copies the
graph, post-registration editing cannot disturb any taken version.

### 2.7 Concurrency discipline of the versioned registry — owned by the SRD

Introducing versions reshapes the engine registry from "one snapshot per id" to
"an ordered set of versions per key, with the latest distinguished," and it
rewrites the very registration/start/unregister methods whose lock discipline the
architecture audit flagged as **fragile** (audit §2.6: correctness resting on a
comment, *"release before launch … re-acquire"*, a refactor-hostile invariant).
Because the implementing work touches exactly those methods, the clean
concurrency discipline the audit asks for — engine state as an atomic value, and
an explicit, documented split between lock-held and lock-free sections — is
adopted **as part of landing this ADR**, recorded as an implementation decision in
its **dedicated concurrency SRD**, separate from the versioning SRD. This ADR
fixes the *what* (versions, handle, isolation, latest-supersedes); the SRD owns
the *how* of the registry's internals and retires audit §2.6 there.

### 2.8 Non-goals and out of scope (each with a named home)

- **Durable / persistent versioning and migration.** Versions here live in the
  running engine's memory only. Persisting deployments, rehydrating instances onto
  a stored version, and migrating live instances across versions remain the
  deferred platform epics (SAD-001) — a future Persistence & State decision.
- **The model-editing API.** `Unlink` / `Remove` / `Clear` and their semantics —
  the sibling rich-editing ADR (§2.6).
- **The registry concurrency rewrite's mechanics.** The dedicated concurrency SRD
  (§2.7); this ADR carries no file/line.
- **Cross-version correlation and version-pinned message routing.** A conversation
  spanning versions (§2.5) — out of scope; revisit with the durable story.
- **Version tags / semantic version labels.** Camunda's `versionTag` is *"only for
  tagging and will neither influence the start … behaviour."* Integer version
  ordering is sufficient here; named tags are a later ergonomic add if asked.

## 3. Consequences

**Positive.**

- The §1.1 ambiguity disappears: a handle names exactly one version; "start what I
  just registered" is unambiguous, and re-registration is a meaningful operation
  (a new version) instead of a silent no-op.
- The §1.2 / audit §3.3 leak and its race are closed *by construction* via
  isolation (§2.3), without restricting the user — honouring the project's
  "compose, don't restrict" principle.
- Behaviour matches a model BPMN practitioners already know (Camunda), lowering
  the learning curve.
- Multiple shapes of a definition can coexist safely: long-running v1 instances
  finish while v2 takes new starts.

**Negative / costs.**

- **A contract change.** `RegisterProcess` gains a return value; `StartProcess` and
  the single-version `UnregisterVersion` take a handle, with `StartVersion(key,
  version)` / `StartLatest(key)` / `UnregisterProcess(key)` (whole-process removal)
  as the key-addressed paths. Pre-versioning call sites must adopt the handle. This
  is acceptable pre-1.0 and is the point of the change.
- **Registration is no longer idempotent.** Re-registering now *adds a version*.
  Callers that re-registered defensively must stop, or accept version growth.
- **Per-registration copy cost.** Deep-copying the graph at registration adds work
  and memory per version (bounded by definition size, paid once per registration,
  not per instance).
- **Unbounded version accumulation** if a caller re-registers in a loop without
  unregistering. Mitigations (retention/pruning) are noted under §5; the engine
  does not impose a cap in this ADR.

## 4. Alternatives considered

### 4.1 Freeze the model after the first snapshot (the audit's proposal)

Mark the process immutable on registration; reject later edits with an error.
**Rejected** as the primary mechanism: it restricts the user to solve an engine
isolation bug, and it conflicts with the rich-editing direction (§2.6) and the
project's "compose, don't restrict" principle. Snapshot isolation (§2.3) achieves
the same safety while *keeping* the model editable, which is strictly more
composable. (A freeze could still be offered later as an opt-in assertion for
callers who *want* a sealed builder, but it is not the default and not required
for safety.)

### 4.2 Key versions by process name

Use the model's display **name** as the versioning key (the most "Camunda-feel"
ergonomically, since a name is always present). **Rejected:** in BPMN the name is
non-identifying display text; keying on it collides two unrelated processes that
share a label into one version lineage, and silently. The id is the standard's
identity and is the safe key (§2.1). Ergonomics are recovered by letting the
caller set a short, stable id.

### 4.3 Snapshot isolation without a handle (deep-copy only)

Fix only §1.2 — make the snapshot copy the graph — but keep the single-keyed,
id-addressed, idempotent registration. **Rejected as insufficient:** it removes
the leak and the race, but a post-registration edit is then *silently ignored*
(the snapshot is frozen and re-registration is still a no-op), which is the §1.1
confusion the user raised. Isolation is necessary but not sufficient; the handle +
versioning is what makes the user's intent expressible.

### 4.4 Opaque registration id without key grouping

Return an opaque id per registration with **no** notion of a shared key/version
lineage (every registration is an island). **Rejected:** it disambiguates
starting, but loses "latest version of this definition," cannot express auto-start
supersession (§2.5), and discards the well-understood Camunda mental model for no
gain.

## 5. Enterprise-readiness recommendations

- **Version retention policy.** Provide (in the implementing SRD or a follow-up) a
  way to enumerate a key's versions and prune superseded ones once their instances
  drain, so a long-lived engine that re-registers frequently does not accumulate
  dead snapshots. Surface counts via the existing observability surface.
- **Observability of versioning.** Log at registration the assigned `(key,
  version)` and, on auto-start supersession, the from→to version and the
  starter-subscription move, so operators can see which version is live and why a
  trigger spawned the version it did.
- **Explicit-id guidance.** Document that durable, intentional versioning requires
  a stable process id; an anonymous (auto-id) process is always a singleton v1.
  Consider a debug-level warning when two registrations share a *name* but differ
  in id (a likely "I meant to version this" mistake).
- **Deprecation path to durability.** Keep the in-memory model's vocabulary
  (key/version/handle) aligned with the future durable deployment story so the
  persistence ADR can adopt it without renaming the public surface.

## 6. References

- [SAD-001 v.1 Vision & Architecture](SAD-001-vision-and-architecture.md) — the
  parent: the **Snapshot** as the engine's immutable input contract ("Engine
  accepts a Snapshot, not a mutable model"), and the tracked **versioning**
  platform epic this lays the in-memory foundation for.
- [ADR-009 v.1 Per-instance node graph](ADR-009-per-instance-node-graph.md) — the
  snapshot→per-instance clone model this extends; §2.3's registration-time graph
  copy is the missing first hop of that same isolation story.
- [ADR-015 v.1 Event-triggered instantiation](ADR-015-event-triggered-instantiation.md)
  — the definition-level **instance-starters** whose latest-supersedes transfer
  (§2.5) is decided here; the manual-start mode that registers none.
- [ADR-016 v.1 Message correlation](ADR-016-message-correlation.md) — the
  create-or-join resolution that stays coherent under versioning because only the
  latest version auto-starts (§2.5).
- [ADR-001 v.6 Execution model](ADR-001-execution-model.md) — instances, tracks,
  and the per-instance lifecycle that older versions keep running after they are
  superseded.
- [ADR-013 v.1 Instance observability](ADR-013-instance-observability.md) — the
  read-only handle/discovery surface the versioning observability (§5) extends.
- Architecture audit 2026-06-11 — **§3.3** (model mutability after snapshot; this
  ADR resolves it via isolation rather than freeze) and **§2.6** (fragile Thresher
  mutex discipline; retired by this ADR's concurrency SRD, §2.7).
- BPMN 2.0 — the `BaseElement` **`id`** is the attribute other elements reference
  (`docs/bpmn-spec/elements/foundation.md`; the spec's cross-references are `(ref)`
  id-references), whereas **`name`** is a plain optional attribute with no
  referential role — the basis for §2.1's key choice.
- Camunda 7 manual — *Process Versioning* (version on redeploy-by-key; running
  instances stay; start-by-key = latest; `versionTag` is non-behavioural) and
  *Signal Events* (*"the signal subscriptions of the previous version are
  canceled"*) — the prior-art model adopted in §1.3 / §2.5.

## 7. Open questions

None. Scope: **in-memory definition versioning** — versioned registration keyed on
the process id, a registration handle, snapshot isolation at registration time,
start by handle / by key+version / by latest-of-key, and latest-supersedes
auto-start. The
**model-editing API** is the sibling rich-editing ADR; the **registry concurrency
rewrite** (audit §2.6) is this ADR's dedicated concurrency SRD; **durable
versioning, migration, cross-version correlation, and version tags** are named
deferrals (§2.8).

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-06-28 | Ruslan Gabitov | Draft. **Definition versioning**: `RegisterProcess` returns a **registration handle** naming a `(key, version)`; the versioning **key is the process id** (BPMN identity, not the display name), anonymous processes are singleton v1; the snapshot **deep-copies the graph at registration** so each version is frozen and the model stays **mutable** without a freeze (closing audit §3.3's leak/race by isolation); start **by handle** (exact), **by key+version** (specific), or **by key** (latest); **latest-supersedes** auto-start keeps the latest version as the sole live auto-starter — registering a newer version transfers the instance-starters to it, and unregistering the latest promotes the now-newest remaining version back to live (Camunda-grounded, symmetric in both directions). Refines SAD-001 v.1; siblings ADR-009 v.1, ADR-015 v.1, ADR-016 v.1, ADR-001 v.6, ADR-013 v.1. The **model-editing API** is carved out to a sibling rich-editing ADR; the **registry concurrency discipline** (audit §2.6) to a dedicated SRD; durable versioning/migration/cross-version correlation/version tags deferred. |
