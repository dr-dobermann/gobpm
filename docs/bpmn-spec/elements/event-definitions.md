# Event Definitions

_Structural metamodel for **Event Definitions** elements, extracted from bpmn-moddle. In-scope per [../conformance.md](../conformance.md)._

_Kind legend: `attr` = XML attribute, `child elem` = XML child element, `(ref)` = ID reference to another element rather than embedded._

---

### EventDefinition

_Abstract type._

**Type hierarchy:** `EventDefinition → RootElement → BaseElement`

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### MessageEventDefinition

**Type hierarchy:** `MessageEventDefinition → EventDefinition → RootElement → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `messageRef` | `Message` | attr (ref) | 0..1 |  |
| `operationRef` | `Operation` | child elem (ref) | 0..1 |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### TimerEventDefinition

**Type hierarchy:** `TimerEventDefinition → EventDefinition → RootElement → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `timeDate` | `Expression` | child elem | 0..1 |  |
| `timeCycle` | `Expression` | child elem | 0..1 |  |
| `timeDuration` | `Expression` | child elem | 0..1 |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### SignalEventDefinition

**Type hierarchy:** `SignalEventDefinition → EventDefinition → RootElement → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `signalRef` | `Signal` | attr (ref) | 0..1 |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### ErrorEventDefinition

**Type hierarchy:** `ErrorEventDefinition → EventDefinition → RootElement → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `errorRef` | `Error` | attr (ref) | 0..1 |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### EscalationEventDefinition

**Type hierarchy:** `EscalationEventDefinition → EventDefinition → RootElement → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `escalationRef` | `Escalation` | attr (ref) | 0..1 |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### CompensateEventDefinition

**Type hierarchy:** `CompensateEventDefinition → EventDefinition → RootElement → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `waitForCompletion` | `Boolean` | attr | 0..1 | default `True` |
| `activityRef` | `Activity` | attr (ref) | 0..1 |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### CancelEventDefinition

**Type hierarchy:** `CancelEventDefinition → EventDefinition → RootElement → BaseElement`

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### ConditionalEventDefinition

**Type hierarchy:** `ConditionalEventDefinition → EventDefinition → RootElement → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `condition` | `Expression` | child elem | 0..1 |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### LinkEventDefinition

**Type hierarchy:** `LinkEventDefinition → EventDefinition → RootElement → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `name` | `String` | attr | 0..1 |  |
| `target` | `LinkEventDefinition` | child elem (ref) | 0..1 |  |
| `source` | `LinkEventDefinition` | child elem (ref) | 0..* |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### TerminateEventDefinition

**Type hierarchy:** `TerminateEventDefinition → EventDefinition → RootElement → BaseElement`

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### Message

**Type hierarchy:** `Message → RootElement → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `name` | `String` | attr | 0..1 |  |
| `itemRef` | `ItemDefinition` | attr (ref) | 0..1 |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### Signal

**Type hierarchy:** `Signal → RootElement → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `structureRef` | `ItemDefinition` | attr (ref) | 0..1 |  |
| `name` | `String` | attr | 0..1 |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### Error

**Type hierarchy:** `Error → RootElement → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `structureRef` | `ItemDefinition` | attr (ref) | 0..1 |  |
| `name` | `String` | attr | 0..1 |  |
| `errorCode` | `String` | attr | 0..1 |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### Escalation

**Type hierarchy:** `Escalation → RootElement → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `structureRef` | `ItemDefinition` | attr (ref) | 0..1 |  |
| `name` | `String` | attr | 0..1 |  |
| `escalationCode` | `String` | attr | 0..1 |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---
