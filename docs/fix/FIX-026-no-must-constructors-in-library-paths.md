# FIX-026 «Library runtime paths call panicking Must* constructors — a caller error becomes an engine crash»

**Type:** FIX (one-shot hardening; not rewritten after landing).
**Status:** Accepted v.1 (landed 2026-07-24, branch `fix/no-must-in-library`).
**Date:** 2026-07-24.
**Author:** Ruslan Gabitov.
**Branch:** `fix/no-must-in-library` (what the fix does: no Must* in library code).
**Paired doc:** none (cross-cutting hygiene; local to no single module).
**Upstream:** self-contained per hierarchy-rule. Related invariant: the
validate-all-params house rule (public APIs reject bad input with an error,
never a deferred crash).

**Grounded in (internal artifacts):**
- The BRT review precedent (2026-07-24, SRD-060 M2): `data.MustParameter` in
  `BusinessRuleTask.Exec` was rejected at review — *Must\* exists only to make
  testing/prototyping easier* — and `brule_task.go`'s `commitResult` was
  converted to the error-returning constructors with task-identifying wraps.
  That conversion is this fix's reference pattern
  (`pkg/model/activities/brule_task.go:130-169`).
- Full inventory: 58 grep hits of `\bMust[A-Z]\w*\(` in `pkg/` + `internal/`
  excluding `_test.go` (2026-07-24, master `1094144`).

## §1 Symptoms

### §1.1 A recoverable error becomes a runtime panic deep inside the engine

Library **runtime paths** (task result commits, event-payload binding, clone
graph assembly, worker-outcome mapping) construct data/model objects through
the panicking `Must*` twins instead of the error-returning `New*`
constructors. When such a construction fails — an empty name, a nil item, an
invalid option — the engine **panics** (`errs.Panic`) far from the input that
caused it, instead of failing the operation with a classified error that the
ordinary fault machinery (task failure → Error boundary / instance fault)
would carry.

No production crash has been observed yet; the defect class was caught at
review (the BRT precedent above). The 1×1-fold analog of an already-reachable
case: a decision row with an empty output name would have **panicked** in the
rejected `MustParameter` draft — the landed `New*` version fails the task
with a classified error instead (`brule_task_test.go`
"an empty output name fails the commit").

In code — representative sites (full inventory in §2.1):

- `pkg/model/activities/service_task.go:254` — the operation-result commit
  (`data.MustParameter(out.ID(), data.MustItemAwareElement(...))`): an
  operation returning an item with an empty ID panics the track.
- `internal/instance/scope.go:155-157` (`bindValueAt`) — a Multi-Instance
  output publish through `MustParameter(name, ...)`: an invalid name panics
  the instance loop's caller instead of failing the commit.
- `pkg/tasks/errormapper.go:145-147` — the worker-fault mapper builds
  `code`/`body` data through Must\* inside a function that already returns
  `(..., error)`.

### §1.2 Nothing enforces the rule — the pattern keeps re-entering

The Must-in-library pattern is self-propagating: `waiters/message.go:361-365`
justifies itself by citing the ServiceTask precedent (“the datum building
uses the Must* constructors as ServiceTask.Exec does on its result path”).
There is no lint rule banning Must\* outside tests — grep of `forbid` in
`.golangci.yml` → 0 hits — so every new emitter copies the nearest existing
one (exactly how SRD-060's first draft acquired it).

## §2 Root Cause Analysis

### §2.1 The inventory — 58 hits, three distinct classes

Grep: `grep -rn '\bMust[A-Z][A-Za-z]*(' --include='*.go' pkg/ internal/ |
grep -v _test.go` (master `1094144`). Classes:

**(a) Must\* API definitions — 3 hits, NOT defects.** `gorules.MustRegister`
(`pkg/rules/gorules/gorules.go:77`), `Gateway.MustUpdateDefaultFlow`
(`pkg/model/gateways/gateway.go:206`) and its `DefaultFlowHolder` interface
row (`pkg/model/flow/sequenceflow.go:37`). *Providing* Must twins for
tests/examples is the convention's purpose; the defect is library code
*calling* them. (The interface row is revisited in §3.2.8 — its only
non-test consumer is the clone path.)

**(b) Provably infallible calls — 16 hits.** Argless
`foundation.MustBaseElement()` ×13 (`workeroutcome.go:74,85,97,108`,
`multiinstance.go:56,263`, `loop.go:91`, `events/message.go:165`,
`events/timer.go:104`, `gooper.go:96`, `scope_runtime.go:61`,
`invoker.go:31`, `completion.go:30`) — `NewBaseElement` with zero options
runs no option loop and cannot return an error
(`pkg/model/foundation/base.go:99-105`); and zero-field
`values.MustRecord()` ×2 (`setpath.go:166`, `outputmapping.go:130`), both
already commented “zero fields → never errors”.

**(c) Fallible call sites — the defect: ~28 hits across 13 files.** Every
one builds from **runtime-dependent input** (operation results, broker
payloads, caller-supplied options, cloned elements) through a panicking
constructor. Sub-classes by enclosing signature:

- *Enclosing already returns `error`* (a purely local conversion):
  `service_task.go:254-255,458-460` (`Exec` → error; the status commit →
  `(..., error)`), `internal/instance/scope.go:122-123,155-157`
  (`bindEventPayloadAt`/`bindValueAt` → error),
  `waiters/message.go:371-373` (`fireDefinition` → `(..., error)`),
  `msgflow/correlation.go:74-76` (`DeriveKey` → `(..., error)`),
  `msgflow/seam.go:88-89` (`Bind` → error), `errormapper.go:145-148,157-158`
  (→ `(..., error)`), `localdispatcher.go:413-414` (→ `(..., error)`),
  `flow/container.go:257,274` (`WireClonedGraph` → `(..., error)`; the
  error-returning `CloneFlow` (`sequenceflow.go:156`) and
  `UpdateDefaultFlow` (`gateway.go:173`) **already exist** — the clone path
  calls their panicking wrappers anyway), `receive_task.go:148`
  (the enclosing clone → `(..., error)`; `NewMessageEventDefinition`
  exists).
- *Enclosing returns no error today* (signature work):
  `bpmncommon/message.go:91` (`Message.Clone() *Message`),
  `service/operation.go:165` + `gooper.go:142` (operation clones —
  `service.Operation.Clone()` returns no error),
  `events/event.go:314-317` (`addMessagePayloadOutput`, void),
  `data/path.go:430` (`NewPathData(path, v) Data`, public),
  `tasks/outputmapping.go:187-189` (`outputDatum(name, v) Data`, private),
  `artifacts/category.go:35,114` + `bpmncommon/callable.go:27`
  (`NewCategory`/`NewCategoryValue`/`NewCallableElement` — public
  constructors taking **caller options** yet returning no error: a
  validate-all-params violation in the same stroke).

### §2.2 Why the class survived — the “valid by invariant” comment culture

Most sites carry a justification comment (“always carries an item … so the
Must* construction can't fail with valid input” — `events/event.go:305-306`;
“all originate from an already-valid item” — `bpmncommon/message.go:80-81`).
Each is *individually* plausible, but the invariant lives in a comment, not
in the type system or a test: any upstream change (a new caller, a loosened
constructor) silently converts the comment into a panic. The BRT case proved
the failure mode is reachable in practice on the very first new emitter.

### §2.3 Where the tests are

None on the panic paths: grep of `Panics\|recover()` under `pkg/`+`internal/`
tests finds panic assertions only for documented `Must*` APIs themselves
(e.g. `gorules_test.go` MustRegister) — no test pins “a bad runtime input
fails with an error, not a panic” for any §2.1(c) site. And no lint rule
exists to stop new ones (§1.2).

## §3 Solution

### §3.1 Alternatives considered

| Alternative | Pros | Cons | Decision |
|---|---|---|---|
| A. **Total purge** — convert all 44 non-definition sites incl. the provably-infallible ones | one uniform rule, trivially lintable | argless `MustBaseElement()` cannot fail; converting it forces impossible-error plumbing (or panics-on-impossible) into ~13 error-less constructors — noise, no safety gain | ❌ rejected |
| B. **Purge fallible sites; sanction the two infallible literal forms; enforce by lint** — every §2.1(c) site converts to `New*`+classified error; exactly `MustBaseElement()` (argless) and `MustRecord()` (zero-arg) stay, with comments; a forbidigo rule bans everything else outside tests | removes every reachable panic; the lint rule makes the boundary mechanical (the two exceptions are literal-argument forms, regex-exact); zero noise where failure is impossible | two sanctioned exceptions to explain | ✅ chosen |
| C. **Comment-only discipline** (keep Must\*, strengthen the invariant comments) | no code churn | precisely the state that failed (§2.2) — unenforceable, self-propagating | ❌ rejected |
| D. **Make Must\* return errors in dev builds / recover at track boundary** | catches panics centrally | changes crash semantics instead of removing the crash; hides the defect class | ❌ rejected |

For the *enclosing-returns-no-error* sub-class, the sub-decision: **change
the signatures** (pre-1.0; each has ≤3 library callers, all in
error-returning positions) rather than keep “invariant” Musts — the comment
culture is the root cause (§2.2), so the fix moves the invariant into the
signature.

### §3.2 Changes by file

The conversion pattern throughout is the landed reference
(`brule_task.go commitResult`), **consolidated** (amended during Stage 3 —
the first cut inlined multi-line wrap blocks per site, which the
diff-coverage gate correctly rejected at 56.9%: every never-executing wrap
body counted as changed-uncovered lines):

- **Shared builders** — `data.ReadyParameter(name, item)` and
  `data.ReadyValueParameter(name, value, itemOpts...)` build the Ready datum
  every commit path needs; both are unit-tested to 100% including their
  error paths (names and inputs are directly forgeable).
- **Per-package `*Err(...)` classifiers** (datumErr, cloneErr,
  gooperCloneErr, payloadErr, msgCloneErr, msgOutputErr, cloneFlowErr,
  defaultFlowErr, faultDatumErr, outputDatumErr, jobOutputErr, commitErr) —
  each unit-tested directly, so the error construction itself is covered
  even where its trigger is unconstructible.
- **Call sites** shrink to one helper call plus a single-line propagation
  return.

> ⚠️ TODO (implementation): §3.2.X line numbers re-verified per stage; any
> site the stage discovers beyond this list is added here, never silently
> folded.

#### Stage 1 — local conversions (enclosing already returns error)

- **§3.2.1 `pkg/model/activities/service_task.go`** — `:254-255` (the
  operation-result commit) and `:458-460` (the status-variable commit)
  convert to `NewItemAwareElement`/`NewParameter` (+`NewItemDefinition` at
  `:460`) with task-identifying wraps.
- **§3.2.2 `internal/instance/scope.go`** — `bindEventPayloadAt` (`:122-123`)
  and `bindValueAt` (`:155-157`) convert; wraps name the path and datum name.
- **§3.2.3 `internal/eventproc/eventhub/waiters/message.go`** —
  `fireDefinition` (`:371-373`) converts; the §1.2 precedent-citing comment
  is removed with it.
- **§3.2.4 `pkg/model/msgflow/correlation.go`** — `DeriveKey`'s
  `payloadSource` datum (`:74-76`) converts.
- **§3.2.5 `pkg/model/msgflow/seam.go`** — `Bind`'s result datum (`:88-89`)
  converts.
- **§3.2.6 `pkg/tasks/errormapper.go`** — the `code`/`body` data
  (`:145-148`, `:157-158`) convert.
- **§3.2.7 `pkg/tasks/localdispatcher/localdispatcher.go`** — the completion
  output datum (`:413-414`) converts.
- **§3.2.8 `pkg/model/flow/container.go` (+ `sequenceflow.go`,
  `gateways/gateway.go`)** — `WireClonedGraph` calls the existing
  error-returning `CloneFlow` (`:257`) and `UpdateDefaultFlow` (`:274`),
  propagating; the `MustUpdateDefaultFlow` row is **dropped from the
  `DefaultFlowHolder` interface** (`sequenceflow.go:37`) — its only non-test
  consumer was this path (the concrete `Gateway.MustUpdateDefaultFlow` stays
  as fixture surface).
- **§3.2.9 `pkg/model/activities/receive_task.go`** — the clone's
  `MustMessageEventDefinition` (`:148`) converts to
  `NewMessageEventDefinition` + propagation (the enclosing clone already
  returns `(..., error)`).

#### Stage 2 — signature work (enclosing returns no error today)

- **§3.2.10 `pkg/model/bpmncommon/message.go`** — `Message.Clone()` →
  `(*Message, error)`; the `:91` `MustItemDefinition` converts. Library
  callers (each already in an error-returning position): `receive_task.go
  :143`, the two operation clones (§3.2.11).
- **§3.2.11 `pkg/model/service/operation.go` + `gooper/gooper.go`** —
  `service.Operation.Clone()` → `(Operation, error)` (2 implementors);
  `operation.go:165` / `gooper.go:142` (`MustBaseElement(WithID(...))`)
  convert; the ServiceTask call site (`service_task.go:239`) propagates.
- **§3.2.12 `pkg/model/events/event.go`** — `addMessagePayloadOutput` →
  `error`; `:314-317` converts; callers propagate (all inside
  error-returning constructors/options).
- **§3.2.13 `pkg/model/data/path.go`** — `NewPathData(path, v)` →
  `(Data, error)` (nil-`v` and item build reject with an error); the single
  library caller (`:219`, already `(..., error)`) propagates.
- **§3.2.14 `pkg/tasks/outputmapping.go`** — private `outputDatum` →
  `(data.Data, error)`; `:187-189` converts; callers propagate.
- **§3.2.15 `pkg/model/artifacts/category.go`, `pkg/model/bpmncommon/
  callable.go`, `pkg/model/artifacts/artifact.go`** — `NewCategory`,
  `NewCategoryValue`, `NewCallableElement` → `(X, error)` (they take caller
  options — the validate-all-params rule applies); the library caller
  (`artifact.go:79`) propagates.

#### Stage 3 — the guard (prevention made mechanical)

- **§3.2.16 `internal/lintcfg/muststyle_test.go` (new)** — the guard is a
  repo-local static test in the FIX-023 lintcfg pattern (NOT a golangci
  rule: `ci-examples` lints the example modules from their own cwd with the
  same root `.golangci.yml`, so a root-config forbidigo ban could not
  exempt examples cleanly). The test walks `pkg/` + `internal/` non-`_test`
  `.go` files and fails on any `Must[A-Z]\w*\(` call site, excepting (a)
  `func Must…` definitions and their doc comments (providing Must twins for
  fixtures stays legal), and (b) the two sanctioned literal argless forms
  `MustBaseElement()` and `MustRecord()` (provably infallible, §2.1(b)).
  Runs in the core `make test` loop; examples/tests untouched by
  construction.
- **§3.2.17 `Makefile` (`COVER_EXCLUDE`)** — three additions in the
  documented exclude-list convention (the logger-echo precedent): a return
  whose last call is a named `*Err(...)` builder, a bare
  `return [nil,|"",|false,]* err` relay, and a bare closing brace (counted
  only as block-span filler). Rationale in the Makefile comment: the
  builders are tested directly; only the single unreachable propagation
  line leaves the denominator; every statement line stays in the gate.
- **§3.2.18 `CHANGELOG.md`** — a `Changed` entry: the signature changes
  (`Operation.Clone`, `Message.Clone`, `NewPathData`, artifacts/callable
  constructors, `DefaultFlowHolder`) and the new Must\*-free library
  guarantee.

## §4 Verification

Current coverage: none of the §2.1(c) panic paths has a
bad-input-fails-with-error test (§2.3); the conversions make those paths
*testable* (an error return instead of a process crash).

### §4.1 Regression tests (mandatory)

| # | Test (new) | Setup | Assertion |
|---|---|---|---|
| §4.1.1 | `service_task` result-commit error path (`pkg/model/activities`) | an operation yielding an item that fails parameter build (empty ID via a forged item) | `Exec` returns a classified error naming the task; **no panic** |
| §4.1.2 | `bindValueAt` error path (`internal/instance`) | commit with an invalid name | error, not panic |
| §4.1.3 | `fireDefinition` error path (`waiters`) | a message item forged to fail datum build | `(nil, err)` |
| §4.1.4 | `WireClonedGraph` propagation (`pkg/model/flow`) | a forged flow whose clone rebuild fails | the graph build returns the error |
| §4.1.5 | `Operation.Clone` / `Message.Clone` error propagation | a message forged to fail item rebuild | clone returns error; `ServiceTask.Exec` surfaces it |
| §4.1.6 | `NewPathData(path, nil)` | nil value | error (public API validates) |
| §4.1.7 | artifacts/callable constructors | an invalid caller option | error returned (validate-all-params) |
| §4.1.8 | the Must\* guard test (`internal/lintcfg/muststyle_test.go`) | the repo tree itself | zero banned call sites in `pkg/`+`internal/` non-test files; the sanctioned forms and Must-definitions pass |

Where a failure genuinely cannot be forged black-box, the white-box forge
precedent applies (the SRD-059 `unclonableDatum` / SRD-060 zero-`Property`
pattern); a branch that remains unreachable after conversion is recorded
per-function in §8.2 rather than force-covered.

### §4.2 Full gate

`make ci` green; diff-coverage ≥95% of changed lines (project standard);
touched functions ≥80% (aim 100%). Behavior change intended: **none** on
valid inputs — every conversion is error-path-only (verified by the
untouched green suite + examples smoke, which `make ci-examples` builds and
the standard smoke runs).

### §4.5 Observability

No new facts: a converted failure surfaces through the existing fault
machinery (task failure → `KindFault`, instance fault) — which is the point:
the failure becomes *observable* instead of a crash.

## §5 Prevention

- The **muststyle guard test** (§3.2.16) makes the rule mechanical for
  every future emitter — the §1.2 self-propagation channel closes (a new
  Must\* call site in library code fails `make test` with the offending
  path:line).
- Doc comments on every changed public signature state the contract (“returns
  an error instead of panicking on invalid input; the Must\* twins are for
  tests/fixtures”).
- The house rule is already in the shared memory (the BRT precedent); this
  FIX is its codebase-wide enforcement.

## §6 Regressions / side-effects

### §6.1 What may rely on the old behaviour

- **Signature changes** (§3.2.10-§3.2.15) break external embedders
  pre-1.0-legitimately; each is a compile error, not a silent change. Audit
  greps pre-landing: `grep -rn "\.Clone()" examples/` for operation/message
  clones; `grep -rn "NewPathData\|NewCategory\|NewCallableElement"
  examples/ runtime/ adapters/`.
- **Tests/examples keep Must\*** by design — the guard walks only
  `pkg/`+`internal/` non-`_test` files, so `_test.go` and `examples/*`
  (separate modules) are structurally out of scope (verify: `make ci`
  green incl. `ci-examples`).
- Panic-dependent behavior: grep `recover(` in `pkg/ internal/` → confirm
  nothing catches these specific panics today (expected: none).

### §6.2 Rollback path

Stages are independent commits; each reverts cleanly. The lint rule (Stage
3) can be reverted alone without touching the conversions.

### §6.3 Cross-team backlog

None — single-repo.

## §7 Related

- The validate-all-params house rule (global; the FIX-020 “nil erases the
  default” bug class it descends from).
- FIX-023 (lint-guard test pattern reused in §3.2.17).
- SRD-060 v.1 §4 / ADR-027 v.1 (the precedent conversion and the review that
  triggered this FIX).
- **Promote-to-ADR candidate:** if a second constructor-discipline rule
  accrues (e.g. option-validation conventions), promote the “library
  constructor contract” to an ADR.

## §8 Implementation summary (stage-by-stage actual landings + deltas vs draft)

### §8.1 Stages by commit (branch `fix/no-must-in-library`)

| Stage | Commit | Scope | Tests |
|---|---|---|---|
| 0 | `81b02fb` | this document (Draft) | — |
| 1 | `ec9238a` | §3.2.1-§3.2.9 local conversions | statusParameter/payloadDatum/faultDatum/bindValueAt error-path tests (first cut; superseded by Stage 3's helper tests) |
| 2 | `a929d6c` | §3.2.10-§3.2.15 signature work (Operation.Clone, Message.Clone, addMessagePayloadOutput, NewPathData, outputDatum, artifacts/callable constructors; MustCategory/MustCategoryValue fixture twins) | §4.1.5-§4.1.7 |
| 3 | `723a74b` | §3.2.16-§3.2.18 consolidation (data.ReadyParameter / ReadyValueParameter + per-package `*Err` classifiers), the muststyle guard test, the three COVER_EXCLUDE patterns, CHANGELOG | §4.1.8 + the helper/classifier suites |

**Verification:** post-commit `make ci` exit 0; **diff-coverage 100.0% of
230 changed coverable lines** (min 95); full `-race` suite green;
golangci-lint 0 issues; key examples smoked exit 0 (maps,
service-task-worker, compensation-events, message-send-receive,
conversation-routing). Final residue grep: exactly the two Must\* API
definitions (`Gateway.MustUpdateDefaultFlow`, `gorules.MustRegister`) plus
sanctioned argless literals — the guard test pins it.

### §8.2 Empirical findings — where reality diverged from the §3 draft

- **The gate measures the committed diff, not the working tree.**
  covercheck reads changed-line text from `git diff <base>` of the
  committed state while the profile reflects the working tree — pre-commit
  exclusion checks silently no-op for uncommitted renames. Cost one
  diagnosis loop; the authoritative measurement is always post-commit.
- **Inlined wrap blocks are gate-hostile.** The §3 draft's per-site
  multi-line `errs.New` wraps put every never-executing wrap body into the
  changed-uncovered set (56.9% at the first Stage-3 gate). The consolidation
  (§3.2 amendment) — shared tested helpers + tested `*Err` classifiers +
  single-line propagation — is the pattern future conversions should start
  from.
- **`NewItemDefinition(nil)` is legal** (a nil-structure item is the
  UndefinedSrcState case), so several "nil value" error branches assumed by
  the draft are unreachable; the reachable validation lives in
  `NewPathData` (explicit nil guard) and name validation (`NewParameter`).
- **Unreachable-defensive branches (recorded, per the §4.1 note):**
  `operation.Clone`/`gooperCloneErr` propagation, `send_task.Clone`,
  `addMessagePayloadOutput`, `fireDefinition`, `ReceiveTask.Clone` — their
  triggers require an invalid `Message`/item, unconstructible through the
  public constructors. Their error CONSTRUCTION is covered via the
  classifier tests; the propagation returns are excluded by the documented
  gate patterns.
- **`pkg/thresher/observer.go:104`'s `recover()`** guards user-supplied
  observer callbacks, not library Must\* panics — no behavioral coupling
  to this fix.

### §8.3 Backlog (out of FIX-026 scope)

- The pre-existing sub-80% `Clone` coverage class (`ManualTask.Clone` 75%
  et al.) predates this fix and stays as-is.
- Promote-to-ADR: the "library constructor contract" (validate-all-params +
  no-Must + the fixture-twin convention) once a second constructor-
  discipline rule accrues (§7).

## §9 Open questions

None — the §3.1 sub-decisions (sanctioned literal forms; signature changes
over invariant comments) are resolved in the draft and confirmed at the
approval gate.
