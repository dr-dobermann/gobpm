---
title: Custom worker dispatcher
description: Provide the external-worker job queue.
---

# Custom worker dispatcher

A `ServiceTask` marked `WithWorker(topic)` doesn't run in-process — the engine
**Enqueues a job** and parks the task until a worker fetches, runs, and reports
it. The `tasks.WorkerDispatcher` is that job queue: the seam between the engine
and however you actually distribute work. The built-in
[`localdispatcher`](../../../pkg/tasks/localdispatcher/) is an in-memory
fetch-and-lock store with a local worker pool — zero infrastructure. Swap in
your own when the queue must be **durable** (survive restarts) or **remote**
(HTTP/gRPC to out-of-process workers): implement the interface, register it with
`thresher.WithWorkerDispatcher`. This page is the extension reference — the
seam, the registration call, a minimal implementation, and how the engine drives it.

> You rarely need to write one. The `localdispatcher` already gives you an
> external-worker execution model with retries, output mapping, trust modes, and
> a local pool. Reach for a custom dispatcher only when the *transport* or
> *durability* of the queue itself must change.

## The seam interface

```go
type WorkerDispatcher interface {
    // Enqueue adds a job to the queue (non-blocking); the engine then parks the
    // ServiceTask.
    Enqueue(ctx context.Context, job Job) error

    // FetchAndLock returns and locks (for lockDuration, to workerID) the next
    // available jobs for the given topics, blocking until at least one is
    // available or ctx is done.
    FetchAndLock(
        ctx context.Context,
        workerID WorkerID,
        topics []Topic,
        lockDuration time.Duration,
    ) ([]LockedJob, error)

    // ExtendLock extends the lock on jobID (held by workerID) by newDuration
    // from now. Holder-only; bounded by the configured maxLockDuration.
    ExtendLock(
        ctx context.Context,
        jobID JobID,
        workerID WorkerID,
        newDuration time.Duration,
    ) error

    Complete(ctx context.Context, jobID JobID, workerID WorkerID, output *data.ItemDefinition) error
    ReportBpmnError(ctx context.Context, jobID JobID, workerID WorkerID, code, message string) error
    ReportStatus(ctx context.Context, jobID JobID, workerID WorkerID, value data.Value) error
    Fail(ctx context.Context, jobID JobID, workerID WorkerID, fault Fault) error
}
```

One method is **engine-facing** and the rest are **worker-facing**:

| Method | Who calls it | Role |
|---|---|---|
| `Enqueue` | the engine | queue a parked task's job. Non-blocking. |
| `FetchAndLock` | a worker | pull + lock the next jobs for its topics; block until one appears. |
| `ExtendLock` | a worker | renew a held lock while still working (heartbeat). |
| `Complete` | a worker | success — `output` is the operation's result item (nil if none). |
| `ReportBpmnError` | a worker | a self-classified Business Error `code` — caught by a matching Error boundary. |
| `ReportStatus` | a worker | a self-classified Business Status `value` — written to the task's `WithStatus` variable. |
| `Fail` | a worker | a raw `Fault` the engine's `ErrorMapper` classifies (empty `Fault` → default technical outcome). |

The four terminal reports mirror the four worker-outcome kinds (ADR-021 §2.6):
a worker either **self-classifies** (`ReportBpmnError` / `ReportStatus`) or hands
back a raw `Fault` for the engine to classify. Every report routes back to the
owning instance and resumes the parked track.

The job structs the seam moves:

| Type | Shape | Meaning |
|---|---|---|
| `Job` | `{ Input *data.ItemDefinition; Policy *Policy; ID JobID; Topic Topic }` | the unit the engine Enqueues; `Input` is the bound input-message item (nil if none). |
| `LockedJob` | embeds `Job` + `{ Deadline time.Time; WorkerID WorkerID }` | a `Job` a worker got from `FetchAndLock`, with its lock. |
| `Fault` | `{ Body *data.ItemDefinition; Cause error; Code string }` | a raw unclassified terminal fault; `{Code, Body}` feed the `ErrorMapper`, `Cause` is the Go diagnostic. |
| `JobID` | `string`; `MakeJobID(instanceID)` / `.InstanceID()` | embeds the owning instance id, so a completion routes back without a separate registry (SRD-036 §4.5). |
| `Topic` | `string` | a job's fetch key; equals the `ServiceTask`'s `WithWorker` topic. |

## The registration call

Pass your dispatcher when constructing the engine — it replaces the in-process default:

```go
func WithWorkerDispatcher(d tasks.WorkerDispatcher) Option
```

```go
engine, err := thresher.New("order-engine",
    thresher.WithWorkerDispatcher(disp))
```

At startup the engine binds itself into the dispatcher through four **optional**
capability interfaces — implement only the ones you need; a dispatcher that
reaches the engine another way (a remote adapter) can skip them:

| Optional binder | The engine hands you | So your dispatcher can |
|---|---|---|
| `SinkBinder` (`BindSink`) | its `JobCompletionSink` | deliver worker outcomes back to the owning instance. |
| `LoggerBinder` (`BindLogger`) | its configured `observability.Logger` | log with the embedder's logger, not a private default. |
| `ReporterBinder` (`BindReporter`) | its `observability.Reporter` | emit `JobState` facts (enqueue/lock/report/retry) on the engine seam. |
| `ExpressionEngineBinder` (`BindExpressionEngine`) | its `expression.Engine` | run a job's `ErrorMapper` engine-side under `EngineAuthoritative`. |

`SinkBinder` is the important one: the `JobCompletionSink` is how a terminal
report re-enters the instance loop. `localdispatcher` implements all four.

## A minimal dispatcher

The reference implementation is [`localdispatcher.Dispatcher`](../../../pkg/tasks/localdispatcher/)
— read its source for the full fetch-and-lock store. Here is the smallest shell
that satisfies the seam and captures the completion sink:

```go
type queue struct {
    sink tasks.JobCompletionSink
    jobs chan tasks.Job
}

func newQueue() *queue { return &queue{jobs: make(chan tasks.Job, 64)} }

// SinkBinder — the engine hands us the route back to the instance.
func (q *queue) BindSink(s tasks.JobCompletionSink) { q.sink = s }

// Engine-facing: park a job on the queue.
func (q *queue) Enqueue(_ context.Context, job tasks.Job) error {
    q.jobs <- job
    return nil
}

// Complete routes a success back through the sink.
func (q *queue) Complete(ctx context.Context, jobID tasks.JobID,
    _ tasks.WorkerID, output *data.ItemDefinition) error {
    var out []data.Data
    if output != nil {
        out = []data.Data{output}
    }
    return q.sink.ReportJobCompletion(ctx, tasks.NewWorkerComplete(jobID, out))
}

// …FetchAndLock / ExtendLock / ReportBpmnError / ReportStatus / Fail similarly.
```

The terminal reports build a `*tasks.WorkerOutcome` (`NewWorkerComplete`,
`NewWorkerBpmnError`, `NewWorkerStatus`, `NewWorkerFault`) and hand it to
`sink.ReportJobCompletion` — that is the whole contract for getting a verdict
back into the engine.

## How the engine uses it

The default (`localdispatcher`) also gives you a batteries-included worker pool,
so you can dispatch without writing a `FetchAndLock` loop at all. From
[`examples/service-task-worker/`](../../../examples/service-task-worker/):

```go
disp := localdispatcher.New(nil, 0) // nil clock, default max-lock
disp.RegisterWorker(ctx, "reserve", reserveWorker())     // a WorkerFunc per topic
disp.RegisterWorker(ctx, "authorize", authorizeWorker())

engine, err := thresher.New("order-engine",
    thresher.WithWorkerDispatcher(disp))
```

`RegisterWorker(ctx, topic, fn)` starts an in-process goroutine that
fetch-and-locks jobs for `topic`, runs `fn` (a `localdispatcher.WorkerFunc`:
`func(ctx, tasks.LockedJob) (*data.ItemDefinition, error)`), and reports
`Complete`/`Fail` for you. Running the example — a flaky worker retried
in-process under `WorkerTrusted`, then an output-mapped completion:

```
order-normal (amount 50):
  reserve attempt 1: inventory timeout — worker retries in-process…
  reserve attempt 3: reserved (reservationId=R-1001, zone=A-3)
  authorize: AUTHORIZED (Business Status)
  ✓ completed (Completed) → shipped [paymentStatus=AUTHORIZED, reservationId=R-1001, warehouseZone=A-3]

order-gateway-down (amount -1):
  authorize: PaymentGatewayDown (Business Error) → boundary
  ✓ completed (Completed) → payment-failed (Business Error caught by the boundary)
```

The engine parks the task on `Enqueue`, the pool worker executes and reports, and
the instance sees only the *final* verdict — retries and classification happen in
the worker, off the track. A durable or remote dispatcher slots into the same
`Enqueue` → `ReportJobCompletion` cycle; only the transport between queue and
worker changes.

## See also

- Examples: [`examples/service-task-worker/`](../../../examples/service-task-worker/)
- Related guides: [Service Task](../tasks/service-task.md) · [External workers](../operating/external-workers.md) · [Custom task distributor](task-distributor.md)
- Design: [ADR-021 — Service Task execution model](../../design/ADR-021-service-task-execution-model.md) · [ADR-004 — runtime environment contract](../../design/ADR-004-runtime-environment-contract.md) (remote transport)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/tasks`
