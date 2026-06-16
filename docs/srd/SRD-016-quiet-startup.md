# SRD-016 — Quiet startup: suppressible banner and configuration dump

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-06-16 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-002 v.2 §4.4.1 Extension Architecture](../design/ADR-002-extension-architecture.md) |

This SRD lands [ADR-002 v.2 §4.4.1](../design/ADR-002-extension-architecture.md): two
no-argument `thresher.New` options that each suppress one block of the startup report —
`WithoutBanner()` drops the ASCII wordmark / version / last-commit block, and
`WithoutStartupConfig()` drops the per-extension configuration dump. Both default to **on**
(visible by default — opt-out, never opt-in); the closing separator prints only when a block
was emitted, so suppressing both yields a fully silent startup.

## 1. Background & motivation

### 1.1 Current state (verified against the code)

- **The startup report is unconditional.** `Thresher.New` calls `t.logStartupConfig()` after
  wiring (`pkg/thresher/thresher.go:160`), with no way to silence it. `logStartupConfig`
  (`pkg/thresher/thresher.go:168`) emits, in order: the banner lines (`strings.SplitSeq(banner,
  "\n")`), `"GoBPM — BPMN v2 process engine"`, `version:`, `last commit:`, `thresher id:`,
  `configuration:`, one `module(...)` line per extension, then `separator`.
- **ADR-002 v.1 forbade an opt-out;** v.2 §4.4.1 reverses that, splitting the report into a
  *banner* block and a *configuration dump*, each independently opt-out. This SRD implements that
  decision.
- **The option machinery already exists.** `Option = func(*thresherConfig) error`
  (`pkg/thresher/options.go:42`); `New` applies each option and wraps the first error
  (`thresher.go:123-131`). `thresherConfig` (`options.go:27`) holds the nine extensions;
  `defaultConfig()` (`options.go:201`) wires their defaults. The existing `WithXxx` options are
  value-setters that reject a nil argument (`options.go:45-177`).
- **The banner / separator constants** live in `pkg/thresher/buildinfo.go:13` (`banner`) and
  `:21` (`separator`); `readBuildInfo()` (`:36`) resolves version + last commit.
- **The current behaviour is covered** by `TestStartupConfigLog`
  (`pkg/thresher/options_test.go:124`), which captures records via a `capHandler` and asserts the
  human-readable lines are present at INFO.

### 1.2 Problem

The banner is decorative ASCII printed one INFO record per line — pure noise in a structured log
aggregator — and the configuration dump is verbose for embedders that construct many short-lived
engines or already capture the wiring elsewhere. The Logger-level lever ADR-002 v.1 pointed at
cannot drop this noise without also dropping legitimate application INFO. A per-block opt-out
removes the noise precisely while keeping the default loud.

## 2. Decision

Add two no-argument options to `pkg/thresher`:

- `WithoutBanner()` — suppress the **banner block**: the ASCII wordmark, the
  `"GoBPM — BPMN v2 process engine"` tagline, `version:`, and `last commit:`.
- `WithoutStartupConfig()` — suppress the **configuration block**: `thresher id:`, the
  `configuration:` header, and the one-per-extension `module(...)` lines.

Each option sets an unexported `bool` on `thresherConfig`. `logStartupConfig` consults the two
flags and prints each block only when its flag is unset. The `separator` closes the report iff at
least one block printed; with both suppressed, `logStartupConfig` emits nothing.

The two flags are engine-construction config, **not** part of `renv.EngineRuntime` — they are not
exposed through the runtime interface and do not touch any executor or waiter.

## 3. Functional requirements

| # | Requirement | Acceptance |
|---|---|---|
| FR-1 | `WithoutBanner() Option` exists; applied, it suppresses the banner block (wordmark, tagline, `version:`, `last commit:`). | With the option, captured records contain none of those lines; the configuration block and separator are still present. |
| FR-2 | `WithoutStartupConfig() Option` exists; applied, it suppresses the configuration block (`thresher id:`, `configuration:`, per-extension lines). | With the option, captured records contain none of those lines; the banner block and separator are still present. |
| FR-3 | The `separator` prints iff at least one block was emitted. | Default → separator present. Both options → **zero** records emitted (no separator). Either one → separator present. |
| FR-4 | With neither option, startup output is byte-for-byte the pre-change behaviour. | `TestStartupConfigLog` (unchanged assertions) stays green. |
| FR-5 | The options honour the `Option` contract and compose with the others; order-independent; idempotent. | Each returns a non-nil `Option`; applying twice equals applying once; they do not error (no argument to validate). |

## 4. Non-functional requirements

| # | Requirement |
|---|---|
| NFR-1 | Visible-by-default preserved: both blocks emit unless explicitly suppressed (opt-out only). |
| NFR-2 | No change to `renv.EngineRuntime`; the flags are unexported `thresherConfig` fields. `TestConfigSatisfiesEngineRuntime` stays green. |
| NFR-3 | Diff-coverage ≥ `COVER_MIN` (95) on every touched function, aiming 100%. |

## 5. Path analysis (alternatives)

- **(A) Two no-argument options — chosen.** `WithoutBanner()` / `WithoutStartupConfig()`. Matches
  the requested granularity (suppress each block independently) and the existing `WithXxx` family
  shape. No argument means nothing to validate — the public-API validation rule is satisfied
  vacuously.
- **(B) One `WithQuietStartup()` that suppresses everything.** Rejected: less granular; cannot
  drop only the decorative banner while keeping the wiring dump (a common want). The requirement
  is explicitly *two* flags.
- **(C) A configurable `WithStartupLog(level/mask)` enum or variadic.** Rejected: over-engineered
  for two booleans; invents a vocabulary the engine has no other use for.
- **(D) Status quo — silence via the user's Logger level (ADR-002 v.1).** Rejected: this was v.1's
  decision and is the very problem — it cannot separate banner noise from application INFO.

## 6. API

```go
// WithoutBanner suppresses the startup banner block (the ASCII wordmark, the
// product tagline, the version and the last-commit line). The configuration
// dump still prints unless WithoutStartupConfig is also given.
func WithoutBanner() Option

// WithoutStartupConfig suppresses the startup configuration dump (the thresher
// id, the "configuration:" header and the per-extension lines). The banner
// still prints unless WithoutBanner is also given.
func WithoutStartupConfig() Option
```

```go
// A fully silent startup:
eng, _ := thresher.New("worker-7",
    thresher.WithoutBanner(),
    thresher.WithoutStartupConfig(),
)

// Keep the build identity, drop the wiring dump:
eng, _ := thresher.New("worker-7", thresher.WithoutStartupConfig())
```

## 7. Test plan

Captured via the existing `capHandler` (`options_test.go:113`).

| # | Test | Asserts |
|---|---|---|
| T-1 | `TestStartupConfigLog` (existing, unchanged) | Default: banner + config + separator all present at INFO (FR-4). |
| T-2 | `TestWithoutBanner` | `WithoutBanner()`: no wordmark/`version:`/`last commit:` lines; `configuration:` + per-extension lines + separator present (FR-1, FR-3). |
| T-3 | `TestWithoutStartupConfig` | `WithoutStartupConfig()`: no `thresher id:`/`configuration:`/per-extension lines; banner + separator present (FR-2, FR-3). |
| T-4 | `TestQuietStartup` | Both options: **zero** records captured (FR-3). |
| T-5 | `TestWithoutBannerIdempotent` | Applying `WithoutBanner()` twice == once (FR-5). |

## 8. Cross-document consistency

- Implements [ADR-002 v.2 §4.4.1](../design/ADR-002-extension-architecture.md) (upward reference,
  version-pinned). No other doc references the startup-report shape.
- `README.md` Quick-start area gains a short note on the two options (a reference-doc-style
  update, landed in this change-set).

## 9. Definition of Done

- `WithoutBanner()` / `WithoutStartupConfig()` implemented; `logStartupConfig` branches on the two
  flags; separator gated on "a block printed".
- T-1…T-5 green; touched functions at ≥95% diff-coverage (aim 100%).
- `README.md` documents the two options.
- `make ci` green end to end (tidy → lint → build → race tests → diff-coverage → govulncheck).
- `/check-srd` PASS; §10 implementation summary filled; status flipped to Accepted.
- RU twin added.

## 10. Implementation summary

*Post-landing placeholder — filled at the final audit with files, V-results, and milestone SHAs.*

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-06-16 | Ruslan Gabitov | Draft. Two no-argument `thresher.New` options — `WithoutBanner()` / `WithoutStartupConfig()` — landing the per-block startup-report opt-out decided in ADR-002 v.2 §4.4.1. Separator gated on "a block printed"; both suppressed ⇒ silent startup. |
