# Human Interaction

_Structural metamodel for **Human Interaction** elements, extracted from bpmn-moddle. In-scope per [../conformance.md](../conformance.md)._

_Kind legend: `attr` = XML attribute, `child elem` = XML child element, `(ref)` = ID reference to another element rather than embedded._

---

### HumanPerformer

**Type hierarchy:** `HumanPerformer → Performer → ResourceRole → BaseElement`

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `resourceRef` | `ResourceRole` | `Resource` | child elem (ref) | 0..1 |
| `resourceParameterBindings` | `ResourceRole` | `ResourceParameterBinding` | child elem | 0..* |
| `resourceAssignmentExpression` | `ResourceRole` | `ResourceAssignmentExpression` | child elem | 0..1 |
| `name` | `ResourceRole` | `String` | attr | 0..1 |
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### PotentialOwner

**Type hierarchy:** `PotentialOwner → HumanPerformer → Performer → ResourceRole → BaseElement`

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `resourceRef` | `ResourceRole` | `Resource` | child elem (ref) | 0..1 |
| `resourceParameterBindings` | `ResourceRole` | `ResourceParameterBinding` | child elem | 0..* |
| `resourceAssignmentExpression` | `ResourceRole` | `ResourceAssignmentExpression` | child elem | 0..1 |
| `name` | `ResourceRole` | `String` | attr | 0..1 |
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### Performer

**Type hierarchy:** `Performer → ResourceRole → BaseElement`

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `resourceRef` | `ResourceRole` | `Resource` | child elem (ref) | 0..1 |
| `resourceParameterBindings` | `ResourceRole` | `ResourceParameterBinding` | child elem | 0..* |
| `resourceAssignmentExpression` | `ResourceRole` | `ResourceAssignmentExpression` | child elem | 0..1 |
| `name` | `ResourceRole` | `String` | attr | 0..1 |
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### Rendering

**Type hierarchy:** `Rendering → BaseElement`

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### Resource

**Type hierarchy:** `Resource → RootElement → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `name` | `String` | attr | 0..1 |  |
| `resourceParameters` | `ResourceParameter` | child elem | 0..* |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### ResourceRole

**Type hierarchy:** `ResourceRole → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `resourceRef` | `Resource` | child elem (ref) | 0..1 |  |
| `resourceParameterBindings` | `ResourceParameterBinding` | child elem | 0..* |  |
| `resourceAssignmentExpression` | `ResourceAssignmentExpression` | child elem | 0..1 |  |
| `name` | `String` | attr | 0..1 |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### ResourceParameter

**Type hierarchy:** `ResourceParameter → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `name` | `String` | attr | 0..1 |  |
| `isRequired` | `Boolean` | attr | 0..1 |  |
| `type` | `ItemDefinition` | attr (ref) | 0..1 |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### ResourceParameterBinding

**Type hierarchy:** `ResourceParameterBinding → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `expression` | `Expression` | child elem | 0..1 |  |
| `parameterRef` | `ResourceParameter` | attr (ref) | 0..1 |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---

### ResourceAssignmentExpression

**Type hierarchy:** `ResourceAssignmentExpression → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `expression` | `Expression` | child elem | 0..1 |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `id` | `BaseElement` | `String` | attr | 0..1 |
| `documentation` | `BaseElement` | `Documentation` | child elem | 0..* |
| `extensionDefinitions` | `BaseElement` | `ExtensionDefinition` | child elem (ref) | 0..* |
| `extensionElements` | `BaseElement` | `ExtensionElements` | child elem | 0..1 |

---
