# Activities

_Structural metamodel for **Activities** elements, extracted from bpmn-moddle. In-scope per [../conformance.md](../conformance.md)._

_Kind legend: `attr` = XML attribute, `child elem` = XML child element, `(ref)` = ID reference to another element rather than embedded._

---

### Activity

_Abstract type._

**Type hierarchy:** `Activity → FlowNode → FlowElement → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `isForCompensation` | `Boolean` | attr | 0..1 | default `False` |
| `default` | `SequenceFlow` | attr (ref) | 0..1 |  |
| `ioSpecification` | `InputOutputSpecification` | child elem | 0..1 |  |
| `boundaryEventRefs` | `BoundaryEvent` | child elem (ref) | 0..* |  |
| `properties` | `Property` | child elem | 0..* |  |
| `dataInputAssociations` | `DataInputAssociation` | child elem | 0..* |  |
| `dataOutputAssociations` | `DataOutputAssociation` | child elem | 0..* |  |
| `startQuantity` | `Integer` | attr | 0..1 | default `1` |
| `resources` | `ResourceRole` | child elem | 0..* |  |
| `completionQuantity` | `Integer` | attr | 0..1 | default `1` |
| `loopCharacteristics` | `LoopCharacteristics` | child elem | 0..1 |  |

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

---

### Task

**Type hierarchy:** `Task → Activity → FlowNode → FlowElement → BaseElement → InteractionNode`

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `isForCompensation` | `Activity` | `Boolean` | attr | 0..1 |
| `default` | `Activity` | `SequenceFlow` | attr (ref) | 0..1 |
| `ioSpecification` | `Activity` | `InputOutputSpecification` | child elem | 0..1 |
| `boundaryEventRefs` | `Activity` | `BoundaryEvent` | child elem (ref) | 0..* |
| `properties` | `Activity` | `Property` | child elem | 0..* |
| `dataInputAssociations` | `Activity` | `DataInputAssociation` | child elem | 0..* |
| `dataOutputAssociations` | `Activity` | `DataOutputAssociation` | child elem | 0..* |
| `startQuantity` | `Activity` | `Integer` | attr | 0..1 |
| `resources` | `Activity` | `ResourceRole` | child elem | 0..* |
| `completionQuantity` | `Activity` | `Integer` | attr | 0..1 |
| `loopCharacteristics` | `Activity` | `LoopCharacteristics` | child elem | 0..1 |
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

### ServiceTask

**Type hierarchy:** `ServiceTask → Task → Activity → FlowNode → FlowElement → BaseElement → InteractionNode`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `implementation` | `String` | attr | 0..1 |  |
| `operationRef` | `Operation` | attr (ref) | 0..1 |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `isForCompensation` | `Activity` | `Boolean` | attr | 0..1 |
| `default` | `Activity` | `SequenceFlow` | attr (ref) | 0..1 |
| `ioSpecification` | `Activity` | `InputOutputSpecification` | child elem | 0..1 |
| `boundaryEventRefs` | `Activity` | `BoundaryEvent` | child elem (ref) | 0..* |
| `properties` | `Activity` | `Property` | child elem | 0..* |
| `dataInputAssociations` | `Activity` | `DataInputAssociation` | child elem | 0..* |
| `dataOutputAssociations` | `Activity` | `DataOutputAssociation` | child elem | 0..* |
| `startQuantity` | `Activity` | `Integer` | attr | 0..1 |
| `resources` | `Activity` | `ResourceRole` | child elem | 0..* |
| `completionQuantity` | `Activity` | `Integer` | attr | 0..1 |
| `loopCharacteristics` | `Activity` | `LoopCharacteristics` | child elem | 0..1 |
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

### UserTask

**Type hierarchy:** `UserTask → Task → Activity → FlowNode → FlowElement → BaseElement → InteractionNode`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `renderings` | `Rendering` | child elem | 0..* |  |
| `implementation` | `String` | attr | 0..1 |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `isForCompensation` | `Activity` | `Boolean` | attr | 0..1 |
| `default` | `Activity` | `SequenceFlow` | attr (ref) | 0..1 |
| `ioSpecification` | `Activity` | `InputOutputSpecification` | child elem | 0..1 |
| `boundaryEventRefs` | `Activity` | `BoundaryEvent` | child elem (ref) | 0..* |
| `properties` | `Activity` | `Property` | child elem | 0..* |
| `dataInputAssociations` | `Activity` | `DataInputAssociation` | child elem | 0..* |
| `dataOutputAssociations` | `Activity` | `DataOutputAssociation` | child elem | 0..* |
| `startQuantity` | `Activity` | `Integer` | attr | 0..1 |
| `resources` | `Activity` | `ResourceRole` | child elem | 0..* |
| `completionQuantity` | `Activity` | `Integer` | attr | 0..1 |
| `loopCharacteristics` | `Activity` | `LoopCharacteristics` | child elem | 0..1 |
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

### ManualTask

**Type hierarchy:** `ManualTask → Task → Activity → FlowNode → FlowElement → BaseElement → InteractionNode`

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `isForCompensation` | `Activity` | `Boolean` | attr | 0..1 |
| `default` | `Activity` | `SequenceFlow` | attr (ref) | 0..1 |
| `ioSpecification` | `Activity` | `InputOutputSpecification` | child elem | 0..1 |
| `boundaryEventRefs` | `Activity` | `BoundaryEvent` | child elem (ref) | 0..* |
| `properties` | `Activity` | `Property` | child elem | 0..* |
| `dataInputAssociations` | `Activity` | `DataInputAssociation` | child elem | 0..* |
| `dataOutputAssociations` | `Activity` | `DataOutputAssociation` | child elem | 0..* |
| `startQuantity` | `Activity` | `Integer` | attr | 0..1 |
| `resources` | `Activity` | `ResourceRole` | child elem | 0..* |
| `completionQuantity` | `Activity` | `Integer` | attr | 0..1 |
| `loopCharacteristics` | `Activity` | `LoopCharacteristics` | child elem | 0..1 |
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

### ScriptTask

**Type hierarchy:** `ScriptTask → Task → Activity → FlowNode → FlowElement → BaseElement → InteractionNode`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `scriptFormat` | `String` | attr | 0..1 |  |
| `script` | `String` | child elem | 0..1 |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `isForCompensation` | `Activity` | `Boolean` | attr | 0..1 |
| `default` | `Activity` | `SequenceFlow` | attr (ref) | 0..1 |
| `ioSpecification` | `Activity` | `InputOutputSpecification` | child elem | 0..1 |
| `boundaryEventRefs` | `Activity` | `BoundaryEvent` | child elem (ref) | 0..* |
| `properties` | `Activity` | `Property` | child elem | 0..* |
| `dataInputAssociations` | `Activity` | `DataInputAssociation` | child elem | 0..* |
| `dataOutputAssociations` | `Activity` | `DataOutputAssociation` | child elem | 0..* |
| `startQuantity` | `Activity` | `Integer` | attr | 0..1 |
| `resources` | `Activity` | `ResourceRole` | child elem | 0..* |
| `completionQuantity` | `Activity` | `Integer` | attr | 0..1 |
| `loopCharacteristics` | `Activity` | `LoopCharacteristics` | child elem | 0..1 |
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

### BusinessRuleTask

**Type hierarchy:** `BusinessRuleTask → Task → Activity → FlowNode → FlowElement → BaseElement → InteractionNode`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `implementation` | `String` | attr | 0..1 |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `isForCompensation` | `Activity` | `Boolean` | attr | 0..1 |
| `default` | `Activity` | `SequenceFlow` | attr (ref) | 0..1 |
| `ioSpecification` | `Activity` | `InputOutputSpecification` | child elem | 0..1 |
| `boundaryEventRefs` | `Activity` | `BoundaryEvent` | child elem (ref) | 0..* |
| `properties` | `Activity` | `Property` | child elem | 0..* |
| `dataInputAssociations` | `Activity` | `DataInputAssociation` | child elem | 0..* |
| `dataOutputAssociations` | `Activity` | `DataOutputAssociation` | child elem | 0..* |
| `startQuantity` | `Activity` | `Integer` | attr | 0..1 |
| `resources` | `Activity` | `ResourceRole` | child elem | 0..* |
| `completionQuantity` | `Activity` | `Integer` | attr | 0..1 |
| `loopCharacteristics` | `Activity` | `LoopCharacteristics` | child elem | 0..1 |
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

### SendTask

**Type hierarchy:** `SendTask → Task → Activity → FlowNode → FlowElement → BaseElement → InteractionNode`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `implementation` | `String` | attr | 0..1 |  |
| `operationRef` | `Operation` | attr (ref) | 0..1 |  |
| `messageRef` | `Message` | attr (ref) | 0..1 |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `isForCompensation` | `Activity` | `Boolean` | attr | 0..1 |
| `default` | `Activity` | `SequenceFlow` | attr (ref) | 0..1 |
| `ioSpecification` | `Activity` | `InputOutputSpecification` | child elem | 0..1 |
| `boundaryEventRefs` | `Activity` | `BoundaryEvent` | child elem (ref) | 0..* |
| `properties` | `Activity` | `Property` | child elem | 0..* |
| `dataInputAssociations` | `Activity` | `DataInputAssociation` | child elem | 0..* |
| `dataOutputAssociations` | `Activity` | `DataOutputAssociation` | child elem | 0..* |
| `startQuantity` | `Activity` | `Integer` | attr | 0..1 |
| `resources` | `Activity` | `ResourceRole` | child elem | 0..* |
| `completionQuantity` | `Activity` | `Integer` | attr | 0..1 |
| `loopCharacteristics` | `Activity` | `LoopCharacteristics` | child elem | 0..1 |
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

### ReceiveTask

**Type hierarchy:** `ReceiveTask → Task → Activity → FlowNode → FlowElement → BaseElement → InteractionNode`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `implementation` | `String` | attr | 0..1 |  |
| `instantiate` | `Boolean` | attr | 0..1 | default `False` |
| `operationRef` | `Operation` | attr (ref) | 0..1 |  |
| `messageRef` | `Message` | attr (ref) | 0..1 |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `isForCompensation` | `Activity` | `Boolean` | attr | 0..1 |
| `default` | `Activity` | `SequenceFlow` | attr (ref) | 0..1 |
| `ioSpecification` | `Activity` | `InputOutputSpecification` | child elem | 0..1 |
| `boundaryEventRefs` | `Activity` | `BoundaryEvent` | child elem (ref) | 0..* |
| `properties` | `Activity` | `Property` | child elem | 0..* |
| `dataInputAssociations` | `Activity` | `DataInputAssociation` | child elem | 0..* |
| `dataOutputAssociations` | `Activity` | `DataOutputAssociation` | child elem | 0..* |
| `startQuantity` | `Activity` | `Integer` | attr | 0..1 |
| `resources` | `Activity` | `ResourceRole` | child elem | 0..* |
| `completionQuantity` | `Activity` | `Integer` | attr | 0..1 |
| `loopCharacteristics` | `Activity` | `LoopCharacteristics` | child elem | 0..1 |
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

### SubProcess

**Type hierarchy:** `SubProcess → Activity → FlowNode → FlowElement → BaseElement → FlowElementsContainer → InteractionNode`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `triggeredByEvent` | `Boolean` | attr | 0..1 | default `False` |
| `artifacts` | `Artifact` | child elem | 0..* |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `isForCompensation` | `Activity` | `Boolean` | attr | 0..1 |
| `default` | `Activity` | `SequenceFlow` | attr (ref) | 0..1 |
| `ioSpecification` | `Activity` | `InputOutputSpecification` | child elem | 0..1 |
| `boundaryEventRefs` | `Activity` | `BoundaryEvent` | child elem (ref) | 0..* |
| `properties` | `Activity` | `Property` | child elem | 0..* |
| `dataInputAssociations` | `Activity` | `DataInputAssociation` | child elem | 0..* |
| `dataOutputAssociations` | `Activity` | `DataOutputAssociation` | child elem | 0..* |
| `startQuantity` | `Activity` | `Integer` | attr | 0..1 |
| `resources` | `Activity` | `ResourceRole` | child elem | 0..* |
| `completionQuantity` | `Activity` | `Integer` | attr | 0..1 |
| `loopCharacteristics` | `Activity` | `LoopCharacteristics` | child elem | 0..1 |
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
| `laneSets` | `FlowElementsContainer` | `LaneSet` | child elem | 0..* |
| `flowElements` | `FlowElementsContainer` | `FlowElement` | child elem | 0..* |
| `incomingConversationLinks` | `InteractionNode` | `ConversationLink` | child elem (ref) | 0..* |
| `outgoingConversationLinks` | `InteractionNode` | `ConversationLink` | child elem (ref) | 0..* |

---

### Transaction

**Type hierarchy:** `Transaction → SubProcess → Activity → FlowNode → FlowElement → BaseElement → FlowElementsContainer → InteractionNode`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `protocol` | `String` | attr | 0..1 |  |
| `method` | `String` | attr | 0..1 |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `triggeredByEvent` | `SubProcess` | `Boolean` | attr | 0..1 |
| `artifacts` | `SubProcess` | `Artifact` | child elem | 0..* |
| `isForCompensation` | `Activity` | `Boolean` | attr | 0..1 |
| `default` | `Activity` | `SequenceFlow` | attr (ref) | 0..1 |
| `ioSpecification` | `Activity` | `InputOutputSpecification` | child elem | 0..1 |
| `boundaryEventRefs` | `Activity` | `BoundaryEvent` | child elem (ref) | 0..* |
| `properties` | `Activity` | `Property` | child elem | 0..* |
| `dataInputAssociations` | `Activity` | `DataInputAssociation` | child elem | 0..* |
| `dataOutputAssociations` | `Activity` | `DataOutputAssociation` | child elem | 0..* |
| `startQuantity` | `Activity` | `Integer` | attr | 0..1 |
| `resources` | `Activity` | `ResourceRole` | child elem | 0..* |
| `completionQuantity` | `Activity` | `Integer` | attr | 0..1 |
| `loopCharacteristics` | `Activity` | `LoopCharacteristics` | child elem | 0..1 |
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
| `laneSets` | `FlowElementsContainer` | `LaneSet` | child elem | 0..* |
| `flowElements` | `FlowElementsContainer` | `FlowElement` | child elem | 0..* |
| `incomingConversationLinks` | `InteractionNode` | `ConversationLink` | child elem (ref) | 0..* |
| `outgoingConversationLinks` | `InteractionNode` | `ConversationLink` | child elem (ref) | 0..* |

---

### AdHocSubProcess

**Type hierarchy:** `AdHocSubProcess → SubProcess → Activity → FlowNode → FlowElement → BaseElement → FlowElementsContainer → InteractionNode`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `completionCondition` | `Expression` | child elem | 0..1 |  |
| `ordering` | `AdHocOrdering` | attr | 0..1 |  |
| `cancelRemainingInstances` | `Boolean` | attr | 0..1 | default `True` |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `triggeredByEvent` | `SubProcess` | `Boolean` | attr | 0..1 |
| `artifacts` | `SubProcess` | `Artifact` | child elem | 0..* |
| `isForCompensation` | `Activity` | `Boolean` | attr | 0..1 |
| `default` | `Activity` | `SequenceFlow` | attr (ref) | 0..1 |
| `ioSpecification` | `Activity` | `InputOutputSpecification` | child elem | 0..1 |
| `boundaryEventRefs` | `Activity` | `BoundaryEvent` | child elem (ref) | 0..* |
| `properties` | `Activity` | `Property` | child elem | 0..* |
| `dataInputAssociations` | `Activity` | `DataInputAssociation` | child elem | 0..* |
| `dataOutputAssociations` | `Activity` | `DataOutputAssociation` | child elem | 0..* |
| `startQuantity` | `Activity` | `Integer` | attr | 0..1 |
| `resources` | `Activity` | `ResourceRole` | child elem | 0..* |
| `completionQuantity` | `Activity` | `Integer` | attr | 0..1 |
| `loopCharacteristics` | `Activity` | `LoopCharacteristics` | child elem | 0..1 |
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
| `laneSets` | `FlowElementsContainer` | `LaneSet` | child elem | 0..* |
| `flowElements` | `FlowElementsContainer` | `FlowElement` | child elem | 0..* |
| `incomingConversationLinks` | `InteractionNode` | `ConversationLink` | child elem (ref) | 0..* |
| `outgoingConversationLinks` | `InteractionNode` | `ConversationLink` | child elem (ref) | 0..* |

---

### CallActivity

**Type hierarchy:** `CallActivity → Activity → FlowNode → FlowElement → BaseElement → InteractionNode`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `calledElement` | `String` | attr | 0..1 |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `isForCompensation` | `Activity` | `Boolean` | attr | 0..1 |
| `default` | `Activity` | `SequenceFlow` | attr (ref) | 0..1 |
| `ioSpecification` | `Activity` | `InputOutputSpecification` | child elem | 0..1 |
| `boundaryEventRefs` | `Activity` | `BoundaryEvent` | child elem (ref) | 0..* |
| `properties` | `Activity` | `Property` | child elem | 0..* |
| `dataInputAssociations` | `Activity` | `DataInputAssociation` | child elem | 0..* |
| `dataOutputAssociations` | `Activity` | `DataOutputAssociation` | child elem | 0..* |
| `startQuantity` | `Activity` | `Integer` | attr | 0..1 |
| `resources` | `Activity` | `ResourceRole` | child elem | 0..* |
| `completionQuantity` | `Activity` | `Integer` | attr | 0..1 |
| `loopCharacteristics` | `Activity` | `LoopCharacteristics` | child elem | 0..1 |
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

### LoopCharacteristics

_Abstract type._

**Type hierarchy:** `LoopCharacteristics → BaseElement`

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### StandardLoopCharacteristics

**Type hierarchy:** `StandardLoopCharacteristics → LoopCharacteristics → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `testBefore` | `Boolean` | attr | 0..1 | default `False` |
| `loopCondition` | `Expression` | child elem | 0..1 |  |
| `loopMaximum` | `Integer` | attr | 0..1 |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### MultiInstanceLoopCharacteristics

**Type hierarchy:** `MultiInstanceLoopCharacteristics → LoopCharacteristics → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `isSequential` | `Boolean` | attr | 0..1 | default `False` |
| `behavior` | `MultiInstanceBehavior` | attr | 0..1 | default `All` |
| `loopCardinality` | `Expression` | child elem | 0..1 |  |
| `loopDataInputRef` | `ItemAwareElement` | child elem (ref) | 0..1 |  |
| `loopDataOutputRef` | `ItemAwareElement` | child elem (ref) | 0..1 |  |
| `inputDataItem` | `DataInput` | child elem | 0..1 |  |
| `outputDataItem` | `DataOutput` | child elem | 0..1 |  |
| `complexBehaviorDefinition` | `ComplexBehaviorDefinition` | child elem | 0..* |  |
| `completionCondition` | `Expression` | child elem | 0..1 |  |
| `oneBehaviorEventRef` | `EventDefinition` | attr (ref) | 0..1 |  |
| `noneBehaviorEventRef` | `EventDefinition` | attr (ref) | 0..1 |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### ComplexBehaviorDefinition

**Type hierarchy:** `ComplexBehaviorDefinition → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `condition` | `FormalExpression` | child elem | 0..1 |  |
| `event` | `ImplicitThrowEvent` | child elem | 0..1 |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### GlobalTask

**Type hierarchy:** `GlobalTask → CallableElement → RootElement → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `resources` | `ResourceRole` | child elem | 0..* |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `name` | `CallableElement` | `String` | attr | 0..1 |
| `ioSpecification` | `CallableElement` | `InputOutputSpecification` | child elem | 0..1 |
| `supportedInterfaceRef` | `CallableElement` | `Interface` | child elem (ref) | 0..* |
| `ioBinding` | `CallableElement` | `InputOutputBinding` | child elem | 0..* |
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### GlobalManualTask

**Type hierarchy:** `GlobalManualTask → GlobalTask → CallableElement → RootElement → BaseElement`

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `resources` | `GlobalTask` | `ResourceRole` | child elem | 0..* |
| `name` | `CallableElement` | `String` | attr | 0..1 |
| `ioSpecification` | `CallableElement` | `InputOutputSpecification` | child elem | 0..1 |
| `supportedInterfaceRef` | `CallableElement` | `Interface` | child elem (ref) | 0..* |
| `ioBinding` | `CallableElement` | `InputOutputBinding` | child elem | 0..* |
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### GlobalUserTask

**Type hierarchy:** `GlobalUserTask → GlobalTask → CallableElement → RootElement → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `implementation` | `String` | attr | 0..1 |  |
| `renderings` | `Rendering` | child elem | 0..* |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `resources` | `GlobalTask` | `ResourceRole` | child elem | 0..* |
| `name` | `CallableElement` | `String` | attr | 0..1 |
| `ioSpecification` | `CallableElement` | `InputOutputSpecification` | child elem | 0..1 |
| `supportedInterfaceRef` | `CallableElement` | `Interface` | child elem (ref) | 0..* |
| `ioBinding` | `CallableElement` | `InputOutputBinding` | child elem | 0..* |
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### GlobalScriptTask

**Type hierarchy:** `GlobalScriptTask → GlobalTask → CallableElement → RootElement → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `scriptLanguage` | `String` | attr | 0..1 |  |
| `script` | `String` | attr | 0..1 |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `resources` | `GlobalTask` | `ResourceRole` | child elem | 0..* |
| `name` | `CallableElement` | `String` | attr | 0..1 |
| `ioSpecification` | `CallableElement` | `InputOutputSpecification` | child elem | 0..1 |
| `supportedInterfaceRef` | `CallableElement` | `Interface` | child elem (ref) | 0..* |
| `ioBinding` | `CallableElement` | `InputOutputBinding` | child elem | 0..* |
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### GlobalBusinessRuleTask

**Type hierarchy:** `GlobalBusinessRuleTask → GlobalTask → CallableElement → RootElement → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `implementation` | `String` | attr | 0..1 |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `resources` | `GlobalTask` | `ResourceRole` | child elem | 0..* |
| `name` | `CallableElement` | `String` | attr | 0..1 |
| `ioSpecification` | `CallableElement` | `InputOutputSpecification` | child elem | 0..1 |
| `supportedInterfaceRef` | `CallableElement` | `Interface` | child elem (ref) | 0..* |
| `ioBinding` | `CallableElement` | `InputOutputBinding` | child elem | 0..* |
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---
