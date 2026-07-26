---
title: Sub-processes & reuse
description: Nesting scopes and invoking other processes for structure and reuse.
---

# Sub-processes & reuse

Sub-processes group work into nested scopes and let you reuse whole processes.
gobpm supports embedded sub-processes, call activities that invoke another
process as a child instance, and transaction sub-processes with ACID-like abort.

- [Embedded Sub-Process](embedded.md) — a nested scope in the same instance. *(`embedded-subprocess`)*
- [Call Activity](call-activity.md) — invoke another process as a child instance. *(`call-activity`)*
- [Transaction Sub-Process](transaction.md) — ACID-like abort via Cancel. *(`transaction-sub-process`)*
