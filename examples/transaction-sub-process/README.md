# transaction-sub-process

Demonstrates a **Transaction Sub-Process** (SRD-061, ADR-028) — a Sub-Process
variant that aborts atomically on a **Cancel End Event**, the ACID-style
"all-or-nothing" unit in BPMN form.

```
start → (booking Transaction) → end
              ⚡ cancel-bnd → [notify-customer] → cx-end

booking = start → [reserve-seat] → [charge-card] → cancel-booking (Cancel End)
                     ╳ release-seat    ╳ refund-card   (Compensation boundaries)
```

```mermaid
flowchart LR
    start((start)) --> booking[booking Transaction]
    booking --> end((end))
    booking -.->|cancel-bnd ⚡| notify[notify-customer]
    notify --> cxEnd((cx-end))
    subgraph booking
        sStart((s-start)) --> reserve[reserve-seat]
        reserve --> charge[charge-card]
        charge --> cancelBooking(("cancel-booking<br>Cancel End"))
        reserve -.- compReserve((comp-reserve))
        compReserve -.-> release["release-seat<br>isForCompensation"]
        charge -.- compCharge((comp-charge))
        compCharge -.-> refund["refund-card<br>isForCompensation"]
    end
```

`reserve-seat` and `charge-card` complete and enter the Transaction scope's
**completion ledger**, each guarded by a **Compensation boundary** linked to its
undo handler. Reaching the **Cancel End Event** aborts the Transaction in a
fixed order (ADR-028 §2.3):

1. **compensate** the completed activities — scope-wide, **reverse completion
   order**, so `refund-card` runs before `release-seat`, waiting for both;
2. **terminate** any activities still running;
3. **leave** through the interrupting **Cancel boundary** onto `notify-customer`.

Key semantics on display:

- A Cancel End Event is legal **only inside a Transaction** (validated at
  registration); it performs no other end-event behavior (it wins, like
  Terminate).
- The order is load-bearing: compensation runs **before** the scope teardown,
  because the teardown discards the very ledger the compensation sweeps.
- The **Cancel boundary** is a model-declared exit, always interrupting — it is
  resolved by the abort directly, never through the event bus.

## Run

```bash
cd examples/transaction-sub-process
go run .
```

Expected: `reserve-seat` and `charge-card` print, then the abort prints the
undo handlers **card refunded first, seat released second**, then the customer
notification — and the instance completes.
