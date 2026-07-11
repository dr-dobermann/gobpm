# ADR-022 — Error Propagation and Logging Policy

| Field | Value |
|---|---|
| Status | Accepted |
| Version | v.1 |
| Date | 2026-07-10 |
| Owner | Ruslan Gabitov |
| Refines | [ADR-002 v.2](ADR-002-extension-architecture.md) (the observability extensions and the visible-by-default posture), [SAD-001 v.1](SAD-001-vision-and-architecture.md) (library-first: the embedder owns the process, the engine owes it diagnosability) |
| Siblings | [ADR-013 v.1](ADR-013-instance-observability.md) (the host observation stream — a separate channel from operator logs) |

gobpm users must get **comprehensive information about every error and every
important event** in the engine. This ADR is the single contract for how an
error travels (propagation), where it is allowed to stop (handling), what gets
logged, at which level, with which attributes — and what is forbidden: silent
discards and duplicate logging. It is a continuously-current policy: code
review and the style sweep judge against it, and the accompanying remediation
FIX brings the existing code up to it.

---

## 1. Context & problem

gobpm is a **library** (SAD-001 v.1): the embedder's application is the
process, so the engine's errors and logs are the embedder's *only* windows
into engine trouble. Three problems motivate a policy:

1. **Silent discards exist.** An owner review found error-returning calls
   whose results are dropped with `_ =` on production paths (the message
   waiter's fire report to the hub being the precedent). A swallowed error
   turns an engine defect into an undiagnosable symptom somewhere else —
   the same failure class the validate-all-parameters rule exists for, on
   the outbound side.
2. **The conventions are folklore.** The codebase already leans the right
   way — visible-by-default logging (ADR-002 v.2), `Warn` for best-effort
   degradation, `Debug` for per-event flow — but none of it is written down,
   so each new subsystem re-decides, and drift accumulates (the same entity
   is logged as `instance` in one file and `instance_id` in another).
3. **The naive fix is worse than the defect.** Mechanically adding a log
   next to every error creates the log flood: one failure reported at five
   stack levels, each with partial context. Volume without discipline
   destroys the readability the logs exist for.

BPMN 2.0 is **silent** on engine observability — logging is entirely an
engine choice, so this policy is grounded in engineering practice, not the
standard (§3).

## 2. Decision

### 2.1 Every error is handled **exactly once**

An error has exactly two legal fates, and each occurrence picks **one**:

- **Propagate** — wrap it with context and return it to the caller. A
  propagated error is NOT logged at the propagation site; the caller now
  owns it.
- **Handle** — at a boundary where no caller above can act (§2.3): log it
  with full context and decide the consequence (fault the instance, degrade,
  drop with reason).

**Log-and-return is forbidden.** It is the single cause of log floods: the
same failure reported at every level of the call stack, none with complete
context. One failure ⇒ at most one log record.

### 2.2 Propagation carries context; parallel errors are joined

Three propagation patterns, chosen per site:

| Situation | Pattern |
|---|---|
| A lone error-returning call ends the function | `return f(...)` — don't capture-and-discard |
| An error is already in flight and a second occurs (teardown, follow-up report) | `errors.Join(err, err2)` — both reach the handler |
| Adding engine context on the way up | wrap: `errs.New(errs.M(...), errs.E(err), errs.D(...))` or `fmt.Errorf("...: %w", err)` per the package's prevailing style |

A wrap names the operation that failed and carries the identifying
attributes (§2.6) so the eventual single log record is complete.

### 2.3 The handling boundaries — the only places that log errors

An error is logged exactly where nothing above can act on it:

1. **Goroutine tops** — the instance loop, a track's terminal fault path, a
   waiter's service goroutine, the hub's run loop, a dispatcher worker.
   Nothing is above them to return to.
2. **Best-effort operations** — calls whose failure must not fail the flow
   (task distribution/withdrawal, observer notification, receiver-subscription
   extension). Logged at `Warn`/`Debug` (§2.5) and the flow continues; the
   log **is** the handling.
3. **Deliberate ignores** — a site where the error is provably
   inconsequential keeps BOTH a log (at `Debug`) AND a comment stating *why*
   ignoring is safe. A bare `_ = f()` on an error-returning call is
   **forbidden** in production code.

**Best-effort vs fail-fast — judge by the failure surface, not the call
site.** An operation is best-effort (boundary class 2) only if its failure
modes are genuinely inconsequential — a transient or external hiccup the flow
can shrug off (a distributor timeout, an observer that errored, an idempotent
op that no-ops on a miss). If the *only* way an operation can fail is an
**invariant violation** — a state that cannot happen unless something upstream
is already broken (a waiter absent from the registry it registered into) — its
failure is **not** best-effort: it signals a corrupted environment, and doing
more work on that environment is the worse outcome. Such a failure
**propagates (fail-fast)** so the boundary above stops and logs it. Read what
the called function actually returns an error for before classifying — the
call site's shape (a goroutine top, a "best-effort" comment) suggests, but the
error surface decides.

The **public API edge is NOT a logging boundary**: an error returned to the
embedder is itself the comprehensive report (self-identifying per the
validate-all-parameters rule); logging it as well would double-report a
failure the embedder already owns.

**Components with no logger in scope.** Some units legitimately hold no
`observability.Logger` — the pure `pkg/model` constructors (a `Clone`, an
`AddFlow`) and the `pkg/interactor/console` driver, which *is* itself an
output channel. There, §2.3(3)'s log-half is unimplementable, so the policy
is satisfied a tier up: **propagate** (a model constructor returns the error;
no behavior change where the invariant it asserts truly holds), and where
neither propagation nor a logger is available (the console writer's own
best-effort output), a **why-comment alone** is the documented exception. A
bare `_ = f()` is still forbidden — the minimum is the comment; the intent
must be explicit.

### 2.4 Level discipline — write for the reader

| Level | Meaning | Reader | Examples |
|---|---|---|---|
| `Error` | An actionable failure handled here: engine state was affected | operator paged at night | instance faulted, waiter terminally failed |
| `Warn` | Degraded but continuing; someone should look eventually | operator on the dashboard | distributor timeout, retry scheduled, correlation derivation failed |
| `Info` | A lifecycle milestone a user expects to see | embedder during bring-up | engine start/stop, process registered, startup config (ADR-002 v.2) |
| `Debug` | Flow tracing for diagnosis | developer with a repro | per-event loop dispatch, delivery drops with reason, waiter add/remove |

Two corollaries: a **hot path** (per-event, per-token, per-message) never
logs above `Debug`; and an **expected no-op** (a losing deferred-choice arm,
a signal with no catcher) is `Debug` with the drop *reason* — expected
behavior is not a warning.

### 2.5 One attribute vocabulary

Log records identify their subject with **canonical snake_case keys**, the
same everywhere. The vocabulary (established against the code, not from
memory):

| Domain | Canonical keys |
|---|---|
| Instance / flow | `instance_id`, `track_id`, `node_id`, `node_name`, `process_id`, `start_node_id` |
| Human / worker tasks | `task_id`, `job_id`, `worker_id`, `topic` |
| Events / waiters | `event_definition_id`, `event_definition_type`, `event_processor_id`, `waiter_id`, `signal`, `message_name` |
| Correlation | `correlation_key` (the key **name**), `correlation_value` (its derived **value**) |
| The error | `error` |

Rules:

- **One entity, one key** — no per-file synonyms (`instance` vs `instance_id`,
  `track`/`node`/`message` vs their `_id`/`_name` forms).
- **The error travels under `error`** as its message string (`err.Error()`),
  never a raw `err` object and never under a bumper key (`report_error`,
  `fault`) — *unless* a single record genuinely carries two distinct errors,
  which is the only case a second error-key is allowed and must be named for
  what it is.
- **`correlation_key` is the key name; `correlation_value` is the derived
  value** — the two were conflated (a `key` attr held values, `correlation_key`
  held names); they are now distinct.
- **Descriptive / count attributes are free-form** (`attempts`, `backoff`,
  `deadline`, `cap`, `duration`, a `processors`/`catchers` count) — the
  canon governs *entity references*, not every attribute.
- **New entity keys join by a version bump** of this ADR, not ad hoc.

### 2.6 Silence is opt-out, never accidental

The engine's observability defaults to **visible** (`slog.Default()`,
ADR-002 v.2); an embedder that wants silence configures it explicitly.
Accidental silence — a discarded error, a nil logger erasing the default, a
missing log on a handling boundary — is treated as a defect, and a worse one
than accidental noise: noise is annoying, silence is undiagnosable.

### 2.7 Logs and the observation stream stay separate channels

Operator logs (this policy) and the host observation stream (ADR-013 v.1)
serve different readers and stay decoupled: `ObsEvent`s are the embedder's
programmatic feed (lossy-by-design, per-observer), logs are the operator's
narrative. An event may legitimately appear on both — as data on the stream,
as a record in the log — but neither replaces the other, and log volume
decisions never assume "the observer saw it".

## 3. Practice grounding

- **Handle-errors-once** is the established Go-community principle
  (formulated in D. Cheney's *Don't just check errors, handle them
  gracefully*, 2016 — an error should be handled only once, and logging an
  error is handling it; echoed by the Go blog's *Errors are values*): hence
  log-or-return, never both.
- **Structured key-value logging** with a leveled logger follows the stdlib
  `log/slog` model the project already builds on (`observability.Logger`,
  ADR-002 v.2 §4.3) — attributes over interpolated strings, so records are
  machine-filterable.
- **Wrap-with-`%w` / `errors.Join`** are the stdlib propagation idioms
  (Go 1.13 wrapping; Go 1.20 multi-errors); the project's `errs` package is
  the house wrapper carrying classes and details (reflection-free by
  design).
- **BPMN 2.0 is silent on logging and engine observability** — no
  standard-conformance constraints apply; this is an engine choice
  documented as policy.

## 4. Alternatives considered

| Alternative | Assessment |
|---|---|
| **No policy** — judge case by case in review | ❌ Rejected: the drift is empirical (silent discards shipped; attribute synonyms exist). Folklore doesn't survive subsystem turnover. |
| **Log everywhere** — add a log beside every error, keep returning | ❌ Rejected: guarantees duplicate reports and the log flood; the reader loses the one record that has full context. |
| **Linter-only enforcement** (errcheck et al.) without a policy doc | ❌ Rejected as sole measure: `_ =` is precisely the idiom that *silences* errcheck, and no linter judges meaningfulness, level choice, or attribute naming. Linters enforce the mechanical floor (§5); the policy owns the judgment. |
| **Policy ADR + remediation sweep + review discipline** | ✅ Chosen: a short continuously-current contract, one dedicated pass to bring existing code up to it, then enforcement in review and the style sweep. |

## 5. Consequences

- **Behavioral changes at swept sites.** Removing a discard changes the
  contract: a previously swallowed failure now propagates (or faults, or is
  visible). Each remediated site is a small behavior change and needs its
  test — the reason the remediation is a dedicated, reviewed pass rather
  than a bulk edit.
- **The style sweep gains house rules**: no bare `_ =` on error-returning
  calls; log-and-return flagged; attribute keys checked against §2.5.
- **The mechanical floor can tighten**: with discards remediated, lint
  settings that forbid new ones (errcheck's `check-blank`) become adoptable
  without a red wall.
- **Error paths become tested paths.** The touched-function coverage
  standard applies to every newly reachable branch the sweep exposes.
- The policy is versioned: level definitions and the attribute vocabulary
  evolve by bumping this ADR, keeping one source of truth.

## 6. Enterprise-readiness recommendations

- **Environment profiles**: `Debug` in development, `Info` in staging,
  `Warn`+ for production dashboards — the level semantics of §2.4 are chosen
  so these cutoffs are meaningful.
- **Machine-readable output**: the `observability.Logger` seam accepts any
  slog handler; production embedders should use a JSON handler so the
  canonical keys (§2.5) drive alerting and correlation directly.
- **Cross-channel correlation**: `instance_id` is the join key across logs,
  the ObsEvent stream (ADR-013 v.1), and any future tracing export — dashboards
  should treat it as the primary index.
- **Future**: when the Incident concept lands (deferred from the service-task
  work), incidents become the `Error`-level anchor records and today's
  fault logs attach to them; a dropped/deduplicated-error metric on the
  handling boundaries would make §2.1's "exactly once" observable itself.

## 7. Rollout plan

1. This policy lands first (Accepted) — the contract to sweep against.
2. **Error-discard remediation.** The accompanying remediation FIX
   inventories every silent discard repo-wide and classifies each against
   §2.1–§2.3 (return / join / log-at-boundary /
   deliberate-ignore-with-comment), adding tests for newly reachable error
   paths.
3. **Log audit — the existing records against the policy.** Every current
   log statement is judged, in the same FIX:
   - **removed** where it violates §2.1 (a log beside a returned error — the
     duplicate report) or §2.4's volume corollaries (above-`Debug` on a hot
     path, a warning for expected behavior);
   - **added** where a handling boundary (§2.3) is silent today — a
     goroutine top or best-effort operation whose failure currently leaves
     no record;
   - **re-leveled and re-keyed** where the level contradicts §2.4 or the
     attributes deviate from the §2.5 vocabulary (the `instance` →
     `instance_id` class), so one failure reads as one complete record.
4. Review discipline and the style sweep enforce the policy forward; lint
   tightening (§5) follows once the codebase is clean.

## 8. References

- ADR-002 v.2 — extension architecture: the `observability.Logger` seam and
  the visible-by-default startup posture this policy generalizes.
- ADR-013 v.1 — instance observability: the host observation stream §2.7
  keeps distinct from operator logs.
- SAD-001 v.1 — library-first vision: the embedder-owns-the-process premise
  behind "the public API edge returns, never logs".
- D. Cheney, *Don't just check errors, handle them gracefully* (GoCon 2016);
  Go blog, *Errors are values* — the handle-once principle.
- Go release notes 1.13 (error wrapping), 1.20 (`errors.Join`).

## Open questions

None.

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-07-11 | Ruslan Gabitov | Accepted (authored 2026-07-10). The error-propagation and logging contract: handle-exactly-once (log XOR return); three propagation patterns (lone-call return, errors.Join, contextual wrap); the enumerated handling boundaries (goroutine tops, best-effort ops, deliberate ignores with log+comment) with the public API edge explicitly NOT a logging boundary, a **fail-fast-vs-best-effort discriminator** (judge by the failure surface — an invariant-only failure propagates, not logs — added during implementation from the WaiterFired finding), and a carve-out for logger-less components (model constructors, console driver) that propagate or comment; level discipline (Error/Warn/Info/Debug with hot-path and expected-no-op corollaries); the canonical attribute vocabulary (grounded against the code: adds `event_definition_type`/`event_processor_id`/`worker_id`/`topic`/`start_node_id`, splits `correlation_key`/`correlation_value`, frees count attributes); silence-is-opt-out; logs vs ObsEvent stream separation. Grounded in Go practice (BPMN is silent on observability). Landed by its accompanying FIX (the discard sweep + existing-log audit); Accepted after that FIX's /check-srd landing gate passed. |
