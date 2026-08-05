---
title: Design documents
description: The architecture vision (SAD) and the decision records (ADRs) that fix gobpm's contracts.
---

# Design documents

This directory holds the *why* behind gobpm — the layered design record:

- **[SAD-001 — Vision and architecture](SAD-001-vision-and-architecture.md)**
  — the system architecture description: what gobpm is, the layer stack, and
  the strategic decisions the ADRs refine.
- **ADR-NNN — architecture decision records** — one prescriptive,
  standard-grounded decision each: the execution model, the extension
  architecture, events and subscriptions, the data plane, gateways and
  joins, persistence, observability, and so on. ADRs are versioned,
  continuously-current contracts; several carry a Russian twin — the twins
  live under [`ru/`](ru/ADR-001-execution-model.ru.md) (Russian twins are a
  SAD/ADR privilege; SRD/FIX landing records never get one).

Standard-claims in these documents cite the vendored
[BPMN 2.0 extract](../bpmn-spec/index.md) by section. How each decision
*landed* — requirements, milestones, tests — is recorded in the one-shot
documents under `srd/` and `fix/`, which the ADRs deliberately never
reference downward; follow a landing from its SRD upward instead.
