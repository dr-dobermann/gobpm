# **Analysis of the gobpm project (dr-dobermann/gobpm)**

| Property | Value |
| :---- | :---- |
| **Author** | dr-dobermann |
| **Status** | Proposed |
| **Version** | 1.18 |
| **Date** | 2026-03-29 |

An analysis of the gobpm open-source project through the lens of modern BPM system architectural standards. The project is a native BPMN 2.0 engine implementation in the Go programming language.

*(Note: The development plan and Roadmap are moved to a separate document: gobpm\_roadmap.md).*

## **1\. Strengths and Architectural Fit**

* **Tech Stack (Go):** The choice of language is fully justified. The engine gains from being lightweight, having fast compilation, and native support for goroutines for the parallel execution of multiple process instances. This allows for efficient CPU and RAM utilization in high-load environments, ensuring high instance density per node.  
* **"Library, Not a Framework" Paradigm:** gobpm is embedded directly into the binary of the user's Go application. This distinguishes it from heavy Java-based systems (which require JVM/Tomcat/Spring deployments) and is highly sought after in microservice architectures where startup speed and minimal footprint are critical.  
* **Core Module Isolation:** Proper separation of logic into isolated packages:  
  * pkg/model — programmatic creation of the process model (BPMN foundation). The module intentionally avoids XML parsing to "decouple" model creation from its loading, isolating the business model from serialization details.  
  * pkg/expression — an interface and implementation of the **Formal Expression** specification for calculating conditions and dynamic attributes.  
  * pkg/thresher — the runtime environment (Runtime/Executor) that manages the token lifecycle.  
  * *Benefit:* This allows the model package to be used even by teams that only need to programmatically construct or analyze process models without being tied to XML parsers or the runtime itself.  
* **Event-Driven Execution Model:** The use of an internal EventHub and concurrent node processing provides a solid foundation for high reactivity and performance under load.

## **2\. Target Vision and Conceptual Priorities**

Unlike the growing trend of moving away from heavy XML toward lightweight JSON DSLs, the strategic priority for gobpm in its first phase is **strict Process Execution Conformance to the BPMN v2 specification**.

* **Rationale:** This is a conscious architectural decision. It allows gobpm to become a drop-in replacement for existing Enterprise solutions (e.g., Camunda). Business analysts can continue using familiar modeling tools, while developers get a lightweight Go engine for execution.  
* **Separation of Library and Runtime:** A fundamental concept of the project is the strict separation of pure execution logic (the library) and the execution environment (the thresher runtime). The library is responsible solely for BPMN semantics, having no rigid binding to specific databases, message brokers, or network topologies.

## **3\. Concurrency and Scalability Analysis**

Handling parallel branches (Parallel Gateways) and scaling process instances is one of the most complex aspects of BPM engine development. In gobpm, this problem must be solved considering the ideology: **The library does not dictate the architecture; it only provides contracts.**

### **3.1. Problem Statement: The Distributed Token Movement Anti-pattern**

Distributing the token movement of a single Process Instance across different physical nodes is considered an architectural anti-pattern for several reasons:

1. **Network Overhead (Latency vs. CPU):** A token transition between BPMN nodes in RAM takes nanoseconds. Distributing branches (e.g., sending an event to a data bus for execution on another node) incurs massive costs for context serialization and network transmission, nullifying the benefits of parallelism at the process level.  
2. **Concurrent Variable Access (Race Conditions):** Parallel branches often read and write shared process variables. Simultaneous access from different nodes leads to intense competition for DB rows (VersionMismatch), requiring constant retries and degrading database performance.  
3. **The Synchronizing Gateway Problem (AND-join Nightmare):** Merging parallel branches requires an atomic check of the token counter. Without shared local memory, this forces the use of heavy distributed locks.

In the modern industry (e.g., Cloud-Native orchestrator architectures), this is solved by the **Instance Affinity** mechanism: the entire lifecycle of a specific process instance is handled strictly by one pinned node. Scalability is achieved by distributing *different* instances across different cluster servers.

### **3.2. Requirements for a Hybrid Execution Strategy**

The architecture should not dictate the choice of a specific technology but must meet the following requirements:

* **State Atomicity:** The system must ensure the consistency of the token graph under concurrent changes, using version control mechanisms in the Persistence layer.  
* **Latency Minimization:** The runtime must ensure the execution of transactional transitions between nodes without inter-server calls within a single instance.  
* **Payload Decoupling (External Workers):** The system must support offloading "heavy" business logic (Service Tasks) to external nodes. The orchestrator must be able to publish tasks and asynchronously receive results without blocking resources for managing other instances.

## **4\. Observability Architecture: Audit and Monitoring**

According to the BPMN 2.0 specification and CQRS principles, Audit and Monitoring in the engine are distinct entities with different consumers. Audit provides a "look into the past" for compliance, while Monitoring provides a "look at the present" for operational management.

### **4.1. Audit Subsystem (Audit Trail & History)**

* **What it is responsible for:** An immutable system chronicle (Append-only log). "What happened, when, who initiated it, and based on what data?". This is the foundation for legal and business transparency.  
* **Business Tasks:** Compliance control, incident investigation, and 100% reliable reconstruction of the process context at any point in the past based solely on the event stream.  
* **Implementation Requirements:**  
  * **Event Persistence:** Every significant change (e.g., VariableUpdated, TaskClaimed) must be asynchronously or synchronously recorded in permanent storage.  
  * **History Isolation:** Audit data requires eternal storage and should be movable to "cold" storage without affecting runtime performance.  
  * **Structured Access:** The log must allow searching by instance IDs, users, and time ranges.

### **4.2. Monitoring Subsystem (BAM & Performance Metrics)**

* **What it is responsible for:** Real-time aggregated analytics. "How well is the system working right now, are there any failures, and are KPIs being met?". Unlike audit, monitoring operates on histograms, counters, and rates rather than individual events.  
* **Business Tasks:** Business Activity Monitoring (BAM), process optimization, bottleneck detection, SLA compliance monitoring, and operational alerting.  
* **Classification of Required Metrics (Requirements):**  
  1. **Workflow Metrics (KPIs):**  
     * *Cycle Time:* Full duration from start to finish (per process version).  
     * *Lead Time:* Time tasks spend in queues (especially for User Tasks).  
     * *Throughput:* Number of started/completed instances per unit of time.  
  2. **Reliability Metrics:**  
     * *Incident Rate:* Ratio of successful completions to technical incidents.  
     * *Job Execution Latency:* Delay between scheduled and actual firing time for timers.  
  3. **Internal Resource Metrics:**  
     * *Active Goroutines:* Number of active tokens processed by the engine.  
     * *EventBus Saturation:* The depth of the internal event hub queue.  
* **Technical Implementation Requirements:**  
  * **Multi-dimensionality (Labels/Tags):** Every metric must include mandatory dimensions: tenant\_id, process\_definition\_id, version.  
  * **Low Overhead:** Metric collection must not introduce more than a 1% delay in token transition transactions.  
  * **Exportability:** The architecture must provide a simple mechanism for delivering aggregates to external Time-series providers (e.g., Prometheus).

### **4.3. Technical Observability (Tracing & Logging)**

* **Analysis:** It is vital to clearly distinguish business observability (BPMN Audit) from technical observability (System Observability). Infrastructure errors should not be mixed with the business process history.  
* **Implementation Requirements:**  
  * **Distributed Tracing:** Integration with distributed tracing specifications (e.g., **OpenTelemetry**). Passing TraceIDs to external workers through the execution context to link a process step with external service logs.  
  * **System Logging:** Utilizing standard Go logging mechanisms for recording technical failures (DB errors, OOM, network timeouts) that carry no business meaning.

## **5\. Business Error Management (BPMN Error Handling)**

In the gobpm architecture, a rigid boundary is drawn between **Technical Incidents** (system failures) and **Business Errors** (legitimate process deviations envisioned by the analyst).

### **5.1. Analysis and Classification**

* **Technical Incident:** For example, a 503 error from an external database. Requires creating an Incident, stopping the token, and waiting for an administrator to perform a retry.  
* **Business Error (BPMN Error):** For example, "Insufficient funds". This is a legitimate path that must be caught by the process to transition to an alternative branch.

### **5.2. Requirements for the Handling Mechanism**

* **Typing:** The ability for an executor (worker) to pass a structured error message with a unique code and payload (variables) to the core.  
* **Hierarchical Resolution:** The engine must implement a search for a handler (Boundary Error Event) from the bottom up through the Scope hierarchy (from task to sub-process and above).  
* **Interrupting Semantics:** Upon triggering a handler, the system must guaranteed to terminate all active parallel tokens within the corresponding scope.  
* **Fail-safe for Unhandled Errors:** Converting an instance to an Incident status if a business error finds no matching catch event all the way to the process root.

## **6\. Enterprise-grade Architectural Challenges (Day-2 Operations)**

Production operation imposes several requirements for "blind spots" critical for long-running processes.

### **6.1. Asynchronous Interaction (Message Correlation)**

* **What it is responsible for:** Matching an incoming external signal (message) with a specific waiting process instance.  
* **Implementation Requirements:**  
  * **Business Correlation:** Routing messages based on a set of unique keys (Correlation Keys), e.g., order\_id \+ client\_type.  
  * **Subscription Persistence:** Event subscriptions must be stored in permanent storage so no signal is lost during orchestrator node restarts.

### **6.2. Time Management (Timer Events)**

* **Analysis:** Processes can "sleep" for weeks. Using in-memory timers is unacceptable as state must persist across application restarts or infrastructure failures.  
* **Requirements:**  
  * **Schedule Persistence:** Information about all active timers must be part of the saved instance state in the DB.  
  * **Hydration on Load:** The engine must restore (re-schedule) active timers when loading an instance into memory.  
  * **Reactive Updates:** Dynamically changing the active timer list during any graph mutation (exiting a Scope, loops).  
  * **Missed Fire Handling:** Logic for processing events whose fire time occurred while the system was down.

### **6.3. State Management and Variable Hierarchy (Scope)**

* **Analysis:** In BPMN, sub-processes and multi-instances have their own visibility scopes. The current system implementation in **gobpm/internal/scope** must fully support this hierarchy.  
* **Requirements for Development:**  
  * **Shadowing & Propagation:** The mechanism must distinguish between local writes (for technical iterators) and upward writes (to update business data at its original declaration site).  
  * **Thread-safety:** Ensuring safe concurrent access for parallel instance goroutines to shared parent nodes in the Scope hierarchy.  
  * **Integration with Formal Expression:** Transparent support for hierarchical variable searches by the expression engine without needing explicit nesting paths in business logic.

### **6.4. Platform-Agnostic Form Registry**

* **Analysis:** UI rendering is intentionally omitted from the standard. The engine should not limit usage to specific environments (Web, Mobile, CLI).  
* **Implementation Requirements:**  
  * **Schema Abstraction:** Storing and serving universal data description formats (e.g., JSON Schema) linked to a formKey.  
  * **Rendering Decoupling:** Shifting visualization responsibility to the host application while maintaining the core's role as a metadata provider.

### **6.5. Fault Tolerance and Recovery**

* **Implementation Requirements:**  
  * **Retries:** Configurable retry strategies for system calls.  
  * **Incident Mechanism:** Locking the problematic part of the process ("Dead Letter Queue") while preserving diagnostic data for manual administrator review.

### **6.6. Support for Unstructured Processes (Ad-Hoc & CMMN)**

* **Analysis:** Real life often requires flexibility in early stages. Support for Ad-Hoc processes is seen as a "crystallization" phase, allowing a rigid regulation to form over time based on experience.  
* **Architectural Requirements:**  
  * **Runtime Flexibility:** The core must allow for the introduction of dynamic sub-processes where the next step is chosen by the user.  
  * **Evolutionary Path:** Support for Case Management (CMMN) concepts is considered a promising development stage after stabilizing the basic BPMN 2.0 functionality.

# **Changes**

### **2026-03-29**

* Fully translated to English (v1.18).  
* Added detailed analysis and requirements for the metrics subsystem (Section 4.2).  
* Restored full analytical depth across all sections (Audit, Monitoring, Scope, and Errors).  
* Refined gobpm/internal/scope analysis with shadowing and thread-safety requirements.  
* Formalized "crystallization" concept for Ad-hoc processes.

### **2026-03-28**

* Initial project analysis version.