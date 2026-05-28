# Events

_Structural metamodel for **Events** elements, extracted from bpmn-moddle. In-scope per [../conformance.md](../conformance.md)._

_Kind legend: `attr` = XML attribute, `child elem` = XML child element, `(ref)` = ID reference to another element rather than embedded._

---

### Event

_Abstract type._

**Type hierarchy:** `Event → FlowNode → FlowElement → BaseElement → InteractionNode`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `properties` | `Property` | child elem | 0..* |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `incoming` | `FlowNode` | `SequenceFlow` | child elem (ref) | 0..* |
| `outgoing` | `FlowNode` | `SequenceFlow` | child elem (ref) | 0..* |
| `lanes` | `FlowNode` | `Lane` | child elem (ref) | 0..* |
| `name` | `FlowElement` | `String` | attr | 0..1 |
| `auditing` | `FlowElement` | `Auditing` | child elem | 0..1 |
| `monitoring` | `FlowElement` | `Monitoring` | child elem | 0..1 |
| `categoryValueRef` | `FlowElement` | `CategoryValue` | child elem (ref) | 0..* |
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |
| `incomingConversationLinks` | `InteractionNode` | `ConversationLink` | child elem (ref) | 0..* |
| `outgoingConversationLinks` | `InteractionNode` | `ConversationLink` | child elem (ref) | 0..* |

---

### CatchEvent

_Abstract type._

**Type hierarchy:** `CatchEvent → Event → FlowNode → FlowElement → BaseElement → InteractionNode`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `parallelMultiple` | `Boolean` | attr | 0..1 | default `False` |
| `dataOutputs` | `DataOutput` | child elem | 0..* |  |
| `dataOutputAssociations` | `DataOutputAssociation` | child elem | 0..* |  |
| `outputSet` | `OutputSet` | child elem | 0..1 |  |
| `eventDefinitions` | `EventDefinition` | child elem | 0..* |  |
| `eventDefinitionRef` | `EventDefinition` | child elem (ref) | 0..* |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `properties` | `Event` | `Property` | child elem | 0..* |
| `incoming` | `FlowNode` | `SequenceFlow` | child elem (ref) | 0..* |
| `outgoing` | `FlowNode` | `SequenceFlow` | child elem (ref) | 0..* |
| `lanes` | `FlowNode` | `Lane` | child elem (ref) | 0..* |
| `name` | `FlowElement` | `String` | attr | 0..1 |
| `auditing` | `FlowElement` | `Auditing` | child elem | 0..1 |
| `monitoring` | `FlowElement` | `Monitoring` | child elem | 0..1 |
| `categoryValueRef` | `FlowElement` | `CategoryValue` | child elem (ref) | 0..* |
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |
| `incomingConversationLinks` | `InteractionNode` | `ConversationLink` | child elem (ref) | 0..* |
| `outgoingConversationLinks` | `InteractionNode` | `ConversationLink` | child elem (ref) | 0..* |

---

### ThrowEvent

_Abstract type._

**Type hierarchy:** `ThrowEvent → Event → FlowNode → FlowElement → BaseElement → InteractionNode`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `dataInputs` | `DataInput` | child elem | 0..* |  |
| `dataInputAssociations` | `DataInputAssociation` | child elem | 0..* |  |
| `inputSet` | `InputSet` | child elem | 0..1 |  |
| `eventDefinitions` | `EventDefinition` | child elem | 0..* |  |
| `eventDefinitionRef` | `EventDefinition` | child elem (ref) | 0..* |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `properties` | `Event` | `Property` | child elem | 0..* |
| `incoming` | `FlowNode` | `SequenceFlow` | child elem (ref) | 0..* |
| `outgoing` | `FlowNode` | `SequenceFlow` | child elem (ref) | 0..* |
| `lanes` | `FlowNode` | `Lane` | child elem (ref) | 0..* |
| `name` | `FlowElement` | `String` | attr | 0..1 |
| `auditing` | `FlowElement` | `Auditing` | child elem | 0..1 |
| `monitoring` | `FlowElement` | `Monitoring` | child elem | 0..1 |
| `categoryValueRef` | `FlowElement` | `CategoryValue` | child elem (ref) | 0..* |
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |
| `incomingConversationLinks` | `InteractionNode` | `ConversationLink` | child elem (ref) | 0..* |
| `outgoingConversationLinks` | `InteractionNode` | `ConversationLink` | child elem (ref) | 0..* |

---

### StartEvent

**Type hierarchy:** `StartEvent → CatchEvent → Event → FlowNode → FlowElement → BaseElement → InteractionNode`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `isInterrupting` | `Boolean` | attr | 0..1 | default `True` |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `parallelMultiple` | `CatchEvent` | `Boolean` | attr | 0..1 |
| `dataOutputs` | `CatchEvent` | `DataOutput` | child elem | 0..* |
| `dataOutputAssociations` | `CatchEvent` | `DataOutputAssociation` | child elem | 0..* |
| `outputSet` | `CatchEvent` | `OutputSet` | child elem | 0..1 |
| `eventDefinitions` | `CatchEvent` | `EventDefinition` | child elem | 0..* |
| `eventDefinitionRef` | `CatchEvent` | `EventDefinition` | child elem (ref) | 0..* |
| `properties` | `Event` | `Property` | child elem | 0..* |
| `incoming` | `FlowNode` | `SequenceFlow` | child elem (ref) | 0..* |
| `outgoing` | `FlowNode` | `SequenceFlow` | child elem (ref) | 0..* |
| `lanes` | `FlowNode` | `Lane` | child elem (ref) | 0..* |
| `name` | `FlowElement` | `String` | attr | 0..1 |
| `auditing` | `FlowElement` | `Auditing` | child elem | 0..1 |
| `monitoring` | `FlowElement` | `Monitoring` | child elem | 0..1 |
| `categoryValueRef` | `FlowElement` | `CategoryValue` | child elem (ref) | 0..* |
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |
| `incomingConversationLinks` | `InteractionNode` | `ConversationLink` | child elem (ref) | 0..* |
| `outgoingConversationLinks` | `InteractionNode` | `ConversationLink` | child elem (ref) | 0..* |

---

### IntermediateCatchEvent

**Type hierarchy:** `IntermediateCatchEvent → CatchEvent → Event → FlowNode → FlowElement → BaseElement → InteractionNode`

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `parallelMultiple` | `CatchEvent` | `Boolean` | attr | 0..1 |
| `dataOutputs` | `CatchEvent` | `DataOutput` | child elem | 0..* |
| `dataOutputAssociations` | `CatchEvent` | `DataOutputAssociation` | child elem | 0..* |
| `outputSet` | `CatchEvent` | `OutputSet` | child elem | 0..1 |
| `eventDefinitions` | `CatchEvent` | `EventDefinition` | child elem | 0..* |
| `eventDefinitionRef` | `CatchEvent` | `EventDefinition` | child elem (ref) | 0..* |
| `properties` | `Event` | `Property` | child elem | 0..* |
| `incoming` | `FlowNode` | `SequenceFlow` | child elem (ref) | 0..* |
| `outgoing` | `FlowNode` | `SequenceFlow` | child elem (ref) | 0..* |
| `lanes` | `FlowNode` | `Lane` | child elem (ref) | 0..* |
| `name` | `FlowElement` | `String` | attr | 0..1 |
| `auditing` | `FlowElement` | `Auditing` | child elem | 0..1 |
| `monitoring` | `FlowElement` | `Monitoring` | child elem | 0..1 |
| `categoryValueRef` | `FlowElement` | `CategoryValue` | child elem (ref) | 0..* |
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |
| `incomingConversationLinks` | `InteractionNode` | `ConversationLink` | child elem (ref) | 0..* |
| `outgoingConversationLinks` | `InteractionNode` | `ConversationLink` | child elem (ref) | 0..* |

---

### IntermediateThrowEvent

**Type hierarchy:** `IntermediateThrowEvent → ThrowEvent → Event → FlowNode → FlowElement → BaseElement → InteractionNode`

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `dataInputs` | `ThrowEvent` | `DataInput` | child elem | 0..* |
| `dataInputAssociations` | `ThrowEvent` | `DataInputAssociation` | child elem | 0..* |
| `inputSet` | `ThrowEvent` | `InputSet` | child elem | 0..1 |
| `eventDefinitions` | `ThrowEvent` | `EventDefinition` | child elem | 0..* |
| `eventDefinitionRef` | `ThrowEvent` | `EventDefinition` | child elem (ref) | 0..* |
| `properties` | `Event` | `Property` | child elem | 0..* |
| `incoming` | `FlowNode` | `SequenceFlow` | child elem (ref) | 0..* |
| `outgoing` | `FlowNode` | `SequenceFlow` | child elem (ref) | 0..* |
| `lanes` | `FlowNode` | `Lane` | child elem (ref) | 0..* |
| `name` | `FlowElement` | `String` | attr | 0..1 |
| `auditing` | `FlowElement` | `Auditing` | child elem | 0..1 |
| `monitoring` | `FlowElement` | `Monitoring` | child elem | 0..1 |
| `categoryValueRef` | `FlowElement` | `CategoryValue` | child elem (ref) | 0..* |
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |
| `incomingConversationLinks` | `InteractionNode` | `ConversationLink` | child elem (ref) | 0..* |
| `outgoingConversationLinks` | `InteractionNode` | `ConversationLink` | child elem (ref) | 0..* |

---

### EndEvent

**Type hierarchy:** `EndEvent → ThrowEvent → Event → FlowNode → FlowElement → BaseElement → InteractionNode`

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `dataInputs` | `ThrowEvent` | `DataInput` | child elem | 0..* |
| `dataInputAssociations` | `ThrowEvent` | `DataInputAssociation` | child elem | 0..* |
| `inputSet` | `ThrowEvent` | `InputSet` | child elem | 0..1 |
| `eventDefinitions` | `ThrowEvent` | `EventDefinition` | child elem | 0..* |
| `eventDefinitionRef` | `ThrowEvent` | `EventDefinition` | child elem (ref) | 0..* |
| `properties` | `Event` | `Property` | child elem | 0..* |
| `incoming` | `FlowNode` | `SequenceFlow` | child elem (ref) | 0..* |
| `outgoing` | `FlowNode` | `SequenceFlow` | child elem (ref) | 0..* |
| `lanes` | `FlowNode` | `Lane` | child elem (ref) | 0..* |
| `name` | `FlowElement` | `String` | attr | 0..1 |
| `auditing` | `FlowElement` | `Auditing` | child elem | 0..1 |
| `monitoring` | `FlowElement` | `Monitoring` | child elem | 0..1 |
| `categoryValueRef` | `FlowElement` | `CategoryValue` | child elem (ref) | 0..* |
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |
| `incomingConversationLinks` | `InteractionNode` | `ConversationLink` | child elem (ref) | 0..* |
| `outgoingConversationLinks` | `InteractionNode` | `ConversationLink` | child elem (ref) | 0..* |

---

### BoundaryEvent

**Type hierarchy:** `BoundaryEvent → CatchEvent → Event → FlowNode → FlowElement → BaseElement → InteractionNode`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `cancelActivity` | `Boolean` | attr | 0..1 | default `True` |
| `attachedToRef` | `Activity` | attr (ref) | 0..1 |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `parallelMultiple` | `CatchEvent` | `Boolean` | attr | 0..1 |
| `dataOutputs` | `CatchEvent` | `DataOutput` | child elem | 0..* |
| `dataOutputAssociations` | `CatchEvent` | `DataOutputAssociation` | child elem | 0..* |
| `outputSet` | `CatchEvent` | `OutputSet` | child elem | 0..1 |
| `eventDefinitions` | `CatchEvent` | `EventDefinition` | child elem | 0..* |
| `eventDefinitionRef` | `CatchEvent` | `EventDefinition` | child elem (ref) | 0..* |
| `properties` | `Event` | `Property` | child elem | 0..* |
| `incoming` | `FlowNode` | `SequenceFlow` | child elem (ref) | 0..* |
| `outgoing` | `FlowNode` | `SequenceFlow` | child elem (ref) | 0..* |
| `lanes` | `FlowNode` | `Lane` | child elem (ref) | 0..* |
| `name` | `FlowElement` | `String` | attr | 0..1 |
| `auditing` | `FlowElement` | `Auditing` | child elem | 0..1 |
| `monitoring` | `FlowElement` | `Monitoring` | child elem | 0..1 |
| `categoryValueRef` | `FlowElement` | `CategoryValue` | child elem (ref) | 0..* |
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |
| `incomingConversationLinks` | `InteractionNode` | `ConversationLink` | child elem (ref) | 0..* |
| `outgoingConversationLinks` | `InteractionNode` | `ConversationLink` | child elem (ref) | 0..* |

---

### ImplicitThrowEvent

**Type hierarchy:** `ImplicitThrowEvent → ThrowEvent → Event → FlowNode → FlowElement → BaseElement → InteractionNode`

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `dataInputs` | `ThrowEvent` | `DataInput` | child elem | 0..* |
| `dataInputAssociations` | `ThrowEvent` | `DataInputAssociation` | child elem | 0..* |
| `inputSet` | `ThrowEvent` | `InputSet` | child elem | 0..1 |
| `eventDefinitions` | `ThrowEvent` | `EventDefinition` | child elem | 0..* |
| `eventDefinitionRef` | `ThrowEvent` | `EventDefinition` | child elem (ref) | 0..* |
| `properties` | `Event` | `Property` | child elem | 0..* |
| `incoming` | `FlowNode` | `SequenceFlow` | child elem (ref) | 0..* |
| `outgoing` | `FlowNode` | `SequenceFlow` | child elem (ref) | 0..* |
| `lanes` | `FlowNode` | `Lane` | child elem (ref) | 0..* |
| `name` | `FlowElement` | `String` | attr | 0..1 |
| `auditing` | `FlowElement` | `Auditing` | child elem | 0..1 |
| `monitoring` | `FlowElement` | `Monitoring` | child elem | 0..1 |
| `categoryValueRef` | `FlowElement` | `CategoryValue` | child elem (ref) | 0..* |
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |
| `incomingConversationLinks` | `InteractionNode` | `ConversationLink` | child elem (ref) | 0..* |
| `outgoingConversationLinks` | `InteractionNode` | `ConversationLink` | child elem (ref) | 0..* |

---
