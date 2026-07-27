---
title: Custom authorization
description: Gate user-task actions with your own authorization.
---

# Custom authorization

gobpm is a library, not a security boundary — by default it authorizes
**every** sensitive operation, delegating the decision to the host application.
Reach for a custom authorization provider when the embedding application must gate
who may start a process, claim a user task, or cancel an instance: to enforce
tenant isolation, wire an RBAC/ABAC system, or refuse actions from an unknown
subject.

This page shows the seam interface, how to install your own provider, a minimal
real implementation, and how the engine uses it.

## The authorization request

The engine describes each decision as an `auth.Request` and asks a provider to
allow or deny it. A request is three opaque strings — the engine attaches no
identity or policy model of its own:

```go
type Request struct {
    Subject  string // the actor's identity, opaque to the engine
    Resource string // the target (e.g. a process or instance ID)
    Action   Action // the operation being attempted
}
```

`Action` is a typed string naming the sensitive operation. The engine defines
these:

| Action constant | Value | Covers |
|---|---|---|
| `auth.ActionStartProcess` | `process.start` | starting a Process Instance |
| `auth.ActionClaimUserTask` | `usertask.claim` | claiming a UserTask |
| `auth.ActionCancelInstance` | `instance.cancel` | canceling / terminating an Instance |

`Subject` and `Resource` are yours to interpret — the engine passes them through
unexamined, so their meaning is a contract between your call-site and your
provider.

## The seam interface

A provider is anything with an `Authorize` method:

```go
type AuthorizationProvider interface {
    Authorize(ctx context.Context, req Request) error
}
```

That's the whole contract. Return `nil` to allow the request; return a non-nil
error describing the denial to refuse it. The error you return is the denial
reason — make it self-identifying (which subject, which action, which resource).
`Authorize` runs on the caller's goroutine, so honour `ctx` cancellation if your
decision does I/O (a policy lookup, a token check).

## Installing your provider

Pass it to the engine constructor with `thresher.WithAuthorizationProvider`:

```go
func WithAuthorizationProvider(a auth.AuthorizationProvider) Option
```

| Aspect | Behavior |
|---|---|
| Scope | per-engine — the provider the `Thresher` consults for its lifetime. |
| Nil guard | a `nil` provider is rejected with an `EmptyNotAllowed` error; the engine is not built. |
| Default | when the option is omitted, the engine installs the allow-all provider. |

## A minimal implementation

A provider that only lets a known set of subjects claim user tasks, and allows
everything else — a named type implementing the one method:

```go
type roster struct {
    allowed map[string]bool // subjects permitted to claim tasks
}

func (r roster) Authorize(_ context.Context, req auth.Request) error {
    if req.Action == auth.ActionClaimUserTask && !r.allowed[req.Subject] {
        return fmt.Errorf(
            "authorization: subject %q may not %s %q",
            req.Subject, req.Action, req.Resource)
    }
    return nil
}

// wire it into the engine
eng, _ := thresher.New("engine",
    thresher.WithAuthorizationProvider(roster{
        allowed: map[string]bool{"alice": true, "bob": true},
    }),
)
```

The type carries whatever policy state you need (here a static roster; in
practice an RBAC client or a token verifier). Decide off `req.Action` — one
provider fields every action, so branch on it and default-allow the ones you
don't gate.

## Reference implementation

The built-in default is the reference: `auth/allowall`, a stateless provider
that permits every request.

```go
func allowall.New() auth.AuthorizationProvider   // returns an allow-all provider

type Provider struct{}
func (Provider) Authorize(context.Context, auth.Request) error   // always nil
```

It is the engine's default precisely because gobpm delegates authorization to
the host by default (design: [ADR-002 — extension architecture](../../design/ADR-002-extension-architecture.md),
§4.2/§6). A closed system opts out by installing a provider that denies by
default and allows explicitly — the inverse of `allowall`.

## How the engine uses it

The provider is stored on the engine and exposed to its internals as the single
authorization slot:

1. You call `WithAuthorizationProvider(yourProvider)` at construction (or accept
   the allow-all default).
2. The `Thresher` holds it for its lifetime; every sensitive operation is meant
   to be checked by building an `auth.Request` and calling `Authorize` before
   proceeding.

> The `auth` package is the authorization **seam**: the interface, the action
> vocabulary, and the default are in place, but the engine does not yet call
> `Authorize` at the start/claim/cancel call-sites — enforcement wiring and a
> production authorization contract are owned by their own design work
> (ADR-002 §4.2/§6; the auth package doc calls this out). Install a provider now
> to be ready; treat it as the extension point, not an active gate, until those
> call-sites land.

## See also

- Related guides: [Human tasks](../operating/human-tasks.md) · [Starting instances](../operating/starting-instances.md) · [Instance lifecycle](../operating/instance-lifecycle.md)
- Design: [ADR-002 — extension architecture](../../design/ADR-002-extension-architecture.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/auth`
