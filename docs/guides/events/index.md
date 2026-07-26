---
title: Events
description: How a gobpm process starts, waits, reacts, and ends.
---

# Events

Events drive a process's reactive behavior — instantiation, waiting, throwing
and catching, and completion. This section covers every event type gobpm
supports, from start/end through timers, messages, signals, errors, and
boundary handlers, each with a runnable example.

- [Start & End](start-and-end.md) — instantiation and completion.
- [Timer](timer.md) — wait for a duration/instant. *(`simple-timer`, `timer-event`)*
- [Message](message.md) — send/receive across instances. *(`message-send-receive`, `message-intermediate-events`)*
- [Signal](signal.md) — broadcast to all listeners; signal start. *(`signal-broadcast`, `signal-start`)*
- [Error](error.md) — throw and catch a BPMN error; error boundary. *(`boundary-events`)*
- [Escalation](escalation.md) — non-fatal escalation throw/catch. *(`escalation-events`)*
- [Conditional](conditional.md) — wait on process data (false→true edge). *(`conditional-events`)*
- [Link](link.md) — off-page connectors within a process. *(`link-events`)*
- [Terminate](terminate.md) — end the whole instance/scope at once. *(`terminate-end-event`)*
- [Boundary events](boundary.md) — arm an event on an activity; interrupt or not. *(`boundary-events`)*
- [Event sub-processes](event-subprocess.md) — in-scope handlers, interrupting or not. *(`event-subprocess`)*
