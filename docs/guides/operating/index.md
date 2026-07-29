---
title: Operating gobpm
description: Running the engine in practice — observability, versioning, correlation, and workers.
---

# Operating gobpm

Once processes are built, these guides cover running the engine day to day:
subscribing to and filtering facts, managing definition versions, correlating
messages to the right instance, and dispatching work to external workers.

- [Observability in practice](observability.md) — subscribe, filter facts, tune log level. *(`data-change`)*
- [Definition versioning](registering-and-versioning.md) — register versions, latest vs pinned. *(`versioning`)*
- [Correlation & conversations](correlation.md) — route messages to the right instance. *(`inter-instance-correlation`, `conversation-routing`)*
- [External workers](external-workers.md) — fetch-and-lock job execution. *(`service-task-worker`)*
- [Persistence & recovery](persistence.md) — checkpoints, restart recovery, dehydration (a long wait costs no goroutines), leases & fencing for shared stores. *(`restart-recovery`)*
