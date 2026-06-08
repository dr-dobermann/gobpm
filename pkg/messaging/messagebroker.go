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
	// "no key" (a subscription with an empty key matches any key for Name).
	CorrelationKey string
}

// MessageBroker delivers incoming messages to interested subscribers,
// buffering undelivered ones in a (bounded, in the default) inbox.
type MessageBroker interface {
	// Publish submits an incoming message for delivery or buffering.
	Publish(ctx context.Context, msg Envelope) error
	// Subscribe returns a channel of envelopes matching name and
	// correlationKey (an empty correlationKey matches any key for name).
	Subscribe(ctx context.Context, name, correlationKey string) (<-chan Envelope, error)
}
