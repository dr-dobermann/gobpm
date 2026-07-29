# FIX-029 «CI builds the examples but never runs them — runtime regressions ship green»

**Type:** FIX (one-shot bug-fix; not rewritten after landing).
**Status:** Accepted (2026-07-29, branch `fix/engineering-odds-and-ends`, landed).
**Date:** 2026-07-29.
**Author:** Ruslan Gabitov.
**Branch:** `fix/engineering-odds-and-ends` (shared with FIX-028 — the
engineering-backlog bundle).
**Paired doc:** none (build/CI infrastructure only; no library code changes).
**Upstream:** FIX-002 §5 (the prescribing prevention item: *"CI runs examples,
not just builds them … Add a CI step (and a make target) that runs each
example with a timeout and asserts exit 0. Tracked as a follow-up"*).

**Grounded in (internal artifacts):**
- `docs/fix/FIX-002-event-start-registration-lifecycle.md:30` — *"Masked from
  CI because CI only **builds** the example modules, never **runs** them"*.
- The FIX-002 incident itself: two of three then-existing examples deadlocked
  at runtime for weeks while every CI gate was green.

## §1 Symptoms

A runtime-broken example — a deadlock, a panic, a nil-channel wait, an
`OBJECT_NOT_FOUND` from a model change — passes CI. Observed twice:

- **FIX-002 (2026-05):** examples deadlocked at runtime; invisible until run
  by hand (`FIX-002-…md:30`).
- **2026-07-26 (the guides rework):** `examples/message-send-receive` failed
  at runtime (`OBJECT_NOT_FOUND` after SRD-063 made DataObjects
  scope-resident — the example never `proc.Add`-ed its `received-order`
  object); found only because the documentation work happened to run it, and
  fixed in PR #242. CI had been green the whole time.

In code: `Makefile:294-296` — the examples gate stops at build:

```make
ci-examples:
	@$(MAKE) tidy-check-all lint-all-modules build-all MODULES="$(EXAMPLE_MODULES)"
```

`.github/workflows/check.yml:121-122` — the `examples` job runs exactly that
(`make ci-examples`, step named "Examples sweep (tidy + lint + build)").

## §2 Root Cause Analysis

### §2.1 The gate's blind spot is structural, not accidental

`ci-examples` sweeps `EXAMPLE_MODULES` (`Makefile:63` —
`$(filter ./examples/%,$(MODULES))`, currently **46 modules**) through
tidy-check, lint, and build. Nothing executes a produced program: the examples
carry no tests (by design — they are teaching artifacts), so `go build` is the
last time CI touches them. Everything after `main()` starts — engine wiring,
event delivery, data movement, clean shutdown — is unverified.

### §2.2 Why the class recurs

The examples consume the library through `replace ../..`, so **every** library
change alters their runtime behaviour with zero CI signal. Both incidents
(§1) were library-side changes that kept the examples *compiling* while
breaking them *running* — the exact failure shape a build-only gate cannot
see.

### §2.3 The interactive exception

Two examples use the console interactor (`consinp`), but only one blocks:
`examples/usertask` (`process.go`) — and it is the one example dir **without**
`go.mod` (it belongs to the root module), so `EXAMPLE_MODULES` already
excludes it structurally. `examples/expression-routing` (`tasks.go`) also
uses `consinp`, yet a headless probe shows the reader degrades gracefully on
EOF — `go run . < /dev/null` completes with exit 0 in under 20s, printing the
full success narrative. No run-gate skip is needed for it; the loop runs
every module with stdin closed so any future stdin read gets EOF, never a
hang on the runner's terminal.

## §3 Solution

### §3.1 Alternatives considered

| Alternative | Pros | Cons | Decision |
|---|---|---|---|
| A. Add tests to every example | real assertions | 40+ test files duplicating what `main` already exercises; violates the examples' teaching-artifact design; huge upkeep | ❌ rejected |
| B. Smoke-run only a hand-picked subset | fast | the subset rots — the two §1 incidents were in examples nobody would have picked; partial coverage re-creates the blind spot | ❌ rejected |
| C. `run-examples` make target: every example module runs under `timeout` with stdin closed, asserting exit 0; chained into `ci-examples` | catches the whole class; zero per-example upkeep; `go run` leaves no binary to gitignore; local↔CI parity for free (the CI job already calls `make ci-examples`) | adds minutes to the non-blocking examples job (its `timeout-minutes` must grow); a slow example needs the timeout tuned | ✅ chosen |

### §3.2 Changes by file

#### §3.2.1 `Makefile` — the `run-examples` target, chained into `ci-examples`

```make
# A future example that genuinely blocks on stdin goes here (FIX-029 §5);
# empty today — consinp degrades gracefully on EOF (§2.3), and the loop
# closes stdin so a read gets EOF, never a terminal hang.
EXAMPLE_RUN_SKIP :=
# Generous per-example ceiling: the slowest (timer-driven) examples finish
# well under it; a hang is cut at the ceiling with timeout's exit 124.
EXAMPLE_RUN_TIMEOUT := 90s

# run-examples executes every example module end-to-end (FIX-029): a runtime
# regression — deadlock, panic, model drift — fails the gate that `go build`
# alone kept green (the FIX-002 class).
run-examples:
	@set -e; for dir in $(filter-out $(EXAMPLE_RUN_SKIP),$(EXAMPLE_MODULES)); do \
		echo "::group::run $$dir"; \
		(cd $$dir && timeout $(EXAMPLE_RUN_TIMEOUT) $(GO) run . < /dev/null > /dev/null) || exit 1; \
		echo "::endgroup::"; \
	done
.PHONY: run-examples

ci-examples:
	@$(MAKE) tidy-check-all lint-all-modules build-all MODULES="$(EXAMPLE_MODULES)"
	@$(MAKE) run-examples
```

Stdout is discarded (the examples narrate) and stdin is closed; stderr stays
visible, so a failure's panic/log output lands in the CI log inside its
`::group::`. `timeout` is coreutils — present on every dev box and the
`ubuntu-latest` runner; no new tool pin.

#### §3.2.2 `.github/workflows/check.yml` — honest step name + job headroom

The `examples` job already runs `make ci-examples`, so the logic lands with
no workflow-command change; two edits keep the workflow honest:
- step name: `Examples sweep (tidy + lint + build)` →
  `Examples sweep (tidy + lint + build + run)`;
- `timeout-minutes: 15` → `30` (44 sequential `go run`s add single-digit
  minutes; the job stays non-blocking and parallel).

#### §3.2.3 `docs/fix/FIX-002-event-start-registration-lifecycle.md` — no edit (frozen)

FIX-002 is an Accepted one-shot; its §5 "tracked as a follow-up" stays as the
historical record. This doc is the follow-up it tracks.

## §4 Verification

### §4.1 Regression gate (the deliverable IS the test)

| Check | Setup | Assertion |
|---|---|---|
| `make run-examples` | the branch tree, warm build cache | exits 0; every module's `::group::run` line appears; total wall time recorded in §8 |
| negative probe | a scratch copy of one example patched to `select {}` (hang) | `run-examples` fails with `timeout`'s exit 124 — the gate actually cuts a deadlock (the FIX-002 shape); probe is discarded, not committed |
| `make ci-examples` | unchanged invocation | runs the build sweep **then** the run sweep |

### §4.5 Observability

Each example runs inside a `::group::run ./examples/<name>` fold in the CI
log; a failure's stderr (panic, error output) is the first thing visible on
unfold.

## §5 Prevention

- The gate itself is the prevention — the FIX-002 §5 prescription realized;
  the whole runtime-regression class now fails the `examples` job.
- A future example that genuinely blocks on stdin goes into
  `EXAMPLE_RUN_SKIP` (empty today; the variable's comment says so); a
  wrongly-skipped example shows up as a missing `::group::run` line in the
  job log.
- After landing, update the project memory note that tracked this follow-up
  (`project_examples_runtime_broken`).

## §6 Regressions / side-effects

### §6.1 What may rely on the old behaviour

Nothing consumes `ci-examples` besides the CI `examples` job and the local
`make ci` umbrella (grep: `Makefile` + `check.yml` only). The job is
**non-blocking** (not in branch-protection required checks), so a newly
red run-step can never block an unrelated merge — it surfaces honestly
instead.

### §6.2 Rollback path

Single-commit revert (Makefile + workflow); no state.

### §6.3 Cost

The examples job grows by the sum of example runtimes (measured in §8);
locally `make ci` grows by the same amount. Accepted: the job is parallel
and non-blocking, and the local run is the pre-push gate where this signal
belongs.

## §7 Related

- FIX-002 — the prescribing incident (§5 prevention item this realizes).
- FIX-028 (same branch) — independent scope.
- PR #242 — the second incident's fix (`message-send-receive` runtime break).

## §8 Implementation summary (stage-by-stage actual landings + deltas vs draft)

### §8.1 Stages by commit (branch `fix/engineering-odds-and-ends`)

| Stage | Commit | Scope | Tests |
|---|---|---|---|
| doc | `6ec07dd` | this document (Draft, amended pre-implementation with the §8.2-1 finding) | — |
| 1 | `635dc16` | §3.2.1–§3.2.2: `run-examples` + chain + workflow step name | full sweep 46/46 exit 0; both negative probes |

Verification: full sweep **46/46 modules, exit 0, 33s wall** (warm cache —
`build-all` fills it in the same job); a live hang (`time.Sleep`) is cut with
`timeout`'s exit 124; a full deadlock (`select {}`) fails even faster —
the Go runtime detects it (`all goroutines are asleep`, exit 2) before the
timeout matters; `make ci` green with the run step chained in.

### §8.2 Empirical findings — where reality diverged from the §3 draft

1. **The drafted skip-list was unnecessary** (caught pre-implementation, at
   the owner's prompt): `expression-routing` was assumed stdin-blocking, but
   `go run . < /dev/null` completes with exit 0 in <20s — `consinp` degrades
   gracefully on EOF. The Draft was amended before the code commit;
   `EXAMPLE_RUN_SKIP` ships empty as the extension point.
2. **The `timeout-minutes: 15 → 30` bump proved unnecessary**: the drafted
   estimate ("single-digit minutes") assumed cold compiles, but `go run`
   reuses the cache `build-all` just filled — the measured sweep is 33
   seconds. The workflow keeps `timeout-minutes: 15`; §3.2.2's bump is
   superseded by this finding.
3. **The Go runtime beats the timeout on full deadlocks**: the §4.1 negative
   probe planned to demonstrate exit 124 on `select {}`, but a total
   deadlock is runtime-detected (exit 2) — only a *live* hang (a sleeping
   goroutine, a never-firing wait) needs the timeout. Both shapes fail the
   gate.

### §8.3 Backlog (out of FIX-029 scope)

- Output assertions (expected lines per example) — a stronger gate; add only
  if silent-wrong-output regressions actually appear.
- Parallelizing the run sweep (`-P` fan-out) if wall time becomes a problem.

## §9 Open questions

None.
