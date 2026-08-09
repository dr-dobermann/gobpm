# FIX-039 — the gate's progress is invisible and its verdict is destructible

**Type:** FIX (one-shot bug-fix; not rewritten after landing).
**Status:** Draft.
**Date:** 2026-08-08.
**Author:** Руслан Габитов.
**Branch:** `fix/ci-progress-and-verdict`.
**Upstream:** none — this is build tooling, not engine behaviour. The rules it
serves are the parity rules in `CLAUDE.md` ("if `make ci` is green locally, CI
is green") and the `/check-srd` rule "verify the gate by its own completion
markers, not a wrapper exit code".

**Grounded in (verified at `4d8d3be`):**
- `Makefile:503` — `ci-core: mock-check link-check examples-module-check tidy-check-core lint-core build-core consumer-smoke test-core cover-check vuln-core` (ten steps).
- `Makefile:509` — `ci: ci-core ci-examples`.
- `Makefile:334,350,359,367,379,464` — the only progress output the gate emits is `::group::<verb> <dir>`, one per module per step.
- `Makefile:348` — `test-all` loops modules serially; `go test` prints nothing until a package completes.
- `.github/workflows/check.yml:114` — "the verdict is identical local and on CI".

---

## 1 Symptoms

### 1.1 The verdict exists only in the caller, so killing the caller destroys it

`make ci` communicates pass/fail solely through its exit code. Nothing durable
is written. Every observer therefore invents its own capture, and each invention
has failed in a different way:

- **A trailing `echo` masks the status.** `make ci > log 2>&1; echo "X=$?"` is
  the obvious idiom, and the shell reports *echo's* exit code to whatever is
  watching the compound command. `/check-srd` already documents this trap, which
  is evidence that it is easy to fall into rather than evidence that it is
  avoided.
- **Killing the wrapper kills the gate — or doesn't, and nobody can tell
  which.** On 2026-08-08 a harness killed a backgrounded wrapper; the process
  group died mid-`test-core`, at step 8 of 10, after roughly 90 seconds of work.
  The log simply stopped. There was no record that a run had started, no record
  that it had died, and no way to distinguish "died" from "still working" except
  by inspecting processes.
- **A filtering wrapper can fabricate a pass.** The same day, a run whose
  `cover-check` step failed at 91.7% was reported as exit 0, because an
  output-summarising layer between the gate and the observer truncated the log
  (it wrote a literal `... (563 lines truncated)` line into the file) and
  returned its own status.

The common shape: **the gate knows the answer and the answer does not survive
the trip to whoever asked.** A verdict that a spectator can destroy or forge is
not a verdict.

### 1.2 Progress is invisible, so silence is unreadable

The gate's only progress output is `::group::<verb> <dir>` (`Makefile:334` and
five siblings). Nothing states which of the ten steps is running, how many
remain, how long this step has taken, or how long it usually takes.

`test-core` is the pathological case: it echoes `::group::test .` and then says
nothing at all until the module's whole race suite finishes, because `go test`
buffers per package. An observer sees one line and then a silence of arbitrary
length. That silence is identical whether the suite is working, deadlocked, or
already dead — all three were observed on 2026-08-08, and the dead one was
mistaken for the working one for eighteen minutes.

The cost is not aesthetic. Silence with no expected duration attached makes
every long step a decision: keep waiting, or investigate. Made wrong in one
direction it wastes the session; made wrong in the other it kills a healthy run.

### 1.3 Nothing records how long anything takes

There is no history, so "is this normal?" is unanswerable — by a human or by an
agent. The absence has a measurable cost: during the same session the full gate
was estimated at "10–20 minutes for the race step alone" from watching a killed
process, when the true figure for the entire gate on that machine is **146
seconds** (`exit=0 seconds=146 head=f898eaa`). Two orders of magnitude, and
nothing in the repository could have corrected it.

### 1.4 Reconstructing progress from the outside is unreliable — and looks reliable

Because the gate publishes no progress, every observer parses its log, and a
parser is a second thing that can be wrong while sounding confident. Both
failures below happened within one hour, and both produced plausible output:

- A liveness check of `pgrep -f 'make ci'` matched **its own command line**,
  which contains that string, and so reported "alive" for a process that had
  been dead for eighteen minutes.
- A step classifier keyed on `::group::lint .` also matched
  `::group::lint ./examples/...`, so the examples sweep was reported as core
  step 5 — after step 8 had already been reported. Progress ran backwards.

Neither bug was in the gate. Both were in the act of guessing what the gate was
doing, which is only necessary because the gate does not say.

### 1.5 The runner itself can lose a verdict — three ways found while building this

Implementing the fix surfaced three more instances of the same shape, each
found by running the thing rather than by reasoning about it:

- **Two driver invocations, two verdicts.** `ci` first called `ci-core` and
  `ci-examples` in turn, so the status file was written twice and the second
  overwrote the first — leaving a file that described half the run while looking
  like it described all of it.
- **Editing the script mid-run corrupted the running interpreter.** Bash reads a
  script incrementally as it executes, so an edit shifts byte offsets underneath
  it. A 14/14 green run died at its last line with `syntax error near unexpected
  token 'done'` and wrote no verdict. The run was correct; the file describing it
  was destroyed by a concurrent editor.
- **A trap cannot fire during a long step.** Bash defers a trap until the
  current FOREGROUND command completes, so a SIGTERM arriving during a
  multi-minute `make test-core` sat pending for the rest of that step — the
  handler would have run long after the sender concluded nothing had happened.
  Measured: the first T-7 run left `go test -race` running and wrote nothing.
- **A DRY RUN forged a pass.** `make -n ci` wrote
  `verdict: PASS, 14 steps, 1s` for a run that executed nothing. GNU make runs
  any recipe line containing the literal `$(MAKE)` even under `-n` — so the
  driver started — and passes the dry-run flag down in `MAKEFLAGS`, so every
  step returned instantly and successfully. This is the worst defect found in
  this work: the machinery built to make a verdict unforgeable forged one, and
  it did so in the most innocuous way imaginable, from a command whose entire
  purpose is not to do anything.

### 1.6 What the independent review found

`/pr-review` read the diff doc-blind (two lenses, a different model family) and
returned nine notes; six survived verification and are fixed here. Three are
worth naming because each is the document's own thesis turned on the fix:

- **The driver crashed when run directly.** `${MAKEFLAGS%% *}` under `set -u` is
  fatal when the variable is unset — which is exactly how the script's own usage
  line says to invoke it. Reproduced: `MAKEFLAGS: unbound variable`.
- **Hiding `$(MAKE)` broke the jobserver.** The `MAKE_BIN` trick that stopped
  `-n` from executing the driver also stopped make from passing its jobserver,
  so `make -j4` warned `jobserver unavailable: using -j1` on every step.
  Measured. The guard alone is sufficient and keeps both properties.
- **The verdict was written non-atomically.** Multiple `printf`s into one
  redirect, while the file's whole purpose is to be read by someone else — the
  session's own monitor polled it. A reader could see half a JSON document.

Two notes were refuted and recorded in §8.3 rather than dropped.

## 2 Root cause analysis

One cause with two faces: **the gate reports to its caller and to nobody else.**
Pass/fail goes out through an exit code, which only the immediate parent can
read and which the parent must then relay; progress goes out as unstructured
log text, which anyone downstream must re-derive.

Both make the *observer* responsible for information only the *gate* possesses.
Every failure in §1 is an observer getting that responsibility wrong — a masked
exit code, a killed process group, a truncating filter, a self-matching pgrep, a
too-loose glob. The gate was correct in every one of those runs.

The fix is therefore not "watch it better". It is to have the gate state its own
progress and record its own verdict, so that no downstream party has to
reconstruct either.

## 3 Solution

### 3.1 Alternatives considered

| # | Option | Verdict |
|---|---|---|
| 1 | Leave it; observers must be careful | Rejected — five distinct observer bugs in one session, each careful-looking. The failure rate is the argument. |
| 2 | Parse the log better (a shared parser script) | Rejected — this is §1.4 with more code. A parser is a second source of truth, and the log format is not a contract. |
| 3 | Emit numbered step banners + write a status file, in the gate | **Chosen.** The step that runs is the only party that knows which step it is; the process that ends is the only party that knows how it ended. |
| 4 | Also parallelise the module sweeps to shorten the wait | Rejected — explicitly out of scope. The gate takes 146s; the complaint is unpredictability, not duration. Making a timing-sensitive race suite faster buys flakes, and a flake is the purest form of unpredictability. |
| 5 | Branch the output on `$CI` | Rejected — no conditional is needed, and adding one would be the divergence the parity rule forbids. GitHub invokes each core step as its OWN workflow step (`check.yml:80-124`: `run: make mock-check`, `run: make test-core`, …), so it never runs the driver and already names and times every step in its UI. The driver announces what it drives; CI simply drives the steps itself. The `examples` job DOES call `make ci-examples` (`check.yml:155`) and so gets banners there. |
| 6 | Commit the timing baseline | Rejected — timings are machine-specific (146s on 24 cores; a 2-core runner is far slower), so a shared baseline is wrong for nearly every machine, and a wrong estimate is worse than none. It also rewrites a tracked file on every run, leaving a dirty tree — which this repo can least afford, since `cover-check` is HEAD-based. Ignored, beside `coverage.*`. |

### 3.2 Changes by file

#### 3.2.1 `Makefile` — a step announces itself

Each of the ten `ci-core` steps and the four `ci-examples` phases is wrapped so
it prints its ordinal, its name, its start time and, when a local baseline
exists, its typical duration; and on completion its elapsed time:

```
[ 7/10] consumer-smoke   started 22:51:44  (typically 6s)
[ 7/10] consumer-smoke   ok 5s
[ 8/10] test-core        started 22:51:49  (typically 1m38s)
```

The banner is emitted by the step, so it cannot disagree with reality — the
class of bug §1.4 describes becomes unrepresentable rather than merely rarer.

#### 3.2.2 `Makefile` — a heartbeat inside the long steps

`test-core` and `run-examples` emit a liveness line every 30s while a module is
in flight (`[ 8/10] test-core  … 2m30s elapsed, module ./`). Silence then has a
name and a clock on it, and a stall is distinguishable from work without
inspecting processes.

#### 3.2.3 `Makefile` — the gate records its own verdict

The gate writes `.ci/last-run.json` when it ends, **on both paths**, containing:
the exit code, the failing step if any, per-step durations, the HEAD sha, and
the start/finish timestamps. Three properties are required:

- **Written by the gate**, so killing the caller cannot lose it (§1.1).
- **Absent means "did not finish"** — never inferred as a pass.
- **Carries the HEAD sha and start time**, so a stale file from an earlier run
  cannot be mistaken for this one.

#### 3.2.3b `scripts/ci-run.sh` — the runner protects its own run

Three properties, each earned by a failure in §1.5: the script's body is wrapped
in a braced group ending in `exit`, so bash parses it in ONE pass and an edit
during a run cannot corrupt it; each step runs in the background under an
interruptible `wait`, so a trap fires at once instead of after the step; and the
handler signals its whole process group before recording the verdict, so
stopping the gate stops `make` and `go test` too rather than leaving orphans
holding the CPU and the coverage file.

`make ci` drives all fourteen steps through a SINGLE invocation, so one run
produces exactly one verdict.

A dry run records nothing, guarded twice. The Makefile passes `$(MAKE_BIN)` — a
variable holding the same value under a name make does not recognise — so `-n`
prints these recipes instead of running them; and the driver refuses outright
when `MAKEFLAGS` carries `n`, covering any other route. Two guards because the
failure they prevent is a false PASS, and a false PASS is worse than no gate:
it is a gate that lies in the direction you want to believe.

#### 3.2.4 `.ci/timings.json`, `.gitignore` — a local baseline

Per-step durations from the last N runs, used only to print "typically …".
Machine-local and ignored (§3.1 #6). Absent baseline prints no estimate and says
so, rather than borrowing someone else's number.

#### 3.2.5 `CLAUDE.md` — how to read the gate

The CI-parity section gains the rule the failures teach: **judge a run by
`.ci/last-run.json`, never by the exit code of whatever wrapped it, and never by
a log line count**. Plus the two observer traps by name, since both are easy to
re-invent: a `pgrep` pattern that matches its own command line, and a
`::group::lint .` glob that also matches `./examples/`.

## 4 Verification

### 4.1 Regression tests

Shell-level, run from the repo (a Makefile change cannot be pinned by `go test`):

| # | Test | Asserts |
|---|---|---|
| T-1 | `make ci` on a clean tree | every step prints a banner with ordinal, name and elapsed; the ordinals are strictly increasing (the §1.4 backwards-progress bug cannot recur) |
| T-2 | kill the gate mid-`test-core` | `.ci/last-run.json` is ABSENT, and the documented reading of that is "did not finish" — never a pass |
| T-3 | a deliberately failing step (`COVER_MIN=100`) | the status file records `exit != 0` AND names `cover-check` as the failing step |
| T-4 | `make ci > log 2>&1; echo $?` from a wrapper | the status file's verdict matches the true outcome even when the wrapper's own exit code does not (§1.1's masking trap) |
| T-5 | a second run after a first | banners carry "typically …" from `.ci/timings.json`; a fresh clone prints no estimate and says the baseline is missing |
| T-6 | `git status --porcelain` after a run | clean — the timing baseline and status file are ignored, so the gate never dirties the tree |
| T-7 | SIGTERM the driver mid-`test-core` | the verdict records `interrupted by a signal`, AND no `go test` survives the driver — an unstopped child is a gate that is still running after you stopped it |
| T-8 | `make -n ci` | NO verdict is written and nothing executes — a dry run must never produce a pass. Also `MAKEFLAGS=n ./scripts/ci-run.sh …` directly, covering the guard independently of the Makefile |
| T-9 | `make -j4 <target>` | no `jobserver unavailable` warning — the recipes stay recursive so make passes its jobserver, and the dry-run guard, not a hidden `$(MAKE)`, is what makes `-n` harmless |
| T-10 | `env -u MAKEFLAGS ./scripts/ci-run.sh …` | the driver runs when invoked directly, as its usage line says it may be |
| T-11 | `make workflow-check` | `CI_CORE_STEPS` and the ten `make <step>` invocations in `check.yml` agree — the list is duplicated by necessity, so something must compare them |

### 4.2 Gate

`make ci` green end to end. The change touches no Go code, so diff-coverage has
nothing to measure. The parity requirement is that the ten core steps and their
order are unchanged — they are now named once in `CI_CORE_STEPS` and consumed by
the driver, so the list, the banners and the workflow cannot drift apart.

T-7 covers the signal path found while testing this script: a driver that traps
INT/TERM must take its children down with it. Killed without one, the driver
exits while `go test -race` runs on — an orphan holding CPU and the coverage
file, invisible to whoever believes they stopped the gate.

## 5 Prevention

- **A process that knows something must publish it.** Progress and verdict are
  facts only the gate holds; every attempt to re-derive them downstream failed.
  When a consumer has to guess, that is a missing output, not a careless
  consumer.
- **A status that only one process can read is a status that will be lost.**
  Exit codes are unforgeable but fragile: one wrapper, one filter, one kill and
  they are gone. Durable, self-described state survives all three.
- **Absent must never read as success.** Every mechanism here fails toward "did
  not finish". The 2026-08-08 false pass came from a layer that answered when it
  did not know.
- **A monitor is code and can be wrong.** Two monitors were wrong in one hour,
  both plausibly. Prefer a signal the producer emits over one an observer
  reconstructs; when reconstruction is unavoidable, test the reconstruction
  against a known-bad case.
- **Test the interruption, not just the happy path.** Every failure in §1.5 was
  invisible to a passing run and appeared the moment something was killed, edited
  or raced. T-2 and T-7 exist because "it works when nothing goes wrong" is the
  weakest thing a gate can demonstrate.
- **Do not edit a script or Makefile while a run of it is in flight.** Bash
  re-reads a script as it executes and each step spawns a fresh `make` that
  re-reads the Makefile. The braced-group wrapper makes the first case safe;
  nothing makes the second safe, so wait for the run.

## 6 Regressions and side effects

- The gate's stdout gains ~14 banner lines plus heartbeats. `::group::` folding
  is unaffected; the workflow's job structure is unchanged.
- `.ci/` appears in the working tree and is gitignored, so nothing is committed.
- No Go code changes; no runtime behaviour changes.
- **The "typically" estimate is warm-cache biased, and says nothing about a cold
  run.** Measured on consecutive runs of the same commit: `examples-lint` took
  1m30s cold and 19s warm, `lint-core` 21s then 3s. The median over recent runs
  therefore converges on warm figures, and a fresh clone or a cleared build cache
  will overshoot it substantially. The estimate orients; it does not bound. A
  step exceeding it is a reason to look, never a reason to act — and the
  heartbeat, not the estimate, is what proves the step is alive.

## 7 Related

Nothing in this document depends on unfinished work, and nothing is deferred.
The parallelisation of the module sweeps (§3.1 #4) is not deferred work — it is
rejected work: the wait is 146s and the goal is predictability, not speed.

## 8 Implementation summary

### 8.1 What landed

| File | Change |
|---|---|
| `scripts/ci-run.sh` | the driver: numbered banners with elapsed time and a typical-duration hint, a 30s heartbeat inside long steps, a machine-local timing baseline, and the verdict |
| `Makefile` | `CI_DIR`, `CI_CORE_STEPS`, `CI_EXAMPLE_STEPS` named once; `ci-core`, `ci-examples` and `ci` drive through the script; `ci` is a SINGLE invocation over all 14 steps |
| `.gitignore` | `.ci/` |
| `CLAUDE.md` | "Reading a gate run" — judge by the status file, absent means unfinished, and the two observer traps by name |
| `Makefile` (`workflow-check`) | compares `CI_CORE_STEPS` against the steps `check.yml` runs, so the duplicated list cannot drift silently |

### 8.2 Empirical findings

- **The gate takes ~2 minutes, not the 10–20 estimated.** Measured
  `PASS — 14/14 steps in 2m03s`, with `test-core` at 1m00s the largest single
  step. The 10–20 minute figure came from watching a process that had been dead
  for eighteen minutes — §1.3's cost, priced.
- **Cache state swings step times by 5x.** `examples-lint` 1m30s cold, 19s warm;
  `lint-core` 21s then 3s. The baseline is a median of recent runs precisely
  because the last run is a poor predictor.
- **Every mechanism here was wrong on its first attempt** (§1.5): the verdict was
  written twice, the script corrupted itself under a concurrent edit, and the
  signal handler could not fire during the step it most needed to interrupt.
  None of the three was visible to a passing run.
- **T-4 reproduced live during testing.** The pipeline reported `exit=0` while
  the status file recorded `FAIL` at `cover-check` — the masking trap, caught by
  the mechanism built to catch it.
- **The forged pass came from `make -n`, discovered by accident.** A dry run
  executed to check that the Makefile still parsed left a `PASS` verdict on
  disk, which was then read back as though a real run had produced it. Nothing
  in the design anticipated a command that is defined as doing nothing being
  able to record having done everything. It is now guarded twice and pinned by
  T-8.

### 8.3 Review notes NOT acted on

- **"`--no-print-directory` triggers a false dry-run."** Refuted by measurement:
  GNU make puts long options in their own words, so `MAKEFLAGS` reads
  `s --no-print-directory` and the first-word check never sees them. Only `-n`
  places an `n` there. The line does carry a real bug — the unbound variable
  above — but not this one.
- **"Running a step directly leaves a stale FAIL verdict."** Rejected: the file
  describes a gate RUN, not the state of the tree, and it carries that run's
  HEAD sha and finish time. `make test-core` on its own is not a gate run and
  must not claim to be one.

### 8.4 A defect the review did not find, and the tests did

Fixes #4 and #5 were each correct alone and wrong together. Putting the
heartbeat in its own process group (so killing it takes its `sleep` along) also
put it outside the group the signal handler kills — so an interrupted run left a
heartbeat printing into a log nobody was writing. Neither lens saw it, because
it exists only in the interaction; it appeared the first time T-7 was re-run
after both landed. Verify the combination, not the changes.

## 9 Open questions

None.
