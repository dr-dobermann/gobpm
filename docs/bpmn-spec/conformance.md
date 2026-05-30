# Process Execution Conformance — Scope

Per BPMN 2.0 §2.1.2, **Process Execution Conformance** requires an engine to implement the operational semantics defined in §13 for the set of elements declared as its conformant subset. The minimum conformant subset is the **Common Executable Subclass** (§2.1.3).

`gobpm` targets exactly this: Common Executable Subclass + §13 execution semantics, **plus ComplexGateway as an explicit extension**. Anything else is out of scope.

**Scope note on ComplexGateway:** §13.4.5 defines ComplexGateway with a complete operational semantics, but it is NOT in the minimum Common Executable Subclass per §2.1.3. We include it because it enables workflow patterns (Structured Discriminator WCP-9, Blocking Discriminator WCP-28, Structured Partial Join WCP-30, Blocking Partial Join WCP-31) not otherwise expressible. Process Execution Conformance permits supersets of the Common Executable Subclass — including ComplexGateway makes the engine *more* conformant, not less.

## In scope

### Process container
| Element | bpmn-moddle type | Notes |
|---|---|---|
| Process | `Process` | Top-level executable process (`isExecutable=true`) |
| SubProcess | `SubProcess` | Embedded sub-process; also Event Sub-Process when `triggeredByEvent=true` |
| Transaction | `Transaction` | Sub-process with ACID-like semantics, cancel/compensation triggers |
| AdHocSubProcess | `AdHocSubProcess` | Unordered activity set with completion condition |
| CallActivity | `CallActivity` | Invokes a reusable `CallableElement` |
| Lane / LaneSet | `Lane`, `LaneSet` | Organizational grouping only — no execution semantics attached |

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
| ComplexGateway | `ComplexGateway` | Activation expression over per-gate token counts, 2-phase activation/reset. **Extension above Common Executable** (see scope note). |

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
| DataInput / DataOutput | `DataInput`, `DataOutput` | Activity I/O variables |
| DataInputAssociation | `DataInputAssociation` | Data flow into activity |
| DataOutputAssociation | `DataOutputAssociation` | Data flow out of activity |
| InputSet / OutputSet | `InputSet`, `OutputSet` | I/O grouping with optional / while-executing semantics |
| InputOutputSpecification | `InputOutputSpecification` | Activity I/O contract |
| InputOutputBinding | `InputOutputBinding` | Maps Operation I/O to CallableElement I/O |
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
| GlobalTask / GlobalManualTask / GlobalUserTask / GlobalScriptTask / GlobalBusinessRuleTask | `GlobalTask`, ... | Reusable task definitions invocable via CallActivity |

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
| Collaboration family | `Collaboration`, `Participant`, `ParticipantAssociation`, `ParticipantMultiplicity`, `PartnerEntity`, `PartnerRole`, `InteractionNode`, `MessageFlow`, `MessageFlowAssociation` | Cross-process modeling layer; inter-process messaging covered by Message events |
| Visual artifacts | `TextAnnotation`, `Group`, `Category`, `CategoryValue`, `Artifact` | Pure visual — Association is kept because it carries compensation semantics |
| Cross-namespace | `Relationship` | Not execution-related |
| DI / DC | `BPMNShape`, `BPMNEdge`, `Bounds`, `Point`, all `bpmndi:*` and `dc:*`, `di:*` | Visual layout metamodel; not part of execution conformance |
| BPEL mapping | (no bpmn-moddle types) | Separate conformance subclass |

## Boundary cases noted

- **Lane / LaneSet** — kept in scope as organizational grouping only. The spec defines lanes as having no token-flow semantics; activities in lanes execute exactly as if no lanes existed. Engine MUST parse and preserve, MUST NOT attach behavior.
- **Boundary events on CallActivity** — explicitly allowed by §10.5.4. In scope.
- **Event Sub-Process** — modeled as `SubProcess` with `triggeredByEvent=true`. In scope.
- **Compensation Association** — `Association` between an activity and its compensation handler. The element is visual elsewhere; here it carries normative semantics.

## Spec section index (for cross-reference)

| Topic | Spec section |
|---|---|
| Conformance subclasses | §2 |
| Common Executable Subclass element list | §2.1.3 |
| Process model | §10 |
| Activities | §10.5 |
| Events | §10.4 |
| Gateways | §10.6 |
| Data | §10.3 |
| Execution semantics (state machines, token flow) | §13 |
| Correlation | §8.4.2 |
