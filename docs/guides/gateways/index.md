---
title: Gateways
description: Routing and synchronization points that control flow through a process.
---

# Gateways

Gateways decide where the flow goes and when it converges. gobpm implements the
full BPMN gateway set — exclusive, parallel, inclusive, complex, and event-based
— each with split and join semantics and a runnable example.

- [Exclusive (XOR)](exclusive.md) — first-true routing + default flow. *(`gateway-routing`)*
- [Parallel (AND)](parallel.md) — concurrent split + synchronizing join. *(`parallel-gateway`)*
- [Inclusive (OR)](inclusive.md) — every-true split + OR-join. *(`inclusive-join`)*
- [Complex](complex.md) — activation-threshold join. *(`complex-gateway`)*
- [Event-based](event-based.md) — deferred choice; first event wins. *(`event-based-gateway`, `event-based-parallel-start`)*
