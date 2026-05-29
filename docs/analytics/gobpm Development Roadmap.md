# **gobpm Development Plan and Roadmap**

| Property | Value |
| :---- | :---- |
| **Author** | dr-dobermann |
| **Status** | Living |
| **Version** | 2.0 |
| **Date** | 2026-05-29 |

This document describes the development strategy, current architectural challenges, and a phased implementation plan (Roadmap) for the gobpm BPMN 2.0 engine, adapted for Enterprise-grade requirements.

This Roadmap is **subordinate** to [SAD-001 Vision & Architecture](../design/SAD-001-vision-and-architecture.md). Where SAD-001 establishes architectural principles (multi-module monorepo; library vs runtime split; extension model; execution model), this Roadmap sequences the BPMN element implementation work that delivers on those principles. Conformance scope is defined in [docs/bpmn-spec/conformance.md](../bpmn-spec/conformance.md): BPMN 2.0 Process Execution Conformance (Common Executable Subclass §2.1.3) plus the ComplexGateway extension.

## **1\. Development Strategy (Extensibility Architecture)**

To ensure the successful evolution of the project and provide maximum Developer Experience (DX), the following key vectors are defined, considering Triple Crown and Cloud-Native concepts:

### **1.1. Maximum Extensibility via Interfaces**

The library provides strict interfaces (Ports) for any platform-dependent components. The full catalogue is elaborated in SAD-001 §11 Extension Model.

* **Infrastructure:** Event queues, databases (Persistence), and distributed lock mechanisms are injected strictly through interfaces. The Persistence layer supports the **Event Sourcing** pattern, providing a reliable Audit Trail.  
* **Expressions:** Support for the **Formal Expression** specification is implemented via an abstract interface, allowing the connection of any calculation engine (GEP, FEEL, JUEL) without changing the runtime logic.
* **Security (AuthN / AuthZ):** Per SAD-001 §12, AuthN is a pure runtime concern. AuthZ has hook points in core (the `AuthorizationProvider` interface) with policy implementation in runtime / adapters.
* **Observability:** `Logger`, `Tracer`, `MetricsRecorder` interfaces. Default policy is **visible-by-default, silenceable on opt-out** — `Logger` defaults to `slog.Default()` so production deployments don't silently lose telemetry.

### **1.2. "Batteries Included" Strategy**

The library is shipped with ready-to-use lightweight implementations: in-memory storage, SQLite for persistence, and local Go channels for queues. This ensures a low entry barrier and rapid prototyping.

### **1.3. Runtime Decoupling (Thresher)**

The thresher runtime is designed based on the Interface Segregation principle. Decoupling the interface and implementation allows users to build custom execution environments (e.g., Serverless runtime) by overriding goroutine and memory management methods.

### **1.4. Observability and Audit (CQRS)**

The architecture inherently separates audit data streams (immutable history for compliance) and monitoring (aggregated performance metrics). This optimizes storage and ensures 100% reliable instance context recovery.

### **1.5. Process Evolution (Ad-hoc)**

The importance of Ad-hoc processes is recognized as an "incubator" for rigid regulations. The engine architecture must allow for the gradual "crystallization" of free steps into formalized BPMN schemas.

## **2\. Current Architectural Challenges**

The selected roadmap aims to address critical challenges hindering industrial adoption:

* **Hierarchical Data Isolation:** The internal/scope package must ensure correct variable shadowing in sub-processes and upward propagation during business context updates.  
* **Time Persistence:** Transitioning from ephemeral in-memory timers to a persistent schedule with hydration support during instance loading.  
* **System Operation Fault Tolerance:** Implementing Retries and DLQ (Incidents) mechanisms to prevent token loss during external worker or DB technical failures.  
* **Platform-Agnostic UI:** Creating a registry of abstract form schemas to decouple process logic from rendering specifics (Web/Mobile).

## **3\. BPMN Element Implementation Phases**

The plan is divided into 6 interdependent stages, from the infrastructure core to Day-2 operation tools.

### **Phase 0: Infrastructure Foundation**

*Focus: Context isolation, extension interfaces, and basic data contracts in core. Per SAD-001, IAM enforcement and multitenancy policy live in the runtime layer; this phase establishes the core-side interfaces those runtime concerns plug into.*

* **State Management (Scope):** Implementing the scope tree in internal/scope. Support for name resolution rules (Shadowing) and thread-safe access via sync.RWMutex.  
* **IAM and Multi-tenancy interfaces (core-side):** TenantID, IdentityService, and AuthorizationService interfaces (see IAM ADR — currently in docs/adr/iam/, to be relocated to runtime/ when that submodule scaffolds per SAD-001 §9.2). Core accepts tenant-aware context; runtime enforces isolation policy.  
* **Expression Layer:** Finalizing the **Formal Expression** interface and its integration with the Scope hierarchy.  
* **Form Registry:** Mechanism for storing and serving abstract JSON metadata schemas via formKey.  
* **Event Core (Observability):** Implementing EventHub and basic listeners for Audit (Event Sourcing) and Monitoring (Metrics). Logger/Tracer/MetricsRecorder extension interfaces wired with visible-by-default defaults (SAD-001 §11).
* **Module scaffolding:** Establish the multi-module monorepo layout (`runtime/`, `adapters/`) per SAD-001 §9.2 — even as empty placeholders, so import-direction discipline is enforceable from day 1.

### **Phase 1: Core Flow and Fault Tolerance**

*Focus: Executing basic algorithms and handling failures.*

* **Events:** None Start/End, Terminate End.  
* **Tasks:** Service Task (hybrid model: goroutines \+ external workers), User Task (routing via IAM), Manual Task.  
* **Error Management:** BpmnError contract implementation. Hierarchical resolution mechanism for Boundary Error Events.  
* **Fault Tolerance:** Basic Incident support. Automatic Retry policies and DLQ mechanisms.  
* **Gateways:** Exclusive Gateway (XOR), Parallel Gateway (AND) with local token synchronization.  
* **Data Objects:** Data Object and Data Store Reference implementation.

### **Phase 2: Asynchrony and Reusability**

*Focus: Inter-process communications and time management.*

* **Messaging:** Message Correlation Service implementation. Persistent subscriptions and signal routing via business keys. Message Start/Catch/Throw.  
* **Timers:** Persistence requirements implementation. Hydration logic for active timers. Support for Interrupting and Non-interrupting events.  
* **Structure:** Embedded Sub-Process (new Scope level), Call Activity (variable mapping between processes).  
* **Gateways:** Event-Based Gateway.

### **Phase 3: Business Logic and Mass Processing**

*Focus: Rules integration and iterative execution.*

* **Tasks:** Business Rule Task (DMN), **Script Task** (internal engine execution).  
* **Iterations:** Standard Loop, Multi-Instance (Sequential/Parallel). Scope isolation for parallel branches.  
* **Conditions:** Conditional Event (Start/Catch/Boundary). Reactive triggering on Scope data changes.

### **Phase 4: Full Conformance and Flexibility**

*Focus: Complex events and adaptive scenarios.*

* **Events:** Signal, Compensation (transaction rollbacks), Escalation, Link.  
* **Sub-processes:** Transaction Sub-Process, Event Sub-Process (interrupting and non-interrupting).  
* **Ad-hoc:** Ad-Hoc Sub-Process support. Dynamic task activation within a defined area.  
* **Gateways:** Inclusive Gateway (OR), Complex Gateway (in scope as explicit extension above Common Executable per SAD-001 §13.4 — two-phase activation/reset semantics per BPMN spec §13.4.5).

### **Phase 5: Day-2 Operations**

*Focus: Industrial lifecycle management.*

* **Versioning:** Instance binding strategies for definitions.  
* **Migration:** Migration API. Programmatic token transfer between versions (V1 \-\> V2) while maintaining Scope hierarchy.  
* **Administration:** Tools for manual instance state modification (Move Token API) and incident resolution via API.

# **References**

* [SAD-001 Vision & Architecture](../design/SAD-001-vision-and-architecture.md) — top-level architectural vision. This Roadmap implements SAD-001.
* [docs/bpmn-spec/conformance.md](../bpmn-spec/conformance.md) — BPMN 2.0 Process Execution Conformance scope definition (the authoritative in/out element list).
* [docs/bpmn-spec/](../bpmn-spec/) — BPMN 2.0 normative reference KB.
* IAM ADR — currently at `docs/adr/iam/ADR-001_ IAM Architecture.md`; will relocate to `runtime/docs/adr/` when the runtime submodule scaffolds (per SAD-001 §9.2).

# **Changes**

### **2026-05-29**

* Roadmap refreshed (v2.0): aligned with SAD-001 Vision & Architecture.
* §1.1 expanded with Security and Observability extension categories; observability default policy documented.
* Phase 0 reframed: IAM/multitenancy enforcement is runtime concern per SAD-001 §12; core-side scope establishes interfaces only. Module scaffolding added as Phase 0 item per SAD-001 §9.2.
* Phase 3 — Russian section headers translated to English (Задачи → Tasks, Итерации → Iterations).
* Phase 4 — ComplexGateway explicitly noted as in-scope extension above Common Executable Subclass.
* References section added.

### **2026-03-29**

* Roadmap updated (v1.05): Translated to English.  
* Stages synchronized with the latest architectural GAP analysis.

### **2026-03-29**

* Added Script Task, Event Sub-Process, and Complex Gateway.  
* Refined Timer Events with Non-interrupting support.