# Basic Process Example

This example demonstrates the fundamental concepts of creating a simple BPMN process using GoBPM.

## What it demonstrates

- Creating a BPM engine (Thresher)
- Building a process with Start Event → Service Task → End Event
- Creating and registering process snapshots
- Starting and executing processes

## Key concepts

- Process creation and element addition
- Sequence flow linking
- Service task with operations
- Process lifecycle management

## Running the example

```bash
go run main.go
```

## Expected output

```
Process 'simple-process' started successfully with ID: <process-id>
```

## Code walkthrough

1. **Engine Creation**: Creates a new Thresher BPM engine
2. **Process Definition**: Creates a new process with a descriptive name
3. **BPMN Elements**: Creates start event, service task, and end event
4. **Flow Connections**: Links elements using sequence flows
5. **Process Registration**: Creates snapshot and registers with engine
6. **Execution**: Starts the engine and executes the process

This example provides the foundation for understanding how to work with GoBPM and can be extended with more complex BPMN patterns.
