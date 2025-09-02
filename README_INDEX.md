# GoBPM Documentation Index

This file provides an organized index to all documentation throughout the GoBPM project. Documentation is located alongside the code it describes, following the principle of keeping documentation close to implementation.

## 📋 Quick Links

- **[Main README](README.md)**: Project overview and getting started
- **[Contributing Guidelines](CONTRIBUTING.md)**: How to contribute to the project
- **[Changelog](CHANGELOG.md)**: Project history and release notes

## 🏗️ Core Architecture

### Process Engine
- **[Thresher](pkg/thresher/)**: Main BPM engine and process orchestrator
  - State management and lifecycle control
  - Process instance execution
  - Event-driven process flow
  - *Coverage: 68.8% - Well tested*

### Event Processing System
- **[Event Processing](internal/eventproc/README.md)**: Core event processing interfaces and patterns
  - EventProducer, EventProcessor, EventWaiter interfaces
  - Event-driven communication patterns
  - Asynchronous processing architecture

- **[EventHub](internal/eventproc/eventhub/README.md)**: Central event distribution system
  - Event registration and propagation
  - Event waiter management
  - Timer and message event support
  - *Coverage: 77.6% - Comprehensive testing*

### Runtime Environment
- **[Instance Management](internal/instance/)**: Process instance lifecycle
  - Instance creation and execution
  - Snapshot management for process state
  - Token tracking and node execution

- **[Event Processors](internal/eventproc/)**: Event handling components
  - Process-level event processing
  - Event correlation and routing

## 📊 Model Layer

### BPMN Model Implementation
- **[Process Model](pkg/model/process/)**: BPMN Process implementation
  - Process definition and structure
  - Node and flow management

- **[Activities](pkg/model/activities/)**: BPMN Activity implementations
  - Service Tasks, User Tasks, Script Tasks
  - Task lifecycle and execution

- **[Events](pkg/model/events/)**: BPMN Event implementations
  - Start, Intermediate, End events
  - Timer, Message, Signal events

- **[Gateways](pkg/model/gateways/)**: BPMN Gateway implementations
  - Exclusive, Inclusive, Parallel gateways
  - Decision point logic

- **[Flow Elements](pkg/model/flow/)**: BPMN Flow implementation
  - Sequence flows and control flow
  - Node connections and routing

- **[Data Model](pkg/model/data/)**: BPMN Data handling
  - Data objects and variables
  - Expression evaluation
  - Data transformation

- **[Artifacts](pkg/model/artifacts/)**: BPMN Artifacts support
  - Categories and annotations
  - Visual grouping elements
  - *Coverage: 86.2% - Excellent testing*

### Foundation Components
- **[Foundation](pkg/model/foundation/)**: Base types and utilities
  - ID generation and management
  - Base element implementations
  - Common interfaces

- **[Common Elements](pkg/model/common/)**: Shared BPMN components
  - Messages, errors, signals
  - Resource definitions

## 🔧 Utilities and Support

### Error Handling
- **[Error System](pkg/errs/)**: Centralized error handling
  - Structured error creation
  - Error classification and context
  - Error chaining and wrapping

### Monitoring and Observability
- **[Monitor](pkg/monitor/)**: Process monitoring and logging
  - Execution tracking
  - Performance metrics
  - Debug information

### Data Structures
- **[Set Utilities](pkg/set/)**: Set data structure implementation
  - Type-safe set operations
  - Collection utilities

## 🧪 Testing Strategy

### Test Coverage Overview
- **High Coverage (>75%)**:
  - EventHub: 77.6%
  - Artifacts: 86.2%
  - Thresher: 68.8%
  - Set utilities: 100%
  - Many model components: 80-100%

- **Medium Coverage (50-75%)**:
  - Instance management: 58%
  - Process model: 68%
  - Flow elements: 63%

- **Areas for Improvement**:
  - Data model components
  - Some gateway implementations
  - Integration scenarios

### Testing Principles
1. **Modular Test Structure**: Tests organized by functionality
2. **Comprehensive Error Testing**: All error paths covered
3. **Concurrency Testing**: Thread safety validation
4. **Integration Testing**: Component interaction verification
5. **Performance Testing**: Scalability and performance validation

## 📦 Package Organization

### Internal Packages (`internal/`)
Private implementation details, not exposed to external users:
- **eventproc/**: Event processing implementation
- **instance/**: Process instance management
- **exec/**: Execution engine components
- **runner/**: Process runner implementations
- **scope/**: Data scope management
- **interactor/**: Human interaction handling
- **renv/**: Runtime environment interfaces

### Public Packages (`pkg/`)
Public API exposed to users of the library:
- **model/**: BPMN model implementations
- **errs/**: Error handling utilities
- **monitor/**: Monitoring and observability
- **thresher/**: Main BPM engine
- **set/**: Data structure utilities

## 🔄 Development Workflow

### Documentation Standards
1. **Co-location**: Documentation lives with the code it describes
2. **Markdown Format**: All documentation in markdown
3. **Code Examples**: Practical usage examples in all docs
4. **API Reference**: Complete API documentation
5. **Architecture Diagrams**: Visual representations where helpful

### Documentation Requirements
- **New Features**: Must include comprehensive documentation
- **API Changes**: Update documentation before merging
- **Examples**: Include practical usage examples
- **Testing**: Document testing strategies and coverage

### Maintenance Guidelines
- **Regular Updates**: Keep documentation current with code changes
- **Link Verification**: Ensure all internal links work
- **Coverage Tracking**: Monitor and improve documentation coverage
- **User Feedback**: Incorporate feedback to improve clarity

## 🚀 Getting Started

### For New Developers
1. Start with the [Main README](README.md) for project overview
2. Read [Contributing Guidelines](CONTRIBUTING.md) for development setup
3. Explore [Event Processing](internal/eventproc/README.md) for architecture understanding
4. Check [EventHub](internal/eventproc/eventhub/README.md) for practical examples

### For Users
1. Review [Thresher](pkg/thresher/) for main engine usage
2. Explore [Model packages](pkg/model/) for BPMN implementation details
3. Check [Error Handling](pkg/errs/) for proper error management
4. See examples in individual package documentation

### For Contributors
1. Follow existing documentation patterns
2. Add tests with >75% coverage
3. Update relevant documentation
4. Include practical examples
5. Follow error handling patterns

## 📞 Support and Community

- **Issues**: Report bugs and feature requests on GitHub
- **Discussions**: Architecture and design discussions
- **Documentation**: Improvements and clarifications welcome
- **Testing**: Help improve test coverage and quality

---

*This index is maintained alongside the codebase. When adding new packages or significant features, please update this file to ensure discoverability.*
