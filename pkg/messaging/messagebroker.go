// Package messaging defines the engine's message-delivery extensions. This file
// holds the MessageBroker contract (incoming-message inbox + correlation
// routing); the EventHub interface joins this package in M3. The in-memory
// MessageBroker default lives in the membroker sibling subpackage. The
// production correlation contract is owned by a dedicated messaging adapter ADR
// (ADR-001 v.4 §9); this is the minimal skeleton contract.
package messaging

import "context"

// Envelope is an incoming message instance awaiting correlation/delivery.
type Envelope struct {
	// Payload is the message body, opaque to the broker.
	Payload any
	// Name is the message name (matches bpmncommon.Message.Name).
	Name string
	// CorrelationKey selects the target instance/subscription; empty means
	// "no key" (a wildcard subscription matches any key for Name).
	CorrelationKey string
}

// Subscription is a live subscription handle returned by MessageBroker.Subscribe.
type Subscription interface {
	// C is the channel of envelopes matching the subscription.
	C() <-chan Envelope
	// AddKey extends the subscription's correlation key-set so that subsequent
	// (and already-buffered) messages carrying key are delivered here. It backs
	// lazy secondary-key association (SRD-017): a conversation that learns a new
	// key becomes reachable by it. An empty key is rejected.
	AddKey(key string) error
	// Unsubscribe removes the subscription from the broker; no further published
	// message is routed to it. A stopped subscriber MUST Unsubscribe, otherwise
	// its (buffered) channel keeps claiming matching messages and silently
	// swallows them away from live subscribers. Idempotent: a second call, or one
	// after the broker already dropped the subscription, is a no-op.
	Unsubscribe() error
}

// MessageBroker delivers incoming messages to interested subscribers, buffering
// undelivered ones in a (bounded, in the default) inbox. Delivery is
// most-specific: a keyed subscription (one whose key-set contains the message's
// correlation key) is preferred over a wildcard subscription (SRD-017,
// ADR-016 §2.3).
type MessageBroker interface {
	// Publish submits an incoming message for delivery or buffering.
	Publish(ctx context.Context, msg Envelope) error
	// Subscribe returns a subscription for messages named name. With no keys (or
	// only empty keys) it is a wildcard, matching any key for name; otherwise it
	// matches only a message whose CorrelationKey is in the key-set.
	Subscribe(ctx context.Context, name string, keys ...string) (Subscription, error)
}
