---
title: Tasks
description: The activity types that do the work in a gobpm process.
---

# Tasks

Tasks are the steps that perform work. gobpm supports running your own Go code,
human steps, inline scripts, and decision-table evaluation. Each guide shows the
task type with a runnable example.

- [Service Task](service-task.md) — run your own Go code (in-process or via a worker). *(`service-task-worker`)*
- [User Task](user-task.md) — a human step: assign, list, complete. *(`usertask`)*
- [Script Task](script-task.md) — an inline expression/script step. *(`script-task`)*
- [Business Rule Task](business-rule-task.md) — evaluate a decision table. *(`business-rule-task`, `decision-table`)*
