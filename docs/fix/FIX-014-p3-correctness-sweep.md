# FIX-014 — P3 correctness sweep (data values, scope, gateway, correlation, observability)

| Field | Value |
|---|---|
| Status | Draft |
| Date | 2026-06-30 |
| Owner | Ruslan Gabitov |
| Related | [ADR-010 v.2 Process data model](../design/ADR-010-process-data-model.md), [ADR-016 v.1 Message correlation](../design/ADR-016-message-correlation.md), [ADR-005 v.4 Gateways and joins](../design/ADR-005-gateways-and-joins.md), [ADR-013 v.1 Instance observability](../design/ADR-013-instance-observability.md) |

One-shot remediation of eleven 🟡 P3 defects surfaced by
`docs/audit/code-review-third-pass-2026-06-29.md` (§3.1, §3.2, §3.3, §3.4, §3.6,
§3.7, §3.10, §3.11, §3.12, §3.13, §3.14). Each is a localized correctness or
fidelity bug with a mechanical fix; none changes a public contract. They are
swept together because individually each is below the bar for its own FIX, and
batching keeps one reviewable change-set with a shared verification pass (the
precedent is FIX-003, the earlier audit-bug sweep).

> **Excluded from this sweep.** Two neighbouring §3.x findings need design work
> rather than a patch and are parked in `docs/audit/audit-backlog.md`: §3.5
> (Unspecified-gateway merge-or-split — an unverified BPMN standard-claim + a
> validation-policy decision → **AB-003**, ADR-005 work). Two more are already
> fixed: §3.8 (cyclic timer N+1 → **FIX-012**) and §3.9 (starter reconcile
> mid-loop → **FIX-013** §1.3). The CI/build-hardening findings (§3.15–§3.18)
> are a separate cluster, not this one.

## 1. Symptoms

- **1.1 (§3.2) `Array.Insert` cannot insert at `index == len`.** `Insert`
  (`pkg/model/data/values/array.go:286`) validates the position with
  `checkIndex`, which rejects `index > len-1` (`:336`) and rejects an empty
  collection (`checkForEmpty`, `:346-352`). So a value can never be inserted at
  the end of the array, nor into an empty array — the append position `[0, len]`
  the operation should accept is truncated to `[0, len-1]`.
- **1.2 (§3.3) `Array.Clone` resets the iteration cursor.** `Clone`
  (`array.go:97-103`) returns `NewArray[T](a.elements...)`, and `NewArray` always
  sets `index = 0` for a non-empty array (`:34`). A source array positioned at
  cursor `index = k` clones to a copy positioned at `0` — the cursor state is
  silently lost across a clone.
- **1.3 (§3.4) `Array.Delete` skips its notification when emptying the array.**
  `Delete` (`array.go:312-324`) returns early when the removal empties the
  collection (`:314-317`, `a.index = -1; return nil`) **before** reaching
  `a.notify(data.ValueDeleted, …)` (`:324`). Deleting the last element changes
  the collection but fires no `ValueDeleted` callback; deleting any other element
  does fire one — an inconsistent observation contract.
- **1.4 (§3.1) `scope.namesFrom` omits a `/`-keyed root scope.** `namesFrom`
  (`internal/scope/scope.go:159-181`) builds `prefix = from.String() +
  PathSeparator` and keeps a scope when `path == from ||
  strings.HasPrefix(prefix, path.String()+PathSeparator)`. For the root
  `from = "/"`, `prefix` becomes `"//"`, and no descendant path (`"/Proc"`, …)
  satisfies the `HasPrefix` test, so a root-keyed scope's names are dropped from
  enumeration.
- **1.5 (§3.6) Default-flow routing stores the caller's pointer, not the member.**
  `UpdateDefaultFlow` (`pkg/model/gateways/gateway.go:163-187`) verifies the
  passed flow `f` is one of the gateway's outgoing flows by **ID**, then stores
  `g.defaultFlow = f` (`:178`) — the caller's pointer, not the matched member
  `sf`. Routing later selects the default by **pointer identity** (`:255`,
  `if of == g.defaultFlow`), so a caller passing a different pointer with the
  same ID would store a flow that pointer-comparison never re-selects.
- **1.6 (§3.7) `errs.M` format verbs with no arguments in the unregister path.**
  `track.unregisterEvent` (`internal/instance/track.go:893-896`) builds
  `errs.M("node %q[%s] doesn't implement flow.EventNode interface")` — two verbs,
  zero args — so the message renders as `node %!q(MISSING)[%!s(MISSING)] …`. The
  error also lacks the `errs.C(errorClass, …)` classification its siblings carry.
- **1.7 (§3.10) `DeriveKey` accepts a present-but-nil value as a key part.**
  `DeriveKey` (`pkg/model/msgflow/correlation.go:88-101`) guards `val == nil`
  (`:97-99`) but then appends `fmt.Sprintf("%v", val.Get(ctx))` (`:101`) without
  checking whether `val.Get(ctx)` itself is nil. A `data.Value` that is present
  but holds no value (an unset optional field) yields a `"<nil>"` key part rather
  than failing correlation — the doc-comment requires `ok == false` when a
  property yields no value.
- **1.8 (§3.11) `clocktest.Advance` moves the clock backwards.** `Advance`
  (`pkg/clock/clocktest/clocktest.go:56-62`) applies `c.now = c.now.Add(d)`
  (`:60`) with no sign check, so a negative duration rewinds the fake clock —
  while the sibling `Set` silently ignores a non-forward move (`:70`,
  `if t.After(c.now)`). A rewound test clock violates the monotonicity timer
  waiters assume.
- **1.9 (§3.12) `Message.Clone` drops `BaseElement` documentation.**
  `Message.Clone` (`pkg/model/bpmncommon/message.go:82-102`) rebuilds the
  `BaseElement` from the id alone
  (`foundation.MustBaseElement(foundation.WithID(m.ID()))`, `:98`), discarding the
  source's documentation. A cloned message loses its BPMN `documentation`
  annotations.
- **1.10 (§3.13) `memmetrics.seriesKey` collides distinct attribute sets.**
  `seriesKey` (`pkg/observability/memmetrics/memmetrics.go:209-224`) formats each
  attribute as `fmt.Sprintf("%s=%v", a.Key, a.Value)` (`:220`). `%v` over `any`
  renders `int(1)`, `int64(1)` and `"1"` identically, so series with
  type-distinct attribute values collide onto one key. (Latent — no production
  emit sites yet, the recorder is opt-in.)
- **1.11 (§3.14) `memtrace.liveSpan` mutates span state without synchronization.**
  `liveSpan` (`pkg/observability/memtrace/memtrace.go:72-96`) reads/writes
  `s.data` and `s.ended` in `End`/`SetAttributes`/`RecordError`/`SetStatus` with
  no lock, while the OTel-shaped contract it models permits concurrent use of a
  span. (Latent — the tracer defaults to noop and is unwired, so no concurrent
  path exists today.)

## 2. Root-cause analysis

- **1.1–1.3**: `Array`'s mutators were written against the common path. `Insert`
  reused `checkIndex` (a *random-access* bound, `[0, len)`) where it needed an
  *insertion* bound (`[0, len]`); `Clone` reused `NewArray` without carrying the
  cursor; `Delete`'s empty-collection early-return predates the notification line
  and was never re-threaded through it.
- **1.4**: `namesFrom` models ancestry by string-prefix over `path + separator`,
  which is correct for every non-root key but degenerates for the root, where
  `"/" + "/"` cannot prefix a single-separator child.
- **1.5**: `UpdateDefaultFlow` validates by ID but stores the input, mixing a
  by-ID contract with a by-pointer consumer.
- **1.6**: a copy-paste of a sibling error message lost its arguments and class.
- **1.7**: the nil-guard covers the `Value` wrapper but not the wrapped payload.
- **1.8**: `Advance` was written as a thin `now.Add` without the forward-only
  invariant its sibling `Set` already encodes.
- **1.9**: `Clone` reconstructs the `BaseElement` from id only instead of copying
  it, so non-id base state (documentation) is not carried.
- **1.10–1.11**: observability internals were stubbed to the happy path; the
  type-erasing `%v` key and the unguarded span fields were never exercised
  because the subsystems are opt-in/unwired.

## 3. Solution

### 3.1 Considered alternatives
- **1.1 — keep `checkIndex` and special-case `index == len`**: rejected — the
  cleaner fix is an `Insert`-specific bound (`[0, len]`) that also admits the
  empty-array case, rather than bolting an exception onto a random-access guard.
- **1.5 — change the routing comparison to compare by ID** (instead of fixing the
  stored pointer): viable, but storing the verified member `sf` is the smaller,
  more local fix and keeps the existing pointer-identity routing intact. Both are
  applied defensively where cheap (store `sf`; the comparison stays as-is).
- **1.11 — document single-goroutine confinement** instead of locking: rejected
  (owner decision) — add a per-span mutex so the span honours the concurrent-use
  contract its OTel shape implies, even while the tracer is unwired, rather than
  encoding a confinement the contract doesn't promise.

### 3.2 Per-site changes
- **3.2.1** `array.go` `Insert` (`:286`) — replace the `checkIndex` call with an
  insertion-range check that accepts the end position and the empty array:
  `if idx < 0 || idx > len(a.elements) { return errs.New(errs.M("insert index %d is out of range (len: %d)", idx, len(a.elements)), errs.C(errorClass, errs.OutOfRangeError)) }`;
  and when the insert grows an empty collection, set `a.index = 0` so `Get`
  works (mirroring `NewArray`/`Add`).
- **3.2.2** `array.go` `Clone` (`:97-103`) — preserve the cursor:
  `clone := NewArray[T](a.elements...); clone.index = a.index; return clone`
  (under the held lock).
- **3.2.3** `array.go` `Delete` (`:312-324`) — fire the notification on the
  emptying path too: move `a.notify(data.ValueDeleted, index)` ahead of the
  `if len(a.elements) == 0 { a.index = -1; return nil }` block (or notify before
  the early return), so every successful delete emits exactly one `ValueDeleted`.
- **3.2.4** `scope.go` `namesFrom` (`:159-181`) — admit the root scope: treat
  `path == from` (already present) and the root explicitly, e.g.
  `if path == from || strings.HasPrefix(path.String()+PathSeparator, prefix)`
  reframed so a `/`-keyed scope enumerates, or short-circuit
  `from.String() == PathSeparator` to include every scope under root. The fix
  must make a root `from` enumerate its descendants without regressing non-root
  prefixes.
- **3.2.5** `gateway.go` `UpdateDefaultFlow` (`:178`) — store the verified member,
  not the caller's pointer: `g.defaultFlow = sf`.
- **3.2.6** `track.go` `unregisterEvent` (`:893-896`) — supply the missing args
  and the error class:
  `errs.M("node %q[%s] doesn't implement flow.EventNode interface", n.Name(), n.ID())`
  plus `errs.C(errorClass, errs.TypeCastingError)` (matching the file's sibling
  type-assertion errors).
- **3.2.7** `correlation.go` `DeriveKey` (`:101`) — guard the unwrapped payload
  before using it: `raw := val.Get(ctx); if raw == nil { return "", false, nil };
  parts = append(parts, fmt.Sprintf("%v", raw))`, so a present-but-empty value
  fails correlation (`ok == false`) per the doc-comment and ADR-016.
- **3.2.8** `clocktest.go` `Advance` (`:56-62`) — ignore a non-forward move,
  exactly mirroring `Set`'s forward-only rule (`:70`): `if d <= 0 { c.fireDueLocked(); return }` before `c.now = c.now.Add(d)` (no `errs` import — `clocktest` is a leaf test helper). A backward `Advance` thus leaves `now` unchanged rather than rewinding.
- **3.2.9** `message.go` `Clone` (`:82-102`) — carry the base documentation:
  clone the whole `BaseElement` (copy the source's documentation onto the new
  message) instead of rebuilding it from the id alone.
- **3.2.10** `memmetrics.go` `seriesKey` (`:220`) — make the key type-aware:
  `fmt.Sprintf("%s=%T:%v", a.Key, a.Value, a.Value)`, so type-distinct attribute
  values no longer collide.
- **3.2.11** `memtrace.go` `liveSpan` (`:72-96`) — add a `sync.Mutex` to
  `liveSpan` and guard `End`/`SetAttributes`/`RecordError`/`SetStatus` (and the
  `ended` check) so concurrent span use is race-free.

## 4. Verification

### 4.1 Tests
| Test | Asserts |
|---|---|
| `TestArrayInsertAtEnd` | `Insert` at `index == len` appends; `Insert` into an empty array at 0 succeeds and `Get` returns it (1.1) |
| `TestArrayCloneKeepsCursor` | an array advanced to cursor `k`, cloned, reports cursor `k` on the clone (1.2) |
| `TestArrayDeleteLastNotifies` | deleting the final element fires exactly one `ValueDeleted` callback (1.3) |
| `TestScopeNamesFromRoot` | a `/`-keyed root scope enumerates its names via `namesFrom`/`List` (1.4) |
| `TestUpdateDefaultFlowStoresMember` | after `UpdateDefaultFlow`, routing selects the default even when the call passed a same-ID different pointer (1.5) |
| `TestUnregisterEventNonEventNodeError` | the non-`EventNode` error message interpolates name+id (no `%!q(MISSING)`) and carries the error class (1.6) |
| `TestDeriveKeyPresentButNilValue` | a property whose `Value.Get` is nil yields `ok == false`, not a `"<nil>"` key part (1.7) |
| `TestClockAdvanceRejectsBackward` | `Advance(-d)` is a no-op, leaving `now` unchanged; forward `Advance` still fires due timers (1.8) |
| `TestMessageCloneKeepsDocs` | a message with documentation, cloned, retains the documentation (1.9) |
| `TestSeriesKeyTypeDistinct` | `int64(1)` and `int(1)` (or `"1"`) attribute values produce different series keys (1.10) |
| `TestLiveSpanConcurrentUse` (`-race`) | concurrent `SetAttributes`/`SetStatus`/`End` on one span is race-free (1.11) |

## 5. Prevention
Each fix replaces a happy-path shortcut with the full-range/observable/classified
behaviour, and each lands with a test that pins the previously-untested edge
(end-insertion, cursor-after-clone, empty-delete notification, root enumeration,
default-flow identity, defensive-error formatting, nil payload, clock
monotonicity, clone fidelity, series-key distinctness, concurrent span use), so
the class can't silently reappear.

## 6. Regressions
No public API signatures change. `Array.Insert` widens its accepted index range
(`[0, len]`) — strictly more permissive, no previously-valid call regresses.
`Array.Delete` adds a callback emission on the emptying path (new observation,
no behavioural change for existing non-callback users). `clocktest.Advance` now
rejects a backward move — a test-only tightening that only affects misuse.
The remaining changes are diagnostic/fidelity/internal and behaviour-neutral for
existing callers.

## 7. Related
ADR-010 v.2 (process data model — the `Array`/`Collection` value semantics of
1.1–1.3 and the scope model of 1.4). ADR-016 v.1 (message correlation — the
key-derivation contract 1.7 restores: no key part from an absent value).
ADR-005 v.4 (gateways and joins — the default-flow routing of 1.5; note §3.5's
merge-or-split question is parked as audit-backlog **AB-003**, ADR-005 work).
ADR-013 v.1 (instance observability — the metrics/trace recorders of 1.10–1.11).
The §3.5 (AB-003), §3.8 (FIX-012) and §3.9 (FIX-013) third-pass findings are out
of this sweep's scope (see the intro note).

## 8. Implementation summary
*(filled at landing.)*

## 9. Open questions
None.
