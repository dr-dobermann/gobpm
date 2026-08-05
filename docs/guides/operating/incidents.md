---
title: Incidents & retry
description: A technical failure becomes durable, operable state — inspect, retry, resolve or drop it.
---

# Incidents & retry

A technical failure — an in-process task error, a worker whose retries
exhausted, a script with no engine, an uncaught BPMN error nobody's boundary
catches — does **not** terminate the instance. The failing attempt's track
ends, and an **incident** opens on the instance: a durable record carrying the
node, the cause chain, the attempt history and a **failure-time data
snapshot** — the variables visible from the failing node's scope, exactly as
the attempt saw them, immune to later sibling writes. Sibling branches keep
running; the token stays visible at the failing node (`TokenIncident`); the
instance stays alive until someone — a retry policy or an operator — decides
the continuation. (ADR-036 is the governing decision.)

Three failures deliberately keep the old fatal path: an **invariant
violation** (the engine's own state is suspect — retrying compounds it), an
**Error End Event** reaching the root uncaught (the model's own verdict), and
any failure in a **called process** — to its caller the whole child is a
single task, so the failure crosses the call boundary and the incident arises
at the top-level caller's Call Activity, whose retry re-runs the whole child.

## Automatic retry — two layers

The first line is the worker dispatcher's job retry
([external workers](external-workers.md)) — it runs below the engine loop and
never opens an incident. The **incident retry policy** acts after that
automation gives up, or immediately for in-process failures:

```go
// per activity:
task, _ := activities.NewServiceTask("charge", op,
    activities.WithIncidentRetryPolicy(tasks.FixedDelay(2, time.Second)))

// or engine-wide:
th, _ := thresher.New("engine",
    thresher.WithIncidentRetryPolicy(tasks.ExponentialBackoff(5, time.Second, time.Minute, true)))
```

A retry **respawns** a fresh track at the failed node — against the scope's
current data, with the failed track as its lineage predecessor — and armed
boundary events carry over without re-arming: an SLA timer keeps ticking
against the stuck node, and failing repeatedly never resets its clock. With
**no policy anywhere — the default — every incident waits for an operator.**

## The operator's surface

```go
for _, inc := range h.Incidents() {           // ordered by first raise
    fmt.Println(inc.NodeName, inc.State, inc.Attempts, inc.Cause)
    fmt.Println(string(inc.Data))             // the failure-time snapshot
}

h.RetryIncident(ctx, id)   // re-enter the node now, policy budget ignored
h.ResolveIncident(ctx, id) // "the work's effect exists": continue from the
                           // node's outgoing flows WITHOUT re-executing it
h.DropIncident(ctx, id)    // give up: the record becomes the durable dead
                           // letter; the instance never completes past it —
                           // it waits for your next act (Cancel, compensation)
```

`h.OpenIncidents()` is the cheap "does it need me?" probe; at the store level
the same question is `repository.StatusActiveIncidents` — an in-flight record
with open incidents, listed by recovery like any active instance. An op on a
**parked** instance (one whose loop exited with only incidents left) rebuilds
it from its checkpoint first — you never care which state it was in.

Incidents survive restarts: they ride the checkpoint (schema 3), a scheduled
retry re-arms its deadline on recovery, and a dead-lettered record is retained
as the durable dead letter.

Runnable: [`examples/incident-retry/`](../../../examples/incident-retry/) —
fail → policy retry → exhaustion → operator inspects and retries → completion.
