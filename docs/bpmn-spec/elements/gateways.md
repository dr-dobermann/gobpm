# Gateways

_Structural metamodel for **Gateways** elements, extracted from bpmn-moddle. In-scope per [../conformance.md](../conformance.md)._

_Kind legend: `attr` = XML attribute, `child elem` = XML child element, `(ref)` = ID reference to another element rather than embedded._

---

### Gateway

_Abstract type._

**Type hierarchy:** `Gateway → FlowNode → FlowElement → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `gatewayDirection` | `GatewayDirection` | attr | 0..1 | default `Unspecified` |

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

### ExclusiveGateway

**Type hierarchy:** `ExclusiveGateway → Gateway → FlowNode → FlowElement → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `default` | `SequenceFlow` | attr (ref) | 0..1 |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `gatewayDirection` | `Gateway` | `GatewayDirection` | attr | 0..1 |
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

### ParallelGateway

**Type hierarchy:** `ParallelGateway → Gateway → FlowNode → FlowElement → BaseElement`

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `gatewayDirection` | `Gateway` | `GatewayDirection` | attr | 0..1 |
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

### InclusiveGateway

**Type hierarchy:** `InclusiveGateway → Gateway → FlowNode → FlowElement → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `default` | `SequenceFlow` | attr (ref) | 0..1 |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `gatewayDirection` | `Gateway` | `GatewayDirection` | attr | 0..1 |
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

### EventBasedGateway

**Type hierarchy:** `EventBasedGateway → Gateway → FlowNode → FlowElement → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `instantiate` | `Boolean` | attr | 0..1 | default `False` |
| `eventGatewayType` | `EventBasedGatewayType` | attr | 0..1 | default `Exclusive` |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `gatewayDirection` | `Gateway` | `GatewayDirection` | attr | 0..1 |
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

### ComplexGateway

**Type hierarchy:** `ComplexGateway → Gateway → FlowNode → FlowElement → BaseElement`

**Own properties:**

| Name | Type | Kind | Card | Notes |
|---|---|---|---|---|
| `activationCondition` | `Expression` | child elem | 0..1 |  |
| `default` | `SequenceFlow` | attr (ref) | 0..1 |  |

**Inherited properties** (see ancestor pages for details):

| Name | From | Type | Kind | Card |
|---|---|---|---|---|
| `gatewayDirection` | `Gateway` | `GatewayDirection` | attr | 0..1 |
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
