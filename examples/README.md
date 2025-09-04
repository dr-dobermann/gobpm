# GoBPM Examples

This directory contains working examples demonstrating how to use the GoBPM library.

## 📋 Available Examples

### Basic Process (`basic-process/`)
Demonstrates the fundamental concepts of creating a simple BPMN process:
- Creating a BPM engine (Thresher)
- Building a process with Start Event → Service Task → End Event
- Creating and registering process snapshots
- Starting and executing processes

**Key concepts:**
- Process creation and element addition
- Sequence flow linking
- Service task with operations
- Process lifecycle management

```bash
cd basic-process && go run main.go
```

### Timer Events (`timer-event/`)
Shows how to work with timer-based events in BPMN processes:
- Creating timer expressions with FormalExpression
- Timer event definitions and start events
- Time-based process triggering

**Key concepts:**
- Timer event definitions with timeDate
- FormalExpression using goexpr
- Timer-triggered process execution

```bash
cd timer-event && go run main.go
```

### Simple Timer (`simple-timer/`)
A minimal example focusing on timer functionality:
- Simplified timer setup
- Basic timer start event
- Minimal process structure

**Key concepts:**
- Minimal timer implementation
- Timer start events
- Basic process flow

```bash
cd simple-timer && go run main.go
```

## 🚀 Running Examples

### Prerequisites
1. Go 1.21+ installed
2. GoBPM dependencies available (run from project root)

### Build and Run
```bash
# From examples directory
cd examples

# Run basic process example
cd basic-process && go run main.go && cd ..

# Run timer event example  
cd timer-event && go run main.go && cd ..

# Run simple timer example
cd simple-timer && go run main.go && cd ..
```

### Build All Examples
```bash
# From examples directory
cd examples

# Build each example
cd basic-process && go build -o basic-process main.go && cd ..
cd timer-event && go build -o timer-event main.go && cd ..
cd simple-timer && go build -o simple-timer main.go && cd ..
```

## 📚 Example Breakdown

### Common Patterns

All examples follow these patterns:

```go
// 1. Create BPM engine
engine := thresher.New()

// 2. Create process
proc, err := process.New("process-name")

// 3. Create BPMN elements (events, tasks, gateways)
startEvent, err := events.NewStartEvent("start")
endEvent, err := events.NewEndEvent("end")

// 4. Add elements to process
proc.Add(startEvent)
proc.Add(endEvent)

// 5. Link elements with sequence flows
flow.Link(startEvent, endEvent)

// 6. Create process snapshot
snap, err := snapshot.New(proc)

// 7. Register process with engine
engine.RegisterProcess(snap)

// 8. Start engine
ctx := context.Background()
engine.Run(ctx)

// 9. Execute process
engine.StartProcess(snap.ProcessId)
```

### Error Handling

Examples demonstrate proper error handling:
- Checking all function returns for errors
- Using `log.Fatal()` for critical errors
- Graceful error messages

### Resource Management

Examples show proper resource management:
- Context usage for cancellation
- Proper process lifecycle
- Clean shutdown patterns

## 🔧 Troubleshooting

### Common Issues

1. **Import Errors**
   ```
   cannot find package "github.com/dr-dobermann/gobpm/..."
   ```
   **Solution**: Run examples from project root or ensure proper Go module setup

2. **Build Errors**
   ```
   undefined: events.NewStartEvent
   ```
   **Solution**: Check import statements match exact package names

3. **Runtime Errors**
   ```
   Failed to start engine: thresher isn't started
   ```
   **Solution**: Ensure proper engine startup sequence (Run before StartProcess)

### Getting Help

- Check [main documentation](../README.md)
- Review [component docs](../README_INDEX.md)  
- Examine test files for additional usage patterns
- See [EventHub documentation](../internal/eventproc/eventhub/README.md) for event handling

## 🧪 Testing Examples

You can verify examples work correctly:

```bash
# Test compilation of all examples
cd examples
for dir in */; do
    if [ -f "$dir/main.go" ]; then
        echo "Testing $dir..."
        cd "$dir" && go build main.go && echo "✅ $dir compiled successfully" || echo "❌ $dir failed"
        rm -f main 2>/dev/null
        cd ..
    fi
done
echo "✅ All examples tested"
```

## 📈 Next Steps

After running these examples, explore:

1. **[Process Documentation](../pkg/model/process/)** - Advanced process features
2. **[Activity Types](../pkg/model/activities/)** - Different task types
3. **[Event Types](../pkg/model/events/)** - Various BPMN events
4. **[Data Handling](../pkg/model/data/)** - Process variables and expressions
5. **[Testing Patterns](../pkg/thresher/)** - How to test your processes

## 🤝 Contributing Examples

To add new examples:

1. Create a new `.go` file with descriptive name
2. Follow existing pattern and error handling
3. Add documentation section to this README
4. Test compilation and execution
5. Submit PR with working example

Good example topics:
- Gateway usage (exclusive, parallel)
- Data flow and variables
- Error handling and boundary events
- Sub-processes
- Message events
- Integration patterns
