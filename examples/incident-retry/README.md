# incident-retry — a failure the process survives

A payment call fails. Before this landing, that killed the whole instance —
every parallel branch, every hour of correct work. Here it opens an
**incident** instead (ADR-036): a durable record of the failure, with the
token preserved at the node and the instance alive.

```
start ──▶ charge ──▶ end
            │
            ├─ attempt 1: gateway unavailable  → incident raised
            ├─ attempt 2: policy retry, fails  → policy exhausted, operator's turn
            └─ attempt 3: operator RetryIncident → succeeds, incident resolved
```

What the run demonstrates:

- **the raise** — the technical failure ends its attempt and opens the
  incident, with the cause chain, the attempt count and the **failure-time
  data snapshot** (the `order_id` property, as the attempt saw it);
- **the two retry layers** — `activities.WithIncidentRetryPolicy(
  tasks.FixedDelay(2, …))` gives the engine one automatic re-entry; when it
  exhausts, the incident waits for an operator — the deliberate default;
- **the operator's surface** — `h.Incidents()` to inspect,
  `h.RetryIncident(ctx, id)` to re-enter the node now;
- **the outcome** — the process completes, the incident closes as
  `resolved`, and both failed attempts stay on record.

Run it:

```bash
go run .
```

The run asserts its own outcome (state, attempt count, incident state, the
snapshot's content) and exits non-zero on any mismatch.

Guide: [`docs/guides/operating/incidents.md`](../../docs/guides/operating/incidents.md).
