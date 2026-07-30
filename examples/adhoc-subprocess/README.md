# adhoc-subprocess

An **Ad-Hoc Sub-Process** — inner activities with **no sequence flows** between
them, whose order is decided while the case runs (BPMN §13.3.5).

```
start → (triage) → end

triage:  gather-logs   notify-customer   escalate   close-incident
         └── no flows between them; a Router answers what runs next ──┘
```

## What it shows

- **A Router replaces sequence-flow succession.** `router.go` is the only thing
  that says what follows what. The engine consults it when the container opens
  and again after each inner activity settles.
- **The decision reads the case's own data.** The Router reads the `severity`
  property through the ad-hoc scope's walk-up, so `"high"` takes a different
  path than `"low"` — ad-hoc routing is more than counting activities.
- **A fork, and a join without a join gateway.** A high-severity incident starts
  `notify-customer` **and** `escalate` at once. When the first of them settles
  the Router answers empty — which ends only *that* track, leaving its sibling
  to finish — and answers `close-incident` when the last one settles.
- **Completion is the drain.** The final empty answer leaves no track behind,
  the scope drains, and the Sub-Process completes. There is no separate
  completion mechanism.

## Run

```bash
go run .
```

Expected (the engine's log lines interleave):

```
    ▶ gather-logs: collected the incident logs
  ▷ router: severity is high → notify AND escalate
    ▶ escalate: paged the on-call engineer
  ▷ router: escalate done, but a sibling is still running
    ▶ notify-customer: told the customer we are on it
  ▷ router: all work settled → close the incident
    ▶ close-incident: closed the incident
  ▷ router: nothing left → the container ends
```

The two forked activities may settle in either order — that is the point of an
ad-hoc container.

## Notes

- The activities are given **explicit ids** (`foundation.WithID("gather-logs")`)
  because the Router's `State` is keyed by id. Ids are unique where names are
  not; giving readable ones keeps the routing code reading like the diagram.
- A Router must be prompt and read-only. Waiting for a human is expressed by
  `WithAdHocManualSelection()`, where the engine offers the enabled set and a
  host calls `Activate` — not by a Router that blocks.
- Common shapes need no hand-written Router at all: see
  `pkg/adhoc/routers` for `Standard()`, `Expression(expr)` and
  `Sequence(ids...)`.

Full reference: [Ad-Hoc Sub-Process](../../docs/guides/subprocesses/adhoc.md).
