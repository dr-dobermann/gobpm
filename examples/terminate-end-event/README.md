# terminate-end-event

A **Terminate End Event** abnormally ending a process from one branch
(SRD-030 / BPMN §13.5.6).

```
start → split ─┬─ fraud-check ──> terminate-end   (kills the instance)
               └─ process-payment ──> payment-done
```

The fraud check finishes first and reaches a Terminate End Event: the engine
terminates the whole instance, cancelling the in-flight payment mid-charge. The
instance settles in `Terminated` (not `Completed`).

## Run

```bash
go run .
```

## Expected output

```
  ⚠ fraud-check: fraudulent order detected — terminating the process
  → process-payment: charging the card (takes ~3s)...
  ✗ process-payment: interrupted before it finished

✓ terminate-end-event finished (Terminated): …
```
