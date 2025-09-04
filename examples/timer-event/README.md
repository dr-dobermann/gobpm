# Timer Event Example

This example shows how to work with timer-based events in BPMN processes using GoBPM.

## What it demonstrates

- Creating timer expressions with FormalExpression
- Timer event definitions and start events
- Time-based process triggering
- Advanced BPMN event handling

## Key concepts

- Timer event definitions with timeDate
- FormalExpression using goexpr
- Timer-triggered process execution
- Complex process flows with time dependencies

## Running the example

```bash
go run main.go
```

## Expected output

```
Timer process 'timer-process' started successfully with ID: <process-id>
Timer will trigger in 3 seconds, repeating 3 times...
Press Ctrl+C to exit
```

## Code walkthrough

1. **Timer Expression**: Creates a FormalExpression for time calculations
2. **Timer Definition**: Creates TimerEventDefinition with timeDate/timeCycle/timeDuration
3. **Timer Event**: Creates a start event with timer trigger
4. **Process Flow**: Links timer event to service task to end event
5. **Engine Setup**: Registers and starts the process for timer-based execution

## Timer types supported

- **timeDate**: Specific date/time to trigger
- **timeCycle**: Recurring intervals  
- **timeDuration**: Duration from process start

This example demonstrates advanced event-driven patterns essential for time-sensitive business processes.
