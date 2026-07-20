# escalation-events

Demonstrates **Escalation events** (SRD-058) — a non-critical signal a
sub-process raises to hand a business condition to an enclosing handler, without
faulting the instance.

```
start → [review-order] ─────────────────────> end-approved
             (raise OVER_BUDGET, Escalation End Event)
             ╳ (escalation boundary OVER_BUDGET, interrupting)
             └─> [notify-manager] ──────────> end-escalated
```

The `review-order` sub-process reaches an **Escalation End Event** that raises
`OVER_BUDGET`. Unlike an Error End Event, this does **not** fault the instance:
the escalation climbs the scope chain to the innermost matching catcher. Here an
**interrupting Escalation boundary** on the sub-process catches it by code,
cancels the sub-process, and routes a token to `notify-manager` — so the run
ends at `end-escalated`.

Escalation is **non-interrupting-capable** too (a non-interrupting boundary or
event-sub-process start forks a parallel handler and lets the sub-process run
on), and an **unresolved** escalation (no reachable catcher) is **logged**, never
silently dropped and never a fault.

## Run

```bash
go run ./examples/escalation-events/
```

Expected: the interrupting boundary fires (`Scope Canceled review-order`),
`notify-manager` prints, and the instance completes at `end-escalated`.
