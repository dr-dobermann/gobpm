# Process Execution Conformance — Scope

**Process Execution Conformance is BPMN 2.0.2 §2.3**, and it has two requirements, addressed to "the tool":

- **§2.3.1 Execution Semantics** — the tool "MUST fully support and interpret the operational semantics and Activity life-cycle"; non-operational elements "MAY be ignored".
- **§2.3.2 Import of Process Diagrams** — the tool "MUST support import of BPMN Process diagram types including its definitional Collaboration".

**A library is not a tool.** `gobpm` ships as an embeddable library and, built on it, the `gobpm-server` product ([SAD-001](../design/SAD-001-vision-and-architecture.md) §2, §14). §2.3.1 is the **library's** target and the subject of this document. §2.3.2 belongs to the **server**, through the converter — so the import-side element coverage is tracked with that work, not here.

**This list enumerates §13's operational elements plus the supporting classes they need.** Clause 13 animates process instantiation and termination (§13.2), activities (§13.3, including Sub-Process, Call Activity, Ad-Hoc, Loop and Multi-Instance), all five gateways (§13.4) and every event position (§13.5, including boundary, event sub-process and compensation). The remaining entries fall into two further tiers, and the element-coverage suite proves each differently:

| Tier | What it is | How coverage is proven |
|---|---|---|
| **Operational** | animated by Clause 13 — activities, gateways, events, flows, containers, markers | a suite-owned scenario **executes** it |
| **Supporting** | consumed by Clause 13 to express the above — data, foundation, correlation, operations, human interaction | a guard-checked **binding** to the tests that exercise it |
| **Model-only** | carried but never executed — `Lane`/`LaneSet` | the type **exists and survives a round-trip**; execution must ignore it |

The first distinction is the standard's own (§13 animates, §8/§10 supply). The third exists because "non-operational" governs execution, not representation: §2.3.1 permits ignoring such elements at run time, while §2.3.2 and ADR-024 §2.8 require the model to hold them.

> **Correction (2026-08-02).** Earlier revisions of this document cited "§2.1.2" for Process Execution Conformance and derived the element set from the **Common Executable Subclass** ("§2.1.3"). Neither clause exists — §2.1 is *General*, §2.2 is Process **Modeling** Conformance — and Common Executable is a sub-class of *Modeling* conformance (§2.2.1) for tools that **emit** executable models, mandating XML Schema, WSDL and XPath, none of which this engine uses. The mis-citation is traceable to §2.1's own cross-reference paragraph, which is off by one against the spec's headings. Consequence: **ComplexGateway is no longer an "extension"** — §13.4.5 gives it complete operational semantics, so §2.3.1 always required it. Its workflow-pattern value (Structured Discriminator WCP-9, Blocking Discriminator WCP-28, Structured Partial Join WCP-30, Blocking Partial Join WCP-31) is a reason it is *useful*, not a reason it is optional.

## In scope

### Process container
| Element | bpmn-moddle type | Notes |
|---|---|---|
| Process | `Process` | Top-level executable process (`isExecutable=true`) |
| SubProcess | `SubProcess` | Embedded sub-process; also Event Sub-Process when `triggeredByEvent=true` |
| Transaction | `Transaction` | Sub-process with ACID-like semantics, cancel/compensation triggers |
| AdHocSubProcess | `AdHocSubProcess` | Unordered activity set with completion condition |
| CallActivity | `CallActivity` | Invokes a reusable `CallableElement` |
| Lane / LaneSet | `Lane`, `LaneSet` | **Model-only** — carried, never executed. §2.3.1 lets execution ignore it (no token semantics); §2.3.2 obliges an importer to preserve it, and a model that cannot hold it cannot export it back (ADR-024 §2.8 semantic round-trip). |

### Activities
| Element | bpmn-moddle type | Notes |
|---|---|---|
| Task (abstract) | `Task` | Base for typed tasks |
| ServiceTask | `ServiceTask` | Operation invocation |
| UserTask | `UserTask` | Human task with form |
| ManualTask | `ManualTask` | Out-of-system task, no engine action |
| ScriptTask | `ScriptTask` | Inline script execution |
| BusinessRuleTask | `BusinessRuleTask` | DMN / rule engine call |
| SendTask | `SendTask` | Message throw |
| ReceiveTask | `ReceiveTask` | Message catch |

### Markers (on activities)
| Element | bpmn-moddle type | Notes |
|---|---|---|
| StandardLoopCharacteristics | `StandardLoopCharacteristics` | Sequential loop with test condition |
| MultiInstanceLoopCharacteristics | `MultiInstanceLoopCharacteristics` | Sequential or parallel MI with cardinality / collection |
| ComplexBehaviorDefinition | `ComplexBehaviorDefinition` | Custom MI completion / event handling |

### Events
| Element | bpmn-moddle type | Position |
|---|---|---|
| StartEvent | `StartEvent` | Process start, top-level or event-sub-process |
| IntermediateCatchEvent | `IntermediateCatchEvent` | Inline wait |
| IntermediateThrowEvent | `IntermediateThrowEvent` | Inline emit |
| EndEvent | `EndEvent` | Process / sub-process termination |
| BoundaryEvent | `BoundaryEvent` | Attached to activity (interrupting + non-interrupting via `cancelActivity`) |
| ImplicitThrowEvent | `ImplicitThrowEvent` | Implicit throw at process end (rarely used directly) |

### Event definitions (subtypes)
| Element | bpmn-moddle type | Applies to |
|---|---|---|
| MessageEventDefinition | `MessageEventDefinition` | Start, IntermediateCatch, IntermediateThrow, End, Boundary |
| TimerEventDefinition | `TimerEventDefinition` | Start, IntermediateCatch, Boundary |
| SignalEventDefinition | `SignalEventDefinition` | Start, IntermediateCatch, IntermediateThrow, End, Boundary |
| ErrorEventDefinition | `ErrorEventDefinition` | Start (event sub-process only), End, Boundary |
| EscalationEventDefinition | `EscalationEventDefinition` | Start (event sub-process), IntermediateThrow, End, Boundary |
| CompensateEventDefinition | `CompensateEventDefinition` | Start (event sub-process), IntermediateThrow, End, Boundary |
| CancelEventDefinition | `CancelEventDefinition` | End, Boundary — Transaction sub-process only |
| ConditionalEventDefinition | `ConditionalEventDefinition` | Start, IntermediateCatch, Boundary |
| LinkEventDefinition | `LinkEventDefinition` | IntermediateCatch (target), IntermediateThrow (source) |
| TerminateEventDefinition | `TerminateEventDefinition` | End — terminates entire process instance |
| Message, Signal, Error, Escalation | (referenced item definitions) | Resolved by event definitions |

### Gateways
| Element | bpmn-moddle type | Notes |
|---|---|---|
| ExclusiveGateway | `ExclusiveGateway` | XOR — first true condition wins |
| ParallelGateway | `ParallelGateway` | AND — all paths split / all paths sync |
| InclusiveGateway | `InclusiveGateway` | OR — all true conditions; merge waits for all expected tokens |
| EventBasedGateway | `EventBasedGateway` | Race between catching events |
| ComplexGateway | `ComplexGateway` | Activation expression over per-gate token counts, 2-phase activation/reset. Operational per **§13.4.5**, therefore required by §2.3.1 — not an extension (see the correction note above). |

### Flows
| Element | bpmn-moddle type | Notes |
|---|---|---|
| SequenceFlow | `SequenceFlow` | Conditional (`conditionExpression`), default (referenced by `default` on source) |
| Association | `Association` | Compensation associations (sources to compensation handlers) |

### Data
| Element | bpmn-moddle type | Notes |
|---|---|---|
| ItemDefinition | `ItemDefinition` | Type descriptor for data items |
| DataObject | `DataObject` | Process-scoped data |
| DataObjectReference | `DataObjectReference` | Reference to a DataObject within flow scope |
| DataStore | `DataStore` | External persistent data |
| DataStoreReference | `DataStoreReference` | Reference to a DataStore |
| Property | `Property` | Process / activity local variable |
| DataInput / DataOutput | `DataInput`, `DataOutput` | Activity I/O variables; a throw event's inputs and a catch event's outputs (§10.4.2); the process's declared contract (ADR-040) |
| DataInputAssociation | `DataInputAssociation` | Data flow into an activity or a throw event; a process output into an End event |
| DataOutputAssociation | `DataOutputAssociation` | Data flow out of an activity or a catch event; a Start event into a process input |
| InputSet / OutputSet | `InputSet`, `OutputSet` | I/O grouping with optional / while-executing semantics |
| InputOutputSpecification | `InputOutputSpecification` | Activity I/O contract |
| InputOutputBinding | `InputOutputBinding` | Binds one InputSet + one OutputSet to an `Operation`. **Realized implicitly, with no named type** — gobpm declares a single InputSet and a single OutputSet ([SAD-001](../design/SAD-001-vision-and-architecture.md) §14.1), so there is no pair to select and the binding is the `Operation` contract itself (`BindInputOnly` in, `Execute` out). Absence of the type is a consequence of that deviation, not a gap. |
| Assignment | `Assignment` | Single from/to expression pair within a DataAssociation |
| DataState | `DataState` | Optional state qualifier on data |
| DataAssociation | `DataAssociation` | Abstract base for In/Out associations |
| ItemAwareElement | `ItemAwareElement` | Base for all data-carrying elements |

### Human interaction (UserTask support)
| Element | bpmn-moddle type | Notes |
|---|---|---|
| HumanPerformer | `HumanPerformer` | Specialized Performer |
| PotentialOwner | `PotentialOwner` | Candidate owner expression |
| Performer | `Performer` | Generic performer |
| Rendering | `Rendering` | UI hint |
| Resource | `Resource` | Resource definition |
| ResourceRole | `ResourceRole` | Role assignment |
| ResourceParameter | `ResourceParameter` | Resource query parameter |
| ResourceParameterBinding | `ResourceParameterBinding` | Binds value to ResourceParameter |
| ResourceAssignmentExpression | `ResourceAssignmentExpression` | Expression resolving to resource(s) |

### Correlation (Message events)
| Element | bpmn-moddle type | Notes |
|---|---|---|
| CorrelationKey | `CorrelationKey` | Named set of correlation properties |
| CorrelationProperty | `CorrelationProperty` | Property used to match messages to process instance |
| CorrelationPropertyRetrievalExpression | `CorrelationPropertyRetrievalExpression` | Extracts property value from a message |
| CorrelationPropertyBinding | `CorrelationPropertyBinding` | Binds property to subscription |
| CorrelationSubscription | `CorrelationSubscription` | Process-level subscription to a correlation key |

### Operations / Interfaces (Service/Send/Receive tasks)
| Element | bpmn-moddle type | Notes |
|---|---|---|
| Interface | `Interface` | Operation grouping |
| Operation | `Operation` | Service operation with in/out message |
| EndPoint | `EndPoint` | Operation endpoint reference |
| GlobalTask / GlobalManualTask / GlobalUserTask / GlobalScriptTask / GlobalBusinessRuleTask | `GlobalTask`, ... | Reusable task definitions invocable via CallActivity. **In scope.** A GlobalTask is reuse *by reference*, which needs a registry of callable definitions — and the process registry is one: a global task is a callable **process** whose body is that one task, registered under its id and called like any other (ADR-023 §2.7). The importer builds that process; nothing on the call path distinguishes it from a called process, which is what §13.3.4 says a call is. The library also covers the same need by construction for code-built models — a Go constructor returning a configured task is a reusable definition, and a parameterizable one ([task reuse](../guides/tasks/index.md#reusing-tasks)). |

### Foundation / base types
| Element | bpmn-moddle type | Notes |
|---|---|---|
| Definitions | `Definitions` | Top-level container of all root elements |
| BaseElement | `BaseElement` | Root of all BPMN elements (id, documentation, extensions) |
| RootElement | `RootElement` | Marker for top-level-only elements |
| FlowElement | `FlowElement` | Anything that appears in a process flow |
| FlowNode | `FlowNode` | Connectable element (activities, events, gateways) |
| FlowElementsContainer | `FlowElementsContainer` | Holds flowElements + laneSets (Process, SubProcess) |
| CallableElement | `CallableElement` | Invocable thing (Process, GlobalTask) — has I/O specification |
| Expression / FormalExpression | `Expression`, `FormalExpression` | Conditions, assignments, completion criteria |
| Documentation | `Documentation` | Human-readable annotation |
| Extension / ExtensionDefinition / ExtensionAttributeDefinition / ExtensionElements | (as named) | Vendor extension mechanism |
| Import | `Import` | External type/schema reference |
| Auditing / Monitoring | `Auditing`, `Monitoring` | Process-level audit/monitor placeholders |

## Out of scope

| Element / family | bpmn-moddle types | Reason |
|---|---|---|
| Choreography family | `Choreography`, `SubChoreography`, `CallChoreography`, `ChoreographyTask`, `ChoreographyActivity`, `GlobalChoreographyTask` | Separate Choreography Modeling Conformance subclass |
| Conversation family | `Conversation`, `SubConversation`, `CallConversation`, `GlobalConversation`, `ConversationNode`, `ConversationLink`, `ConversationAssociation` | Modeling-only, not execution |
| Collaboration family | `Collaboration`, `Participant`, `ParticipantAssociation`, `ParticipantMultiplicity`, `PartnerEntity`, `PartnerRole`, `InteractionNode`, `MessageFlow`, `MessageFlowAssociation` | Not animated by Clause 13; inter-process messaging is covered by Message events. Note §2.3.2 names the "definitional Collaboration" for **import** — that is a server/converter concern, not a library one |
| Category machinery | `Category`, `CategoryValue` | Definitions-level taxonomy roots — consumed at load as the resolution input a `Group` embeds its value from (ADR-039); not carried as model elements |
| Cross-namespace | `Relationship` | Not execution-related |
| DI / DC | `BPMNShape`, `BPMNEdge`, `Bounds`, `Point`, all `bpmndi:*` and `dc:*`, `di:*` | Visual layout metamodel; not part of execution conformance |
| BPEL mapping | (no bpmn-moddle types) | Separate conformance subclass |

## Boundary cases noted

- **Lane / LaneSet** — **in scope, model-only.** "Non-operational" governs *execution*, not *representation*: §2.3.1 lets a conformant tool ignore lanes at run time (they carry no token semantics — activities in lanes execute exactly as if no lanes existed), but the model must still **hold** them. Two independent reasons: §2.3.2 obliges an importer to support Process diagrams, and dropping a modeller's lanes is not supporting them; and ADR-024 §2.8 guarantees a **semantic round-trip**, which export cannot deliver for a structure the model never stored. So: engine MUST parse and preserve, MUST NOT attach behaviour — the rule this document always stated, now with its clause grounding. The model **holds** them: `pkg/model/lanes` carries `Lane` and `LaneSet`, and a `Process` holds its lane sets. What remains is the import, which is a converter task rather than a model gap.
- **Boundary events on CallActivity** — explicitly allowed by §10.5.4. In scope.
- **Event Sub-Process** — modeled as `SubProcess` with `triggeredByEvent=true`. In scope.
- **Compensation Association** — `Association` between an activity and its compensation handler. The element is visual elsewhere; here it carries normative semantics.
- **Artifacts (§8.4.1) — in scope, model-only** (ADR-039). The standard's three — `Association` (plain shape), `TextAnnotation`, `Group` — follow the Lane reading: §2.3.1 lets execution ignore them, and the model **carries** them for §2.3.2 loading and the converters' semantic round-trip. `pkg/model/artifacts` holds the three kinds behind a closed `Artifact` interface; `Process` and `SubProcess` carry the collection; no execution path reads one. The compensation shape of `Association` stays what the row above says — the boundary's handler wiring, never duplicated as an artifact. `Category`/`CategoryValue` remain un-carried: load-time resolution input only (the out-of-scope table's row).

## Spec section index (for cross-reference)

The chapter-10 rows below once read Activities §10.5, Events §10.4, Data §10.3 —
a transposition that contradicted every detail file in this extract, and the
boundary-event note above ("explicitly allowed by §10.5.4"). It had propagated
into nine wrong `§` pins in the converter's modeler-facing refusals before it
was caught. The order is Activities, Data, Events, Gateways.

| Topic | Spec section |
|---|---|
| Conformance subclasses | §2 |
| Process Execution Conformance (§2.3.1 semantics, §2.3.2 import) | §2.3 |
| Process **Modeling** Conformance sub-classes (Descriptive / Analytic / Common Executable) — **not** this engine's target | §2.2.1 |
| Process model | §10 |
| Activities | §10.3 |
| Data (Items and Data) | §10.4 |
| Events | §10.5 |
| Gateways | §10.6 |
| Execution semantics (state machines, token flow) | §13 |
| Correlation | §8.4.2 |
