# boundary-events

An **interrupting timer boundary** as a timeout on a long-running activity
(SRD-029 / ADR-018).

```
start → [process-payment] ───────────────> end-paid
             ╳ (timer boundary, 2s, interrupting)
             └─> [cancel-order] ─────────> end-cancelled
```

A 2s timer boundary attached to a ~4s payment ServiceTask fires first: the engine
cancels the payment track, the activity's context-honouring op returns early, its
result is discarded by the interruption checkpoint, and a token is routed onto the
boundary's exception flow to `cancel-order`.

## Run

```bash
go run .
```

## Expected output

```
  → process-payment: charging the card (takes ~4s)...
  ✗ process-payment: interrupted before it finished
  → cancel-order: payment timed out, releasing the reservation

✓ boundary-events completed (Completed): …
```
