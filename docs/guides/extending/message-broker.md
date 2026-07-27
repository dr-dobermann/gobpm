---
title: Custom message broker
description: Route BPMN messages through your own broker.
---

# Custom message broker

When a process **sends** a BPMN message (a Send Task, a message throw event) the
engine hands it to a *message broker*; when a process **waits** for one (a
Receive Task, a message catch event, a message start event) the engine
*subscribes* to that broker. The broker is the seam between the engine's
correlation machinery and wherever messages actually live — an in-memory inbox
by default, or your own transport.

Reach for a custom broker when messages must cross a process boundary that an
in-memory inbox can't: a durable queue that survives restarts, a Kafka/NATS/AMQP
topic shared with other services, or a broker with its own delivery and buffering
policy. This page shows the seam interface, how to install it, a minimal real
implementation, and how the engine drives it.

> The broker only carries **incoming** message *instances* (envelopes) and routes
> them to waiting subscribers. It is not the process model — message *definitions*
> and their correlation keys live in the BPMN elements; the broker just delivers
> by name and key.

## The seam interface

A broker implements `messaging.MessageBroker` — two methods:

```go
type MessageBroker interface {
    // Publish submits an incoming message for delivery or buffering.
    Publish(ctx context.Context, msg Envelope) error
    // Subscribe returns a subscription for messages named name. With no keys (or
    // only empty keys) it is a wildcard, matching any key for name; otherwise it
    // matches only a message whose CorrelationKey is in the key-set.
    Subscribe(ctx context.Context, name string, keys ...string) (Subscription, error)
}
```

| Member | You implement | Why |
|---|---|---|
| `Publish(ctx, msg)` | accept an `Envelope`, deliver it to a matching subscriber or buffer it | the send side of every message flow. |
| `Subscribe(ctx, name, keys…)` | return a live `Subscription` for `name` (wildcard when no keys) | the wait side — every message catch. |

Delivery must be **most-specific**: a keyed subscription (one whose key-set
contains the message's `CorrelationKey`) is preferred over a wildcard
subscription for the same name. That is how a follow-up message routes to the
conversation that owns it rather than to the engine-level instance-starter
(SRD-017, [ADR-016](../../design/ADR-016-message-correlation.md) §2.3). A broker
that ignores this still runs, but breaks multi-instance correlation.

### Envelope — what flows

`Publish` and each subscription channel carry an `Envelope`:

```go
type Envelope struct {
    Payload        any    // the message body, opaque to the broker
    Name           string // the message name (matches bpmncommon.Message.Name)
    CorrelationKey string // selects the target; empty means "no key" (wildcard)
}
```

The broker treats `Payload` as opaque — it routes on `Name` + `CorrelationKey`
only, and never inspects the body.

### Subscription — the wait handle

`Subscribe` returns a `Subscription`; the engine reads from it until the wait ends:

```go
type Subscription interface {
    C() <-chan Envelope     // channel of envelopes matching this subscription
    AddKey(key string) error // extend the key-set (lazy secondary-key association)
    Unsubscribe() error      // remove the subscription; idempotent
}
```

| Member | Contract |
|---|---|
| `C()` | the delivery channel — matching envelopes arrive here. |
| `AddKey(key)` | grow the correlation key-set so subsequent **and already-buffered** messages carrying `key` are delivered here; an empty key is rejected. Backs a conversation learning a new key at runtime (SRD-017). |
| `Unsubscribe()` | detach; no further message is routed here. A stopped subscriber **must** call it — a buffered channel otherwise keeps claiming matches and silently swallows them away from live subscribers. Idempotent: a second call (or one after the broker already dropped it) is a no-op. |

## Installing your broker

Pass it to the engine with the `thresher` option:

```go
func WithMessageBroker(b messaging.MessageBroker) Option
```

| Aspect | Behavior |
|---|---|
| Default | the in-memory inbox (`membroker`) when the option is omitted. |
| Nil guard | a `nil` broker is rejected — `WithMessageBroker: a nil MessageBroker isn't allowed` (`EmptyNotAllowed`); the default stays in place. |
| Scope | per-engine — the broker is read off the engine runtime by every send/receive. |

```go
eng, err := thresher.New("engine", thresher.WithMessageBroker(myBroker))
```

## A minimal implementation

A wildcard-only broker over a plain Go channel — enough for a single-conversation
process where correlation keys don't matter. It shows the two methods and the
subscription handle; a production broker adds keyed routing and buffering.

```go
type chanBroker struct {
    mu   sync.Mutex
    subs map[string][]*chanSub // by message name
}

func (b *chanBroker) Publish(ctx context.Context, msg messaging.Envelope) error {
    b.mu.Lock()
    defer b.mu.Unlock()
    for _, s := range b.subs[msg.Name] {
        select {
        case s.ch <- msg:
        default: // subscriber slow — a real broker would buffer here
        }
    }
    return nil
}

func (b *chanBroker) Subscribe(
    ctx context.Context, name string, keys ...string,
) (messaging.Subscription, error) {
    b.mu.Lock()
    defer b.mu.Unlock()
    s := &chanSub{broker: b, name: name, ch: make(chan messaging.Envelope, 8)}
    b.subs[name] = append(b.subs[name], s)
    return s, nil
}

type chanSub struct {
    broker *chanBroker
    name   string
    ch     chan messaging.Envelope
}

func (s *chanSub) C() <-chan messaging.Envelope { return s.ch }
func (s *chanSub) AddKey(key string) error      { return nil } // no keyed routing
func (s *chanSub) Unsubscribe() error { /* remove s from broker.subs[s.name] */ return nil }
```

> This example is deliberately wildcard-only: it delivers every message of a
> given name to every subscriber. Correct multi-instance correlation needs the
> most-specific rule above (prefer a keyed subscription over a wildcard) plus
> `AddKey` honoured — study `membroker` before shipping a real transport.

## Reference implementation

`membroker` is the built-in default and the reference to read:

- `membroker.New(opts ...Option) *Broker` — an in-memory inbox + correlation
  router.
- Undelivered envelopes buffer in a **bounded** inbox (`DefaultMaxInbox = 1024`)
  that drops the oldest and warns once past the cap, so uncorrelated messages
  can't grow unbounded (ADR-002 §4.2). Tune with `WithMaxInbox(n)` (`n <= 0`
  disables the cap); silence its logger with `WithLogger(l)`.
- Delivery is most-specific, and `AddKey` grows a subscription's key-set at
  runtime for lazy secondary-key association.

It is the model for your own: implement the two `MessageBroker` methods, honour
the most-specific delivery rule, and bound any buffering.

## How the engine uses it

The engine reaches the broker off its runtime — you never call `Publish` or
`Subscribe` yourself:

1. **Send.** A Send Task / message throw resolves the message name and
   correlation key, then calls `MessageBroker().Publish(ctx, Envelope{…})`. A
   broker error fails the send (`msgflow.Send: broker rejected message …`).
2. **Wait.** A Receive Task / message catch arms a waiter that calls
   `MessageBroker().Subscribe(ctx, name, keys…)` and parks on `sub.C()`. A
   matching envelope wakes the waiter and drives the catch.
3. **Correlation.** When a conversation learns a secondary key mid-flight, the
   waiter calls `AddKey` so later messages under that key reach the same
   instance; when the wait ends, it calls `Unsubscribe`.

So a single `WithMessageBroker` re-points every message flow in every process the
engine runs. Everything above the broker — which node sends, which waits, how
keys are derived — stays in the model; the broker only decides how an envelope
travels from a `Publish` to a subscriber's channel.

## See also

- Related guides: [Send / Receive Task](../tasks/send-receive-task.md) · [Message event](../events/message.md) · [Correlation & conversations](../operating/correlation.md) · [How events are processed](../concepts/event-processing.md)
- Design: [ADR-016 — Message correlation](../../design/ADR-016-message-correlation.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/messaging`
