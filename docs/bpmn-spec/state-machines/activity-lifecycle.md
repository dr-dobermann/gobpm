# Activity Lifecycle

_Source: BPMN 2.0 §13.3.2 (spec p428–429). Normative state machine in Figure 13.2._

Every BPMN activity — Task variants, SubProcess, Transaction, Ad-Hoc Sub-Process, Call Activity — follows this lifecycle. State transitions are observable points where engine MUST persist state, emit events, and apply data assignments.

## State diagram

```
                Inactive
                   |
                A Token Arrives
                   v
                 Ready ─────────[Activity Interrupted, Alt Path for Event GW]──> Withdrawn
                   |                                                                  |
              Data InputSet Available                                       [The Process Ends]
                   v                                                                  |
                 Active ─────────[Activity Interrupted, Alt Path for Event GW]─> Withdrawn
                   |                                                                  v
              Activity's work completed                                            Closed
                   v
                Completing ─────[Activity Interrupted, Alt Path for Event GW]─> Withdrawn
                   |
              Completing Requirements Done + Assignments Completed
                   v
                Completed
                   |
              Compensation Occurs ─────────────> Compensating
                   |                                  |
              The Process Ends                  Compensation Completes ─> Compensated ─[Process Ends]─> Closed
                   v                            Compensation Failed   ─> Failed       ─[Process Ends]─> Closed
                 Closed                         Compensation Interrupted (decision)

  From Ready/Active/Completing on Interrupting Event:
                   |
              Error?
                Yes ─> Failing  ────[handler reaches final state]──> Failed     ─[Process Ends]─> Closed
                No  ─> Terminating ─[Terminating Requirements Done]─> Terminated ─[Process Ends]─> Closed
```

## States

| State | Terminal? | Meaning |
|---|---|---|
| **Inactive** | no | Activity not yet enabled. No tokens have arrived. |
| **Ready** | no | Required tokens have arrived (per `startQuantity`). Waiting for data InputSet to become available. |
| **Active** | no | InputSet bound; activity performing its work. New non-interrupting Event Sub-Processes MAY be created. |
| **Withdrawn** | yes | Lost a race at an Event-Based Exclusive Gateway. Reached via interrupt + alternative-path-selected. |
| **Completing** | no | Work done; waiting for non-interrupting Event Handlers attached during Active to finish. No new processing steps allowed. New non-interrupting Event Sub-Processes MAY NOT be created. |
| **Completed** | no | All completion dependencies satisfied. Outgoing sequence flows will be activated with `completionQuantity` tokens each (implicit Parallel Gateway). Data OutputSet selected and pushed. |
| **Compensating** | no | A throw Compensation Event has targeted this activity (which had been Completed). |
| **Compensated** | yes | Compensation handler completed successfully. |
| **Failing** | no | Interrupting Event Sub-Process triggered by an error has started. Activity remains here while running Event Handler is still active. |
| **Terminating** | no | Interrupting non-error event triggered. Activity remains here while running Event Handler is still active. |
| **Failed** | yes | Compensation failed, OR Failing handler reached final state. |
| **Terminated** | yes | Terminating handler reached final state. |
| **Closed** | yes (sink) | The Process ends. Terminal pseudo-state for all final-state activities. |

## Transitions

| From | To | Trigger / Condition |
|---|---|---|
| Inactive | Ready | A token arrives (count = `startQuantity`) |
| Ready | Active | A data InputSet becomes available |
| Ready | Withdrawn | Activity Interrupted + Event Gateway alternative-path selected |
| Ready | Failing | Activity Interrupted + Interrupting Event + Error |
| Ready | Terminating | Activity Interrupted + Interrupting Event + Non-Error |
| Active | Completing | Activity's work completed without anomalies |
| Active | Withdrawn | Activity Interrupted + Event Gateway alternative-path selected |
| Active | Failing | Activity Interrupted + Interrupting Event + Error |
| Active | Terminating | Activity Interrupted + Interrupting Event + Non-Error |
| Completing | Completed | All completion dependencies satisfied (Event Handlers done, Assignments complete) |
| Completing | Withdrawn | Activity Interrupted + Event Gateway alternative-path selected |
| Completing | Failing | Activity Interrupted + Interrupting Event + Error |
| Completing | Terminating | Activity Interrupted + Interrupting Event + Non-Error |
| Completed | Compensating | A throw Compensation Event references this activity |
| Completed | Closed | The Process ends |
| Compensating | Compensated | Compensation handler completed successfully |
| Compensating | Failed | Compensation handler raised an exception |
| Compensating | Terminated | Compensation interrupted by controlled / uncontrolled termination |
| Failing | Failed | Interrupting Event Handler reached final state |
| Terminating | Terminated | Terminating requirements done |
| Withdrawn / Compensated / Failed / Terminated | Closed | The Process ends |

## State-specific normative behavior

### Ready

- Required number of tokens is determined by `startQuantity` (default 1).
- An activity with multiple incoming sequence flows behaves as if preceded by an implied **Exclusive Gateway** (uncontrolled flow) — see [../semantics/token-flow.md](../semantics/token-flow.md).
- Activity with no incoming sequence flows is instantiated when the containing Process / SubProcess is instantiated (exception: Compensation Activities).

### Ready → Active (data binding)

- Each `InputSet` is evaluated in declaration order until one becomes _available_.
- An `InputSet` is _available_ iff each of its REQUIRED `DataInput`s is bound. A `DataInput` is REQUIRED iff it is NOT optional within that `InputSet`.
- Data binding executes the input `DataInputAssociation`s — values flow from process-scope `DataObject`s / `Property`s into the activity.
- If no `InputSet` is available, the activity waits until one becomes available. Does NOT proceed to Active.

### Active

- New non-interrupting Event Sub-Processes MAY be created and attached while in Active.
- Interrupt sources: thrown error, or any interrupting Event Sub-Process triggering.
- Race-condition withdrawal: when this activity is attached after an Event-Based Exclusive Gateway, the first sibling to complete causes all others to enter Withdrawn (from Ready or Active).

### Completing

- Exists to give non-interrupting Event Handlers attached during Active time to finish.
- Running handlers MAY complete; new non-interrupting handlers MAY NOT be created.
- No further processing steps of the activity itself are performed in this state.

### Active / Completing → Failing / Terminating

- All nested activities not in a final state (Completed, Compensated, Failed, etc.) are terminated.
- Non-interrupting Event Sub-Processes are terminated.
- Data context of the activity is preserved while an interrupting Event Sub-Process runs (so the handler can access it).
- Data context is released after the Event Sub-Process reaches a final state.

### Completed (outflow)

- Outgoing sequence flows activated; `completionQuantity` tokens (default 1) emitted on each (acts as implicit Parallel Gateway for multiple outgoing).
- `OutputSet` selection mirrors `InputSet` selection — evaluated in order until one is available. If none → runtime exception.
- If `IORule` defined: the chosen `OutputSet` is checked for compliance with the `InputSet` that started the instance. Non-compliance → runtime exception.

### Completed → Compensating

- Only Completed activities can be compensated.
- A `Compensation Event Sub-Process` (if defined) operates against a data snapshot taken at the moment the parent reached Completed.
- For Loop / MI Sub-Processes, each instance has its own snapshot and its own Compensation Event Sub-Process.

## Sub-Process-specific notes

A Sub-Process completes (Active → Completing → Completed) only when:
1. No tokens remain inside the Sub-Process.
2. No nested Activity is still active.

If a Terminate End Event fires inside, the Sub-Process is abnormally terminated (→ Terminating → Terminated).
If a Cancel End Event fires inside a Transaction, the Sub-Process is abnormally terminated AND the transaction aborted; control leaves through a cancel intermediate boundary event.

## Cross-references

- Process-level instantiation and termination: [process-lifecycle.md](process-lifecycle.md)
- Per-task-type behavior (Service / User / Send / Receive / etc.): [../semantics/tasks.md](../semantics/tasks.md)
- Sub-Process variants: [../semantics/sub-processes.md](../semantics/sub-processes.md)
- Multi-instance / Loop: [../semantics/multi-instance.md](../semantics/multi-instance.md)
- Compensation: [../semantics/compensation.md](../semantics/compensation.md)
- Token-flow at activity boundaries: [../semantics/token-flow.md](../semantics/token-flow.md)
- Event Sub-Process triggering: [../semantics/events.md](../semantics/events.md)
