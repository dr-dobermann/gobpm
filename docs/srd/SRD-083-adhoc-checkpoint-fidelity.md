# SRD-083 — Ad-Hoc Sub-Process checkpoint fidelity

| Field | Value |
|---|---|
| Status | Draft |
| Date | 2026-08-08 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-033 v.4](../design/ADR-033-persistence-and-state.md) §2.10 (composite constructs capture faithfully), [ADR-035 v.1](../design/ADR-035-adhoc-sub-process.md) (the Router-driven routing model whose position this persists) |
| Upstream | [ADR-023 v.3](../design/ADR-023-sub-process-and-call-activity.md) §2.4 (the scope lifecycle an ad-hoc container rides) |
| Related | [SRD-082](SRD-082-checkpoint-composite-fidelity.md) (the adoption machinery this extends; schema 4, which this bumps to 5), [SRD-074](SRD-074-adhoc-sub-process-router.md) (the runtime this makes durable) |
| Tracking | #307 |

The Ad-Hoc Sub-Process is the one composite whose position the checkpoint
still does not carry — and unlike the pre-SRD-082 constructs, it is not
guarded either: an in-flight container **is** captured today, and the
restored instance is **silently wrong**. This SRD records the routing state
(schema 5, additive), rebuilds it on restore, and makes a pre-fidelity
document with an open ad-hoc scope refuse loudly instead of resuming
corrupt.

## §1 Background (verified)

- **The routing state is loop-owned and never captured.** An open ad-hoc
  scope's progress lives in `adHocProgress` — `completed` and `running`
  counts, the manual-mode `offered` candidates, the `stopped` flag and
  `stopReason` (`internal/instance/adhoc.go:50-70`) — attached to the
  scope entry (`scope_runtime.go:85`). `captureDocument` walks scopes,
  ledgers and tracks (`checkpoint_capture.go:150-200`) and records none
  of it; `TrackRecord` (`checkpoint/document.go:148-169`) does not carry
  the track's `adHocActivity` assignment (`track.go:200`) either.
- **No guard defers the capture.** SRD-082 FR-8 retired the capture
  deferral guards entirely (`checkpoint_capture.go:145-148`) — so an
  in-flight ad-hoc container checkpoints without complaint, with its
  routing state absent from the document.
- **The restored instance is silently corrupt, four ways.**
  `adoptRestoredScopes` rebuilds a generic scope entry with `adHoc: nil`
  (`scope_runtime.go:399-405`), and:
  1. a restored inner activity's settle skips `settleAdHoc` on the nil
     guard (`scope_runtime.go:598`) — the Router is never consulted
     again, so the container drains and completes after the in-flight
     activities finish, **skipping all remaining routing**;
  2. the `completed`/`running` counts are gone, so even a Router that
     did run would decide over a false progress picture;
  3. a **manual** container's pending `offered` set is gone — nothing is
     left to activate and no settle will ever drive the drain: the
     container **hangs**;
  4. a container whose completion condition had fired (`stopped`) loses
     the flag — a settle after restore would **re-route work past
     completion**.
- **The adoption machinery to extend is proven.** SRD-082 landed the
  pattern this SRD reuses verbatim: a per-construct record adopted
  before the initial spawns (`adoptRestoredGroups`,
  `scope_runtime.go:417`), restored-state plumbing on the instance
  (`restore.go:129-130`), and loud refusals when a record names state
  the track table does not carry.
- **The schema is at 4.** `Document.Marshal` stamps 4 and `Unmarshal`
  refuses schemas beyond it with "schema 1..4" (SRD-082 FR-1); the bump
  to 5 follows the same additive discipline.

## §2 Requirements

### §2.1 Functional

- **FR-1 — schema 5, additive.** The document gains
  `AdHoc []AdHocRecord` (one per open ad-hoc scope) and `TrackRecord`
  gains `AdHocActivity string` (the inner activity a routed track runs;
  empty for every other track). `Marshal` stamps 5; `Unmarshal` accepts
  1..5 and refuses 6+ loudly. Absent records rebuild nothing — every
  schema ≤ 4 document without an open ad-hoc scope restores exactly as
  today.
- **FR-2 — capture records the routing state.** For every scope entry
  with a non-nil `adHoc`, capture emits an `AdHocRecord`: the host
  track id, the scope path, the `completed` counts, the `offered`
  candidate ids (manual mode), `stopped` and `stopReason`. The
  `running` counts are **derived, not recorded** (ADR-033 v.4 §2.1
  minimality): each live routed track already records its
  `AdHocActivity`, and the counts rebuild from the track table.
- **FR-3 — restore rebuilds the progress.** An adoption pass
  (`adoptRestoredAdHoc`, running with the SRD-082 adoption passes before
  the initial spawns) rebuilds `entry.adHoc` on the adopted scope entry:
  restored `completed` counts, `offered` resolved against the
  container's inner nodes, `stopped`/`stopReason` reinstated, and
  `running` counted from the restored tracks of that scope carrying an
  `AdHocActivity`. Restored routed tracks get their `adHocActivity`
  field back from the track record.
- **FR-4 — routing resumes at the recorded position.** After restore:
  a settled inner activity consults the Router with the restored
  progress (`Completed`/`Running` true to the pre-kill state, `Last` =
  the settled activity); a `stopped` container runs **no** further
  routing and completes when its live activities drain; a manual
  container's restored offer is visible through `AdHocView` and
  consumable through `ActivateAdHoc`; the sequential-ordering
  invariant (one live activity) holds across the restore.
- **FR-5 — refusals are loud and classified.** A restore refuses, naming
  the cause, when an `AdHocRecord` names a host track the track table
  does not carry; when the named host's node is not an ad-hoc
  container; or when an offered id resolves to none of the container's
  inner nodes.
- **FR-6 — a pre-fidelity document refuses.** When scope adoption finds
  an open scope whose host node is an ad-hoc container (`adHocOf` is
  non-nil) but the document carries **no** matching `AdHocRecord` (any
  schema ≤ 4 capture of an in-flight container), the restore refuses,
  naming the scope — loud beats the silent corruption of §1. Documents
  without in-flight ad-hoc work are unaffected.

### §2.2 Non-functional

- **NFR-1** — the capture stays loop-serialized: all recorded state is
  loop-owned (`adHocProgress` and `track.adHocActivity` are written on
  the loop goroutine pre-spawn), so no new synchronization appears.
- **NFR-2** — race-clean under `-race`; diff-coverage ≥ 95% (aim 100%)
  on touched files.

## §3 Models

```go
// checkpoint/document.go — Schema 5 (SRD-083)
type AdHocRecord struct {
    HostTrack  string         `json:"host_track"`
    ScopePath  string         `json:"scope_path"`
    Completed  map[string]int `json:"completed,omitempty"`
    Offered    []string       `json:"offered,omitempty"`
    StopReason string         `json:"stop_reason,omitempty"`
    Stopped    bool           `json:"stopped,omitempty"`
}

type TrackRecord struct {
    // ... unchanged fields ...
    AdHocActivity string `json:"adhoc_activity,omitempty"` // SRD-083
}
```

**Worked trace.** An automatic container `triage` (inner activities
`a`,`b`,`c`; Router: run `a`, then `b`+`c` in parallel, stop when all
three completed once) is killed after `a` completed and while `b` and
`c` run. The capture holds: the scope record for `/p/triage`; two track
records at `b` and `c` with `AdHocActivity: "b"`/`"c"`; one
`AdHocRecord{HostTrack: <host>, ScopePath: "/p/triage",
Completed: {a:1}}`. Restore adopts the scope entry, rebuilds
`adHoc.completed={a:1}`, counts `running={b:1,c:1}` from the two
tracks, and re-parks the host. `b` settles → `settleAdHoc` consults the
Router with `Completed{a:1,b:1}, Running{c:1}, Last:"b"` — exactly the
pre-kill contract. When `c` settles the Router answers empty
(`router-empty`), the scope drains, and the container completes with
the same terminal fact stream as an unkilled run.

## §4 Analysis & decisions

- **Record the offer; do not re-ask the Router at restore.** A manual
  container's pending offer is restored as recorded rather than
  re-derived. Re-asking would emit a second `Offered` fact for the same
  decision and could answer differently against restored data —
  the offer the host saw is the offer that must stand. (Rejected:
  re-route on restore.)
- **Derive `running`, record `completed`.** Live work is exactly what
  the track table already carries — recording it twice invites the
  tables to disagree. Completed counts exist nowhere else once the
  tracks are gone, so they are recorded. (Rejected: recording both;
  rejected: deriving `completed` from ledger-like replay — the ad-hoc
  container has no ledger.)
- **Refuse a pre-fidelity in-flight document (FR-6) rather than restore
  it as today.** Today's behavior is the silent corruption of §1 — the
  only honest options are refusal or fidelity, and a schema ≤ 4
  document cannot offer fidelity. The refusal names the scope so an
  operator knows which instance to re-run. (Rejected: preserving
  today's silent restore for compatibility — compatibility with
  corruption is not a contract worth keeping.)
- **No new persist points.** Routing decisions happen synchronously on
  the loop inside settle handling; every existing capture point sees a
  consistent `adHocProgress`. (Rejected: a capture-on-route persist
  point — nothing waits between routing and the next persist point that
  could lose more than the standard at-least-once window.)

## §5 API deltas

None public. The checkpoint schema (internal) gains the §3 records; no
engine, model or thresher surface changes.

## §6 Test scenarios

| # | Test | Verifies |
|---|---|---|
| T-1 | schema-5 round-trip (`internal/instance/checkpoint`) | FR-1: records marshal/unmarshal; schema stamps 5; future refusal names 1..5 |
| T-2 | automatic container mid-flight kill/restore (`internal/instance`) | FR-2/FR-3/FR-4: restored counts feed the Router; routing resumes; the container completes with the unkilled run's outcome |
| T-3 | manual offer kill/restore (`internal/instance`) | FR-3/FR-4: the restored offer is visible in `AdHocView` and consumable by `ActivateAdHoc`; nothing re-routes |
| T-4 | stopped container kill/restore (`internal/instance`) | FR-4: no routing after restore; the container completes on drain |
| T-5 | refusal table (`internal/instance`) | FR-5/FR-6: missing host track, non-ad-hoc host node, unresolvable offered id, pre-fidelity in-flight document — each refuses with its cause |
| T-6 | schema-4 document without ad-hoc work (`internal/instance`) | FR-1/FR-6: restores exactly as today |
| T-7 | e2e kill-and-resume through the engine (`pkg/thresher`) | the §3 worked trace end-to-end over a real Repository |

## §7 Milestones

- **M1 — schema 5 + capture.** FR-1, FR-2; T-1.
  `feat(instance): schema-5 ad-hoc routing records; capture emits them (SRD-083 M1)`.
- **M2 — restore adoption + refusals.** FR-3, FR-4, FR-5, FR-6;
  T-2..T-6.
  `feat(instance): an in-flight ad-hoc container restores at its position (SRD-083 M2)`.
- **M3 — the proof + docs.** T-7; guides (`operating/persistence.md`
  drops the ad-hoc line from "Current limits",
  `subprocesses/adhoc.md` gains its restart note), CHANGELOG, the
  roadmap §5 map files #305–#308.
  `feat(thresher): the ad-hoc kill-and-resume e2e; docs (SRD-083 M3)`.

## §8 Cross-doc

- Implements **ADR-033 v.4** §2.10 and **ADR-035 v.1** (no ADR change:
  the routing model is untouched; only its durability lands).
- Upstream: **ADR-023 v.3** §2.4.
- Related: **SRD-082** (adoption machinery, schema 4 → 5), **SRD-074**
  (the runtime made durable; frozen one-shot, not retro-edited).
- **#307**: closes it.

## §9 Definition of Done

- [ ] FR-1…FR-6 implemented; every §6 test exists and passes.
- [ ] `make ci` green; diff-coverage ≥95% (aim 100%); touched
      functions ≥80%.
- [ ] The persistence guide's "Current limits" no longer lists ad-hoc
      routing state; the ad-hoc guide carries the restart note.
- [ ] §10 filled.

## §10 Implementation summary

*Post-landing placeholder.*

## Open questions

*None — §4 records the resolved design points (offer restored as
recorded, derived running counts, the pre-fidelity refusal, no new
persist points).*
