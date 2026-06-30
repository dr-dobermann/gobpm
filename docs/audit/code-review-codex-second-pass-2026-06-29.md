# Codex Second-Pass Code Review Notes

Date: 2026-06-29

Scope: findings from an additional pass over the event hub, timers, in-memory broker, instance lifecycle, local task dispatcher, default model data, and build tooling.

This file is separate from `docs/code-review-2025.md` and focuses on the causes behind the issues, the concrete evidence in code, and proposed fixes.

## Executive Summary

The strongest pattern is inconsistent boundary validation and lifecycle ownership. Several APIs accept nullable or cancelable inputs but dereference them before validation, ignore `context.Context`, or let stale handles mutate state after unsubscribe. A second pattern is abstraction bypass: timer code accepts an injected clock but later falls back to the real system clock.

Priority:

| Priority | Finding | Risk |
| --- | --- | --- |
| P1 | Timer waiter bypasses injected `Clock` | Fake clocks can hang tests; production behavior diverges from runtime abstraction |
| P1 | Lazy `AddEventKey` can mutate an unsubscribed broker subscription | Buffered messages can be moved into a dead channel and effectively lost |
| P1 | `EventHub` can panic on nil event definitions after start | Public API returns panic instead of domain error |
| P2 | `Instance.RegisterEvent` can panic on nil processor for terminal instances | Validation path is order-dependent and unsafe |
| P2 | `membroker` ignores canceled contexts | Canceled calls still mutate broker state |
| P2 | `data.CreateDefaultStates` writes package globals without synchronization | Race-prone initialization if tests or users call it concurrently |
| P3 | `localdispatcher.Register` accepts nil handlers | Deferred panic during dispatch |
| P3 | Pinned tools are not version-checked | Wrong installed binary passes `require-tool` and fails later |

## 1. Timer waiter bypasses injected `Clock`

Evidence:

- `internal/eventproc/eventhub/waiters/timer.go:257-293`
- `pkg/clock.Clock` owns both `Now()` and `After()`

Cause:

The waiter validates absolute timer values with `tw.rt.Clock().Now()`, but later calculates the delay with `time.Until(tw.next)` and waits with `time.NewTicker(tw.duration)`. That mixes two time sources in the same component: the injected runtime clock for validation, and the real wall clock for execution.

Why this matters:

If a test or embedding application provides a fake clock, timer creation can appear valid, but the service goroutine still waits on real time. This can make deterministic timer tests hang or pass only by sleeping. It also weakens the runtime extension contract: callers can inject a `Clock`, but timer waiting does not fully honor it.

Suggested fix:

- Compute the duration as `tw.next.Sub(tw.rt.Clock().Now())`.
- Wait with `tw.rt.Clock().After(duration)`.
- Avoid `time.NewTicker` for one-shot timer waits unless repeating behavior is explicitly required.
- Add a test with a fake clock that advances time without real sleeping.

## 2. Lazy `AddEventKey` can drain messages into an unsubscribed subscription

Evidence:

- `internal/eventproc/eventhub/eventhub.go:619-642`
- `internal/eventproc/eventhub/waiters/message.go:54-59`
- `pkg/messaging/membroker/membroker.go:88-100`
- `pkg/messaging/membroker/membroker.go:112-124`

Cause:

`EventHub.AddEventKey` looks up a waiter under `RLock`, releases the lock, and then calls `AddKey` on the waiter. In parallel, `UnregisterEvent` or `RemoveWaiter` can stop that waiter and unsubscribe it from the broker. The broker subscription handle does not track an active/closed state, so `AddKey` can still mutate a detached subscription and call inbox draining logic after unsubscribe.

Why this matters:

Buffered messages can be moved from the broker inbox into a subscription channel that nobody reads anymore. That creates message loss for later live receivers. This is a lifecycle ownership bug: the handle outlives the subscription registration, but still has write authority over broker state.

Suggested fix:

- Add an `active` or `closed` flag to broker subscription state, protected by the broker mutex.
- Make `Unsubscribe` mark the subscription inactive before removing it from indexes.
- Make `AddKey` return an error or no-op when the handle is inactive.
- Guard `messageWaiter.sub` with the waiter mutex or a narrower lifecycle lock.
- Add a race/lifecycle test: subscribe, buffer a matching message, concurrently unsubscribe and add a key, then verify the message is not drained into a dead subscription.

## 3. `EventHub` panics on nil event definitions after start

Evidence:

- `internal/eventproc/eventhub/eventhub.go:140-180`
- `internal/eventproc/eventhub/eventhub.go:204-246`
- `internal/eventproc/eventhub/eventhub.go:429-438`

Cause:

`RegisterEvent` and `RegisterPersistentEvent` validate the processor argument, but not the event definition argument before passing it into `registerWaiter`. `registerWaiter` dereferences `eDef` through `eDef.ID()` and `eDef.Type()`. `PropagateEvent` also uses `eDef.Type()` before a nil guard.

Why this matters:

The API has validation-style errors elsewhere, and tests cover some nil paths when the hub is not started. After start, the same nil input follows a different branch and can panic. That makes caller mistakes crash the process instead of returning a predictable domain error.

Suggested fix:

- Check `eDef == nil` at the public API boundary in `RegisterEvent`, `RegisterPersistentEvent`, and `PropagateEvent`.
- Put nil checks before lifecycle/state checks if later diagnostics dereference the input.
- Add tests for `started + nil eDef` and `PropagateEvent(nil)`.

## 4. `Instance.RegisterEvent` can panic on nil processor for terminal instances

Evidence:

- `internal/instance/instance.go:1515-1539`

Cause:

The terminal-state branch builds diagnostics with `proc.ID()` before checking whether `proc` is nil. The nil processor validation exists, but it happens too late for this state-specific branch.

Why this matters:

For a terminal instance, `RegisterEvent(nil, eDef)` can panic instead of returning the intended validation error. This is another validation ordering bug: diagnostic construction is not safe for invalid input.

Suggested fix:

- Validate `proc` and `eDef` before checking terminal-state behavior.
- Avoid dereferencing optional arguments while building diagnostics.
- Add a terminal-instance test for nil processor and nil event definition.

## 5. `membroker` ignores canceled contexts

Evidence:

- `pkg/messaging/membroker/membroker.go:158-205`
- `pkg/messaging/membroker/membroker.go:211-225`

Cause:

`Publish` and `Subscribe` accept `context.Context`, but the implementation does not inspect it. A canceled context can still create a subscription, buffer a message, or deliver a message.

Why this matters:

Context parameters communicate cancellation and deadlines. Ignoring them makes callers believe a canceled operation was stopped when it actually committed broker state. It also makes shutdown and timeout behavior harder to reason about.

Suggested fix:

- Return `ctx.Err()` before acquiring the broker mutex when the context is already canceled.
- If locking or later work can block, check `ctx.Err()` again before committing state.
- Add tests for canceled `Publish` and canceled `Subscribe`.

## 6. `data.CreateDefaultStates` has unsynchronized global initialization

Evidence:

- `pkg/model/data/state.go:20-27`
- `pkg/model/data/state.go:85-109`

Cause:

The package stores default state pointers in globals and initializes them through a function that reads and writes those globals without `sync.Once`, a mutex, or atomics.

Why this matters:

The helper is used across tests and can also be called by library users. If calls become parallel, the globals can race. Even if today most tests are sequential, this becomes fragile as soon as `t.Parallel()` is added around model tests.

Suggested fix:

- Prefer immutable package-level defaults initialized at package load time.
- If lazy initialization is required, wrap it in `sync.Once`.
- Add a `go test -race` path that covers repeated concurrent calls.

## 7. `localdispatcher.Register` accepts nil handlers

Evidence:

- `pkg/tasks/localdispatcher/localdispatcher.go:42-75`

Cause:

`Register` stores the handler without checking whether it is nil. `Dispatch` later retrieves it and calls `h(ctx, job)`.

Why this matters:

The actual failure is delayed until dispatch time, where it appears as a panic far away from the invalid registration. This makes the source of the bug harder to diagnose and can crash worker execution.

Suggested fix:

- Reject nil handlers in `Register`.
- Return a sentinel or wrapped validation error.
- Consider rejecting empty job types at the same boundary.
- Add tests for nil handler and empty job type.

## 8. Pinned tool versions are not enforced

Evidence:

- `Makefile:44-53`
- `Makefile:103-107`
- Observed locally: installed `mockery v2.32.4` satisfies `command -v mockery`, but the project config expects `mockery v3.5.0`.

Cause:

`require-tool` checks only that a binary exists in `PATH`. It does not verify the version that the Makefile pins. That means a globally installed incompatible binary can pass the preflight check and fail later with confusing config errors.

Why this matters:

Generated mocks are part of the test/build workflow. If developers have different global tool versions, the same target behaves differently across machines. This is especially visible for `mockery`, because the v3 config contains keys that v2 rejects.

Suggested fix:

- Run pinned tools directly with `go run`, for example `go run github.com/vektra/mockery/v3@$(MOCKERY_VERSION)`.
- Or make `require-tool` also check `--version` output against the pinned version.
- Prefer local, versioned tool execution for generators and linters used in CI.

## Recommended Fix Order

1. Fix nil validation ordering in `EventHub` and `Instance.RegisterEvent`; this is low risk and removes public API panics.
2. Fix timer waiting to use the injected `Clock`; this aligns implementation with the runtime extension contract.
3. Fix broker subscription lifecycle around `AddKey` and unsubscribe; this is the most concurrency-sensitive change and needs focused tests.
4. Honor canceled contexts in `membroker`.
5. Make `CreateDefaultStates` concurrency-safe.
6. Add nil handler validation to `localdispatcher`.
7. Enforce pinned tool versions in the Makefile.

## Suggested Tests

- `EventHub`: started hub rejects nil event definition in `RegisterEvent`, `RegisterPersistentEvent`, and `PropagateEvent`.
- `TimerWaiter`: fake clock advances timer without real sleeping.
- `MessageWaiter/membroker`: concurrent unsubscribe and `AddKey` does not drain messages into a dead subscription.
- `Instance`: terminal instance rejects nil processor without panic.
- `membroker`: canceled `Publish` and `Subscribe` do not mutate broker state.
- `data`: repeated concurrent `CreateDefaultStates` passes under `go test -race`.
- `localdispatcher`: nil handler registration returns an error.
- `Makefile`: generator target uses the pinned mockery version even when another global version is installed.
