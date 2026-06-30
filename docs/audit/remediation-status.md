# Third-pass audit — remediation status

Disposition of every finding in `code-review-third-pass-2026-06-29.md`, the
audit driving branch `fix/audit-remediation-2026-06`. Each row is **Fixed** (a
landed FIX), **Postponed** (reclassified as design work in `audit-backlog.md`),
or **Open** (not yet started — a remaining FIX-track cluster or a P1 needing its
own design).

Legend: ✅ Fixed · 🅿️ Postponed (design backlog) · ⏳ Open

| # | Finding | Sev | Disposition |
|---|---------|-----|-------------|
| 1 | `Snapshot.Clone` shares mutable process Properties across instances | 🔴 P1 | ⏳ Open — data-race; needs the data-flow ADR (audit-remediation triage 1.6/3.x), not a patch |
| 2 | `GExpression.Evaluate` nil-derefs a `(nil, nil)` result | 🟠 P2 | ✅ FIX-010 |
| 3 | `SignalEventDefinition.GetItemList` misnamed | 🟠 P2 | ✅ FIX-011 |
| 4 | `EventDefCloner` never satisfied | 🟠 P2 | ✅ FIX-011 |
| 5 | `RegisterProcess` godoc claims idempotent dedup | 🟠 P2 | ✅ FIX-013 (§1.1) |
| 6 | Parallel-start event gateway without a key double-instantiates | 🟠 P2 | 🅿️ AB-001 |
| 7 | Register/Unregister TOCTOU can orphan a live starter | 🟠 P2 | ✅ FIX-013 (§1.4) |
| 8 | `Run` stays `Started` on starter-registration failure | 🟠 P2 | ✅ FIX-013 (§1.2) |
| 9 | `WithRenderer` rejects a second renderer of the same impl type | 🟠 P2 | 🅿️ AB-002 |
| 10 | `UserTask.Exec` ignores context — goroutine leak | 🟠 P2 | 🅿️ AB-002 |
| 11 | `memrepo` can evict an Active instance after terminal→Active re-save | 🟠 P2 | ⏳ Open — Latent (persistence unwired); defer until persistence lands |
| 12 | `bpmncommon.Error.Structure()` nil-derefs; `NewError` accepts nil | 🟠 P2 | ✅ FIX-010 |
| 13 | `govulncheck` scans only the root module | 🟠 P2 | ⏳ Open — CI/build-hardening cluster |
| 14 | `scope.namesFrom` omits a `/`-keyed root scope | 🟡 P3 | ✅ FIX-014 (1.4) |
| 15 | `Array.Insert` off-by-one: cannot insert at `index == len` | 🟡 P3 | ✅ FIX-014 (1.1) |
| 16 | `Array.Clone` resets the iteration cursor to 0 | 🟡 P3 | ✅ FIX-014 (1.2) |
| 17 | `Array.Delete`/`DeleteT` skip the notification when emptying | 🟡 P3 | ✅ FIX-014 (1.3) |
| 18 | Unspecified-direction gateway doesn't enforce merge-or-split | 🟡 P3 | 🅿️ AB-003 |
| 19 | Default-flow routing relies on pointer identity | 🟡 P3 | ✅ FIX-014 (1.5) |
| 20 | `errs.M` format verbs with no args in `track.go` | 🟡 P3 | ✅ FIX-014 (1.6) |
| 21 | Cyclic timer fires N+1 times for a cycle count of N | 🟡 P3 | ✅ FIX-012 |
| 22 | Starter reconcile aborts mid-loop, leaving the hub partial | 🟡 P3 | ✅ FIX-013 (§1.3) |
| 23 | `DeriveKey` accepts a present-but-nil value as a key part | 🟡 P3 | ✅ FIX-014 (1.7) |
| 24 | `clocktest.Advance` allows moving the clock backwards | 🟡 P3 | ✅ FIX-014 (1.8) |
| 25 | `Message.Clone` drops `BaseElement` documentation | 🟡 P3 | ✅ FIX-014 (1.9) |
| 26 | `memmetrics.seriesKey` uses `%v`, distinct sets collide | 🟡 P3 | ✅ FIX-014 (1.10) |
| 27 | `memtrace.liveSpan` mutates span state without synchronization | 🟡 P3 | ✅ FIX-014 (1.11) |
| 28 | `test`/`test-all`/`test_race` lack `-count=1` | 🟡 P3 | ⏳ Open — CI/build-hardening cluster |
| 29 | `.golangci.yml` `tests: false` disables govet on `_test.go` | 🟡 P3 | ⏳ Open — CI/build-hardening cluster |
| 30 | `depguard` rule will block the runtime server binary | 🟡 P3 | ⏳ Open — CI/build-hardening cluster (latent) |
| 31 | `make clear` errors on a clean checkout | 🟡 P3 | ⏳ Open — CI/build-hardening cluster |

## Tally

- **✅ Fixed — 20** (FIX-010: #2, #12 · FIX-011: #3, #4 · FIX-012: #21 ·
  FIX-013: #5, #7, #8, #22 · FIX-014: #14–17, #19, #20, #23–27).
- **🅿️ Postponed — 4** across 3 backlog entries (AB-001: #6 · AB-002: #9, #10 ·
  AB-003: #18). See `audit-backlog.md`.
- **⏳ Open — 7**: the P1 data-flow race (#1), a latent persistence item (#11),
  and the 5-finding **CI/build-hardening cluster** (#13, #28, #29, #30, #31) —
  the next candidate FIX on this branch.

All landed FIX docs are Accepted in `docs/fix/`. Earlier audits
(`architecture-audit-2026-06-11`, `code-review-codex-second-pass-2026-06-29`)
were remediated by the prior FIX-001…009 series.
