# Event Processing Package

The `eventproc` package provides the core interfaces and abstractions for event-driven processing in the GoBPM engine.

## Overview

This package defines the fundamental interfaces that enable event-driven communication between different components of the BPM engine. It follows a producer-consumer pattern where events are propagated from producers to registered processors.

## Core Interfaces

### EventProcessor

The `EventProcessor` interface represents a component that can handle events.

```go
type EventProcessor interface {
    foundation.Identifyer
    ProcessEvent(context.Context, flow.EventDefinition) error
}
```

**Responsibilities:**
- Process incoming event definitions
- Maintain unique identification
- Handle events asynchronously and safely

**Implementation Examples:**
- Process instances waiting for specific events
- Boundary event handlers
- Message correlation processors

### EventProducer

The `EventProducer` interface represents a component that can distribute events to registered processors.

```go
type EventProducer interface {
    RegisterEvent(EventProcessor, flow.EventDefinition) error
    UnregisterEvent(ep EventProcessor, eDefId string) error
    PropagateEvent(context.Context, flow.EventDefinition) error
}
```

**Responsibilities:**
- Maintain registry of event processors and their subscriptions
- Route events to appropriate processors
- Support dynamic registration/unregistration

**Implementation Examples:**
- EventHub (see [eventhub documentation](eventhub/README.md))
- Thresher main engine
- External event gateways

### EventWaiter

The `EventWaiter` interface represents a component that actively waits for specific events to occur.

```go
type EventWaiter interface {
    foundation.Identifyer
    EventDefinition() flow.EventDefinition
    EventProcessor() EventProcessor
    Service(ctx context.Context) error
    Stop() error
    State() EventWaiterState
}
```

**Responsibilities:**
- Monitor for specific event conditions
- Notify event processors when events occur
- Manage waiter lifecycle and state
- Support graceful shutdown

**Implementation Examples:**
- Timer waiters for time-based events
- Message waiters for external messages
- Signal waiters for process signals

## Event Waiter States

Event waiters follow a defined state machine:

```
Created → Ready → Runned → [Ended|Stopped|Cancelled|Failed]
```

- **Created**: Initial state after construction
- **Ready**: Configured and ready to start waiting
- **Runned**: Actively waiting for events
- **Ended**: Completed normally
- **Stopped**: Stopped by explicit request
- **Cancelled**: Cancelled due to context cancellation
- **Failed**: Failed due to an error

## Architecture Patterns

### Producer-Consumer Pattern

The event processing system follows a classic producer-consumer pattern:

1. **Producers** generate events when specific conditions are met
2. **Consumers** (EventProcessors) register interest in specific event types
3. **Brokers** (EventProducers) route events from producers to interested consumers

### Event-Driven Communication

Components communicate through events rather than direct method calls:

- **Loose Coupling**: Components don't need direct references to each other
- **Scalability**: Multiple processors can handle the same event type
- **Reliability**: Event processing can be made fault-tolerant

### Asynchronous Processing

All event processing is designed to be asynchronous:

- Event propagation doesn't block the producer
- Event processing happens in separate goroutines
- Context cancellation provides clean shutdown

## Usage Examples

### Basic Event Registration

```go
// Create event producer (e.g., EventHub)
producer := eventhub.New()

// Create event processor
processor := &MyEventProcessor{id: "proc-1"}

// Create event definition
eventDef := events.NewMessageEventDefinition(message, nil)

// Register processor for this event type
err := producer.RegisterEvent(processor, eventDef)
if err != nil {
    log.Fatal("Failed to register event:", err)
}

// Start the producer
ctx := context.Background()
go producer.Run(ctx)

// Propagate an event
err = producer.PropagateEvent(ctx, eventDef)
if err != nil {
    log.Error("Failed to propagate event:", err)
}
```

### Event Waiter Implementation

```go
type MyEventWaiter struct {
    id        string
    eventDef  flow.EventDefinition
    processor EventProcessor
    state     EventWaiterState
    stopCh    chan struct{}
}

func (w *MyEventWaiter) Service(ctx context.Context) error {
    w.state = WSRunned
    
    // Wait for event conditions
    for {
        select {
        case <-ctx.Done():
            w.state = WSCancelled
            return ctx.Err()
        case <-w.stopCh:
            w.state = WSStopped
            return nil
        default:
            // Check for event conditions
            if w.checkEventCondition() {
                // Notify processor
                err := w.processor.ProcessEvent(ctx, w.eventDef)
                if err != nil {
                    w.state = WSFailed
                    return err
                }
                w.state = WSEnded
                return nil
            }
            time.Sleep(100 * time.Millisecond)
        }
    }
}
```

## Testing

The package includes comprehensive tests with mocked implementations:

- **Interface Compliance**: Verify implementations satisfy interfaces
- **Event Flow**: Test complete event propagation cycles
- **Error Handling**: Validate error conditions and recovery
- **Concurrency**: Test thread-safety and concurrent access

See the test files for detailed examples of proper usage and testing patterns.

## Related Packages

- **[eventhub](eventhub/README.md)**: Concrete implementation of EventProducer
- **[waiters](eventhub/waiters/)**: Implementations of EventWaiter for different event types
- **[flow](../../pkg/model/flow/)**: Event definition types and interfaces
- **[thresher](../../pkg/thresher/)**: Main BPM engine that uses event processing

## Performance Considerations

- **Goroutine Management**: Each event waiter typically runs in its own goroutine
- **Memory Usage**: Large numbers of event registrations consume memory
- **Event Queue**: Consider queue size limits for high-volume scenarios
- **Context Cancellation**: Always use context for clean shutdown

## Best Practices

1. **Always use context**: Pass context for cancellation support
2. **Handle errors gracefully**: Event processing failures should not crash the system
3. **Resource cleanup**: Ensure waiters are properly stopped to prevent leaks
4. **Thread safety**: All implementations must be thread-safe
5. **Unique IDs**: Use unique identifiers for all processors and waiters
