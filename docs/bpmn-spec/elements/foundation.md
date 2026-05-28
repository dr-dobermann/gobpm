# Foundation

_Structural metamodel for **Foundation** elements, extracted from bpmn-moddle. In-scope per [../conformance.md](../conformance.md)._

_Kind legend: `attr` = XML attribute, `child elem` = XML child element, `(ref)` = ID reference to another element rather than embedded._

---

### Definitions

**Type hierarchy:** `Definitions → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `name` | `String` | attr | 0..1 |  |
| `targetNamespace` | `String` | attr | 0..1 |  |
| `expressionLanguage` | `String` | attr | 0..1 | default `http://www.w3.org/1999/XPath` |
| `typeLanguage` | `String` | attr | 0..1 | default `http://www.w3.org/2001/XMLSchema` |
| `imports` | `Import` | child elem | 0..* |  |
| `extensions` | `Extension` | child elem | 0..* |  |
| `rootElements` | `RootElement` | child elem | 0..* |  |
| `diagrams` | `bpmndi:BPMNDiagram` | child elem | 0..* |  |
| `exporter` | `String` | attr | 0..1 |  |
| `relationships` | `Relationship` | child elem | 0..* |  |
| `exporterVersion` | `String` | attr | 0..1 |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### BaseElement

_Abstract type._

**Type hierarchy:** `BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `id` | `String` | attr | 0..1 | id |
| `documentation` | `Documentation` | child elem | 0..* |  |
| `extensionDefinitions` | `ExtensionDefinition` | child elem (ref) | 0..* |  |
| `extensionElements` | `ExtensionElements` | child elem | 0..1 |  |

---

### RootElement

_Abstract type._

**Type hierarchy:** `RootElement → BaseElement`

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### FlowElement

_Abstract type._

**Type hierarchy:** `FlowElement → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `name` | `String` | attr | 0..1 |  |
| `auditing` | `Auditing` | child elem | 0..1 |  |
| `monitoring` | `Monitoring` | child elem | 0..1 |  |
| `categoryValueRef` | `CategoryValue` | child elem (ref) | 0..* |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### FlowNode

_Abstract type._

**Type hierarchy:** `FlowNode → FlowElement → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `incoming` | `SequenceFlow` | child elem (ref) | 0..* |  |
| `outgoing` | `SequenceFlow` | child elem (ref) | 0..* |  |
| `lanes` | `Lane` | child elem (ref) | 0..* |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `name` | `FlowElement` | `String` | attr | 0..1 |
| `auditing` | `FlowElement` | `Auditing` | child elem | 0..1 |
| `monitoring` | `FlowElement` | `Monitoring` | child elem | 0..1 |
| `categoryValueRef` | `FlowElement` | `CategoryValue` | child elem (ref) | 0..* |
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### FlowElementsContainer

_Abstract type._

**Type hierarchy:** `FlowElementsContainer → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `laneSets` | `LaneSet` | child elem | 0..* |  |
| `flowElements` | `FlowElement` | child elem | 0..* |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### CallableElement

_Abstract type._

**Type hierarchy:** `CallableElement → RootElement → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `name` | `String` | attr | 0..1 |  |
| `ioSpecification` | `InputOutputSpecification` | child elem | 0..1 |  |
| `supportedInterfaceRef` | `Interface` | child elem (ref) | 0..* |  |
| `ioBinding` | `InputOutputBinding` | child elem | 0..* |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### ItemAwareElement

**Type hierarchy:** `ItemAwareElement → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `itemSubjectRef` | `ItemDefinition` | attr (ref) | 0..1 |  |
| `dataState` | `DataState` | child elem | 0..1 |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### ItemDefinition

**Type hierarchy:** `ItemDefinition → RootElement → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `itemKind` | `ItemKind` | attr | 0..1 |  |
| `structureRef` | `String` | attr | 0..1 |  |
| `isCollection` | `Boolean` | attr | 0..1 | default `False` |
| `import` | `Import` | attr (ref) | 0..1 |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### Expression

**Type hierarchy:** `Expression → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `body` | `String` | child elem | 0..1 |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### FormalExpression

**Type hierarchy:** `FormalExpression → Expression → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `language` | `String` | attr | 0..1 |  |
| `evaluatesToTypeRef` | `ItemDefinition` | attr (ref) | 0..1 |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `body` | `Expression` | `String` | child elem | 0..1 |
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### Documentation

**Type hierarchy:** `Documentation → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `text` | `String` | child elem | 0..1 |  |
| `textFormat` | `String` | attr | 0..1 | default `text/plain` |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### Extension

**Type hierarchy:** `Extension`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `mustUnderstand` | `Boolean` | attr | 0..1 | default `False` |
| `definition` | `ExtensionDefinition` | attr (ref) | 0..1 |  |

---

### ExtensionDefinition

**Type hierarchy:** `ExtensionDefinition`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `name` | `String` | attr | 0..1 |  |
| `extensionAttributeDefinitions` | `ExtensionAttributeDefinition` | child elem | 0..* |  |

---

### ExtensionAttributeDefinition

**Type hierarchy:** `ExtensionAttributeDefinition`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `name` | `String` | attr | 0..1 |  |
| `type` | `String` | attr | 0..1 |  |
| `isReference` | `Boolean` | attr | 0..1 | default `False` |
| `extensionDefinition` | `ExtensionDefinition` | attr (ref) | 0..1 |  |

---

### ExtensionElements

**Type hierarchy:** `ExtensionElements`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `valueRef` | `Element` | attr (ref) | 0..1 |  |
| `values` | `Element` | child elem | 0..* |  |
| `extensionAttributeDefinition` | `ExtensionAttributeDefinition` | attr (ref) | 0..1 |  |

---

### Import

**Type hierarchy:** `Import`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `importType` | `String` | attr | 0..1 |  |
| `location` | `String` | attr | 0..1 |  |
| `namespace` | `String` | attr | 0..1 |  |

---

### Auditing

**Type hierarchy:** `Auditing → BaseElement`

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### Monitoring

**Type hierarchy:** `Monitoring → BaseElement`

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---
