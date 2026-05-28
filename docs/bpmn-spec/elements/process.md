# Process / Container

_Structural metamodel for **Process / Container** elements, extracted from bpmn-moddle. In-scope per [../conformance.md](../conformance.md)._

_Kind legend: `attr` = XML attribute, `child elem` = XML child element, `(ref)` = ID reference to another element rather than embedded._

---

### Process

**Type hierarchy:** `Process → FlowElementsContainer → BaseElement → CallableElement → RootElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `processType` | `ProcessType` | attr | 0..1 |  |
| `isClosed` | `Boolean` | attr | 0..1 |  |
| `auditing` | `Auditing` | child elem | 0..1 |  |
| `monitoring` | `Monitoring` | child elem | 0..1 |  |
| `properties` | `Property` | child elem | 0..* |  |
| `laneSets` | `LaneSet` | child elem | 0..* | replaces `FlowElementsContainer#laneSets` |
| `flowElements` | `FlowElement` | child elem | 0..* | replaces `FlowElementsContainer#flowElements` |
| `artifacts` | `Artifact` | child elem | 0..* |  |
| `resources` | `ResourceRole` | child elem | 0..* |  |
| `correlationSubscriptions` | `CorrelationSubscription` | child elem | 0..* |  |
| `supports` | `Process` | child elem (ref) | 0..* |  |
| `definitionalCollaborationRef` | `Collaboration` | attr (ref) | 0..1 |  |
| `isExecutable` | `Boolean` | attr | 0..1 |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |
| `name` | `CallableElement` | `String` | attr | 0..1 |
| `ioSpecification` | `CallableElement` | `InputOutputSpecification` | child elem | 0..1 |
| `supportedInterfaceRef` | `CallableElement` | `Interface` | child elem (ref) | 0..* |
| `ioBinding` | `CallableElement` | `InputOutputBinding` | child elem | 0..* |

---

### Lane

**Type hierarchy:** `Lane → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `name` | `String` | attr | 0..1 |  |
| `partitionElementRef` | `BaseElement` | attr (ref) | 0..1 |  |
| `partitionElement` | `BaseElement` | child elem | 0..1 |  |
| `flowNodeRef` | `FlowNode` | child elem (ref) | 0..* |  |
| `childLaneSet` | `LaneSet` | child elem | 0..1 |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### LaneSet

**Type hierarchy:** `LaneSet → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `lanes` | `Lane` | child elem | 0..* |  |
| `name` | `String` | attr | 0..1 |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---
