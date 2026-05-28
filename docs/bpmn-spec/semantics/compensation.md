# Compensation

_Source: BPMN 2.0 §13.5.5 + §10.7 (spec p441–443, p301)._

Compensation undoes the effects of activities that **already successfully completed**. Used when the side-effects of a completed action need to be reversed because the larger transaction failed or is being aborted.

## Core distinction: compensation vs cancellation

| Term | Applies to | Purpose |
|---|---|---|
| **Cancellation** | _Active_ activities | Halt in-progress work |
| **Compensation** | _Completed_ activities | Reverse the side-effects of already-finished work |

An active activity cannot be compensated — it must be cancelled first. Cancellation of a Sub-Process can in turn trigger compensation of the successfully completed activities inside.

## Compensation handler — three forms

A compensation handler is a set of `Activities` not connected to the rest of the model. Three forms:

| Form | Where defined | Catch element |
|---|---|---|
| **Boundary compensation handler** | Boundary event on a specific Activity | `boundaryEvent` with `CompensateEventDefinition` |
| **Compensation Event Sub-Process** | Inside a Sub-Process or Process | Event Sub-Process whose Start Event has `CompensateEventDefinition` |
| **Compensation Activity** (associated) | Anywhere in the model | Linked via `Association` from a `compensationActivity` reference |

A handler connected via boundary event can only perform **"black-box" compensation** of the original Activity — modeled with a specialized **Compensation Activity** (a Task with `isForCompensation=true`).

## Implicit / default compensation

A Sub-Process has a `compensable` attribute. When set, **default compensation** is implicitly defined:

- Recursively compensates all successfully completed Activities within that Sub-Process.
- In **reverse order** of forward execution.

No explicit handler needed.

## Compensation triggering

Compensation is triggered by a **throw Compensation Event**, either:
- `Intermediate Throw Event` with `CompensateEventDefinition`, OR
- `End Event` with `CompensateEventDefinition`.

The Activity to compensate is referenced via the `CompensateEventDefinition`'s `activityRef`. If unspecified, defaults to the **current Activity** (i.e., the context in which the throw event resides).

### Scoping rules

| Scope of throw | Effect |
|---|---|
| Inline `error handler` of a Sub-Process | Triggers compensation for that Sub-Process |
| Global context (no `activityRef`) | All completed Activities in the Process are compensated |

### Sync vs async

| `waitForCompletion` | Behavior |
|---|---|
| `true` (default) | The throw Compensation Event waits for the compensation handler to complete |
| `false` | Fire-and-forget — compensation triggered, throw proceeds |

### Multi-Instance / Loop

- Each instance of a Loop or MI Sub-Process has its own **Compensation Event Sub-Process** with its own **snapshot data** captured at the time of that instance's completion.
- Triggering compensation for the MI Sub-Process **individually triggers** compensation for all instances within the current scope.
- If compensation is specified via a **boundary handler**, that boundary handler is invoked **once per instance** of the MI Sub-Process.

## Relationship between error handling and compensation

The spec calls out a **"presumed abort" principle**:

1. **Only completed activities are compensated.** Compensation of a failed Activity results in an empty operation. When an Activity fails (state: `Failing` / `Failed`), the responsibility of the error handler is to ensure no further compensation is triggered for it.
2. **Default error-driven compensation:** if no error Event Sub-Process is specified for a Sub-Process and an `error` occurs, the default behavior is to automatically call compensation for all contained Activities of that Sub-Process. This ensures the "presumed abort" invariant.

## Operational semantics

### Compensation handler enablement

- A `Compensation Event Sub-Process` becomes _enabled_ when its parent Activity transitions into state `Completed`.
- At that moment, a **snapshot** of the data associated with the parent Activity is taken and kept for later use.
- For MI / Loop: a separate snapshot is taken per instance.

### Compensation triggering

- When compensation is triggered for the parent Activity:
  - The `Compensation Event Sub-Process` is **activated** and runs.
  - The parent Activity's original data context is **restored** from the snapshot.
  - For MI / Loop: the dedicated snapshot for the affected instance is restored, and a dedicated `Compensation Event Sub-Process` is activated.
- An **associated Compensation Activity** becomes enabled when the Activity it is associated with transitions to `Completed`. When triggered, it is activated.
  - For MI / Loop parent: the Compensation Activity is triggered ONCE but compensates the effects of all instances (the activity is required to handle this internally).

### Ordering of default compensation

Default compensation runs Compensation Activities in **reverse order of forward execution**. Concurrency is allowed when there was no dependency between original activities.

**Dependencies MUST be respected:**

| Dependency | Rule |
|---|---|
| `Sequence Flow` from A → B | Compensate B before A |
| Data dependency (`IORules` specifying B references data produced by A) | Compensate B before A |
| Ad-Hoc Sub-Process: A and B were active; A completed before B started | Compensate B before A |
| Loop / sequential MI | Reverse order of forward execution |
| Parallel MI | Compensate in parallel |
| Sub-Process A has boundary event triggering B; event occurred | Compensate B before A. Applies also to multi-instances and loops. |

## Compensating state transitions

From the activity lifecycle ([../state-machines/activity-lifecycle.md](../state-machines/activity-lifecycle.md)):

- `Completed` → `Compensating` when a throw Compensation Event references this Activity.
- `Compensating` → `Compensated` when compensation completes successfully.
- `Compensating` → `Failed` when the compensation handler raises an exception.
- `Compensating` → `Terminated` when compensation is interrupted by controlled or uncontrolled termination.

## Cross-references

- Activity lifecycle (Compensating / Compensated states): [../state-machines/activity-lifecycle.md](../state-machines/activity-lifecycle.md)
- Multi-instance per-instance snapshots: [multi-instance.md](multi-instance.md)
- CompensateEventDefinition structural attributes: [../elements/event-definitions.md](../elements/event-definitions.md)
- Sub-Process `compensable` attribute: [../elements/activities.md](../elements/activities.md)
