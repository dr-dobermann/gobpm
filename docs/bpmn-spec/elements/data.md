# Data

_Structural metamodel for **Data** elements, extracted from bpmn-moddle. In-scope per [../conformance.md](../conformance.md)._

_Kind legend: `attr` = XML attribute, `child elem` = XML child element, `(ref)` = ID reference to another element rather than embedded._

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

### DataObject

**Type hierarchy:** `DataObject → FlowElement → BaseElement → ItemAwareElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `isCollection` | `Boolean` | attr | 0..1 | default `False` |

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
| `itemSubjectRef` | `ItemAwareElement` | `ItemDefinition` | attr (ref) | 0..1 |
| `dataState` | `ItemAwareElement` | `DataState` | child elem | 0..1 |

---

### DataObjectReference

**Type hierarchy:** `DataObjectReference → ItemAwareElement → BaseElement → FlowElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `dataObjectRef` | `DataObject` | attr (ref) | 0..1 |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `itemSubjectRef` | `ItemAwareElement` | `ItemDefinition` | attr (ref) | 0..1 |
| `dataState` | `ItemAwareElement` | `DataState` | child elem | 0..1 |
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |
| `name` | `FlowElement` | `String` | attr | 0..1 |
| `auditing` | `FlowElement` | `Auditing` | child elem | 0..1 |
| `monitoring` | `FlowElement` | `Monitoring` | child elem | 0..1 |
| `categoryValueRef` | `FlowElement` | `CategoryValue` | child elem (ref) | 0..* |

---

### DataStore

**Type hierarchy:** `DataStore → RootElement → BaseElement → ItemAwareElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `name` | `String` | attr | 0..1 |  |
| `capacity` | `Integer` | attr | 0..1 |  |
| `isUnlimited` | `Boolean` | attr | 0..1 | default `True` |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |
| `itemSubjectRef` | `ItemAwareElement` | `ItemDefinition` | attr (ref) | 0..1 |
| `dataState` | `ItemAwareElement` | `DataState` | child elem | 0..1 |

---

### DataStoreReference

**Type hierarchy:** `DataStoreReference → ItemAwareElement → BaseElement → FlowElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `dataStoreRef` | `DataStore` | attr (ref) | 0..1 |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `itemSubjectRef` | `ItemAwareElement` | `ItemDefinition` | attr (ref) | 0..1 |
| `dataState` | `ItemAwareElement` | `DataState` | child elem | 0..1 |
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |
| `name` | `FlowElement` | `String` | attr | 0..1 |
| `auditing` | `FlowElement` | `Auditing` | child elem | 0..1 |
| `monitoring` | `FlowElement` | `Monitoring` | child elem | 0..1 |
| `categoryValueRef` | `FlowElement` | `CategoryValue` | child elem (ref) | 0..* |

---

### Property

**Type hierarchy:** `Property → ItemAwareElement → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `name` | `String` | attr | 0..1 |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `itemSubjectRef` | `ItemAwareElement` | `ItemDefinition` | attr (ref) | 0..1 |
| `dataState` | `ItemAwareElement` | `DataState` | child elem | 0..1 |
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### DataInput

**Type hierarchy:** `DataInput → ItemAwareElement → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `name` | `String` | attr | 0..1 |  |
| `isCollection` | `Boolean` | attr | 0..1 | default `False` |
| `inputSetRef` | `InputSet` | child elem (ref) | 0..* |  |
| `inputSetWithOptional` | `InputSet` | child elem (ref) | 0..* |  |
| `inputSetWithWhileExecuting` | `InputSet` | child elem (ref) | 0..* |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `itemSubjectRef` | `ItemAwareElement` | `ItemDefinition` | attr (ref) | 0..1 |
| `dataState` | `ItemAwareElement` | `DataState` | child elem | 0..1 |
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### DataOutput

**Type hierarchy:** `DataOutput → ItemAwareElement → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `name` | `String` | attr | 0..1 |  |
| `isCollection` | `Boolean` | attr | 0..1 | default `False` |
| `outputSetRef` | `OutputSet` | child elem (ref) | 0..* |  |
| `outputSetWithOptional` | `OutputSet` | child elem (ref) | 0..* |  |
| `outputSetWithWhileExecuting` | `OutputSet` | child elem (ref) | 0..* |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `itemSubjectRef` | `ItemAwareElement` | `ItemDefinition` | attr (ref) | 0..1 |
| `dataState` | `ItemAwareElement` | `DataState` | child elem | 0..1 |
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### DataInputAssociation

**Type hierarchy:** `DataInputAssociation → DataAssociation → BaseElement`

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `sourceRef` | `DataAssociation` | `ItemAwareElement` | child elem (ref) | 0..* |
| `targetRef` | `DataAssociation` | `ItemAwareElement` | child elem (ref) | 0..1 |
| `transformation` | `DataAssociation` | `FormalExpression` | child elem | 0..1 |
| `assignment` | `DataAssociation` | `Assignment` | child elem | 0..* |
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### DataOutputAssociation

**Type hierarchy:** `DataOutputAssociation → DataAssociation → BaseElement`

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `sourceRef` | `DataAssociation` | `ItemAwareElement` | child elem (ref) | 0..* |
| `targetRef` | `DataAssociation` | `ItemAwareElement` | child elem (ref) | 0..1 |
| `transformation` | `DataAssociation` | `FormalExpression` | child elem | 0..1 |
| `assignment` | `DataAssociation` | `Assignment` | child elem | 0..* |
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### DataAssociation

**Type hierarchy:** `DataAssociation → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `sourceRef` | `ItemAwareElement` | child elem (ref) | 0..* |  |
| `targetRef` | `ItemAwareElement` | child elem (ref) | 0..1 |  |
| `transformation` | `FormalExpression` | child elem | 0..1 |  |
| `assignment` | `Assignment` | child elem | 0..* |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### InputSet

**Type hierarchy:** `InputSet → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `name` | `String` | attr | 0..1 |  |
| `dataInputRefs` | `DataInput` | child elem (ref) | 0..* |  |
| `optionalInputRefs` | `DataInput` | child elem (ref) | 0..* |  |
| `whileExecutingInputRefs` | `DataInput` | child elem (ref) | 0..* |  |
| `outputSetRefs` | `OutputSet` | child elem (ref) | 0..* |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### OutputSet

**Type hierarchy:** `OutputSet → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `dataOutputRefs` | `DataOutput` | child elem (ref) | 0..* |  |
| `name` | `String` | attr | 0..1 |  |
| `inputSetRefs` | `InputSet` | child elem (ref) | 0..* |  |
| `optionalOutputRefs` | `DataOutput` | child elem (ref) | 0..* |  |
| `whileExecutingOutputRefs` | `DataOutput` | child elem (ref) | 0..* |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### InputOutputSpecification

**Type hierarchy:** `InputOutputSpecification → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `dataInputs` | `DataInput` | child elem | 0..* |  |
| `dataOutputs` | `DataOutput` | child elem | 0..* |  |
| `inputSets` | `InputSet` | child elem | 0..* |  |
| `outputSets` | `OutputSet` | child elem | 0..* |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### InputOutputBinding

**Type hierarchy:** `InputOutputBinding`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `inputDataRef` | `InputSet` | attr (ref) | 0..1 |  |
| `outputDataRef` | `OutputSet` | attr (ref) | 0..1 |  |
| `operationRef` | `Operation` | attr (ref) | 0..1 |  |

---

### Assignment

**Type hierarchy:** `Assignment → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `from` | `Expression` | child elem | 0..1 |  |
| `to` | `Expression` | child elem | 0..1 |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### DataState

**Type hierarchy:** `DataState → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `name` | `String` | attr | 0..1 |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---
