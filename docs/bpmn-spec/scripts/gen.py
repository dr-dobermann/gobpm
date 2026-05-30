#!/usr/bin/env python3
"""
Generate per-element markdown KB files under docs/bpmn-spec/elements/
from the bpmn-moddle JSON descriptor (bpmn-moddle.json in this dir).

Scope is restricted to BPMN 2.0 Process Execution Conformance (Common
Executable Subclass per spec §2.1.3) — see ../conformance.md.

Re-run after updating bpmn-moddle.json. Output files are overwritten.
"""

import json
import sys
from pathlib import Path
from collections import OrderedDict

HERE = Path(__file__).resolve().parent
ROOT = HERE.parent
ELEMENTS_DIR = ROOT / "elements"

# In-scope types grouped by output file. Order within each group is the
# emission order in the markdown. See ../conformance.md for rationale.
GROUPS = OrderedDict([
    ("foundation", [
        "Definitions", "BaseElement", "RootElement",
        "FlowElement", "FlowNode", "FlowElementsContainer", "CallableElement",
        "ItemAwareElement", "ItemDefinition",
        "Expression", "FormalExpression",
        "Documentation",
        "Extension", "ExtensionDefinition", "ExtensionAttributeDefinition", "ExtensionElements",
        "Import", "Auditing", "Monitoring",
    ]),
    ("process", [
        "Process", "Lane", "LaneSet",
    ]),
    ("activities", [
        "Activity",
        "Task", "ServiceTask", "UserTask", "ManualTask",
        "ScriptTask", "BusinessRuleTask", "SendTask", "ReceiveTask",
        "SubProcess", "Transaction", "AdHocSubProcess", "CallActivity",
        "LoopCharacteristics", "StandardLoopCharacteristics",
        "MultiInstanceLoopCharacteristics", "ComplexBehaviorDefinition",
        "GlobalTask", "GlobalManualTask", "GlobalUserTask",
        "GlobalScriptTask", "GlobalBusinessRuleTask",
    ]),
    ("events", [
        "Event", "CatchEvent", "ThrowEvent",
        "StartEvent", "IntermediateCatchEvent", "IntermediateThrowEvent",
        "EndEvent", "BoundaryEvent", "ImplicitThrowEvent",
    ]),
    ("event-definitions", [
        "EventDefinition",
        "MessageEventDefinition", "TimerEventDefinition", "SignalEventDefinition",
        "ErrorEventDefinition", "EscalationEventDefinition",
        "CompensateEventDefinition", "CancelEventDefinition",
        "ConditionalEventDefinition", "LinkEventDefinition",
        "TerminateEventDefinition",
        "Message", "Signal", "Error", "Escalation",
    ]),
    ("gateways", [
        "Gateway",
        "ExclusiveGateway", "ParallelGateway",
        "InclusiveGateway", "EventBasedGateway",
        "ComplexGateway",
    ]),
    ("flows", [
        "SequenceFlow", "Association",
    ]),
    ("data", [
        "ItemAwareElement",
        "DataObject", "DataObjectReference",
        "DataStore", "DataStoreReference",
        "Property",
        "DataInput", "DataOutput",
        "DataInputAssociation", "DataOutputAssociation", "DataAssociation",
        "InputSet", "OutputSet",
        "InputOutputSpecification", "InputOutputBinding",
        "Assignment", "DataState",
    ]),
    ("service-interfaces", [
        "Interface", "Operation", "EndPoint",
    ]),
    ("human-interaction", [
        "HumanPerformer", "PotentialOwner", "Performer", "Rendering",
        "Resource", "ResourceRole", "ResourceParameter",
        "ResourceParameterBinding", "ResourceAssignmentExpression",
    ]),
    ("correlation", [
        "CorrelationKey", "CorrelationProperty",
        "CorrelationPropertyRetrievalExpression",
        "CorrelationPropertyBinding", "CorrelationSubscription",
    ]),
])

# Group titles for the markdown file headings.
GROUP_TITLES = {
    "foundation": "Foundation",
    "process": "Process / Container",
    "activities": "Activities",
    "events": "Events",
    "event-definitions": "Event Definitions",
    "gateways": "Gateways",
    "flows": "Flows",
    "data": "Data",
    "service-interfaces": "Service Interfaces",
    "human-interaction": "Human Interaction",
    "correlation": "Message Correlation",
}


def load_descriptor() -> dict:
    with (HERE / "bpmn-moddle.json").open() as f:
        return json.load(f)


def index_types(descriptor: dict) -> dict:
    """Map type name -> type descriptor."""
    return {t["name"]: t for t in descriptor["types"]}


def walk_supers(types: dict, name: str, seen=None) -> list:
    """Return ancestor chain (excluding self) in MRO-ish order: direct supers first, then their supers."""
    if seen is None:
        seen = set()
    if name not in types:
        return []
    chain = []
    for s in types[name].get("superClass", []):
        if s in seen:
            continue
        seen.add(s)
        chain.append(s)
        chain.extend(walk_supers(types, s, seen))
    return chain


def aggregate_properties(types: dict, name: str) -> list:
    """Collect (origin, property) pairs, with `replaces` honored.

    Walks self + all ancestors. A property with `replaces: <Origin>#<name>`
    on a descendant suppresses the matching ancestor property.
    """
    if name not in types:
        return []
    own = [("self", p) for p in types[name].get("properties", [])]
    inherited = []
    for ancestor in walk_supers(types, name):
        for p in types[ancestor].get("properties", []):
            inherited.append((ancestor, p))

    # Honor `replaces`: if any descendant property has replaces="X#prop", drop X's prop.
    replaced_keys = set()
    for _, p in own:
        if "replaces" in p:
            replaced_keys.add(p["replaces"])
    inherited = [
        (o, p) for (o, p) in inherited
        if f"{o}#{p['name']}" not in replaced_keys
    ]
    return own + inherited


def kind(prop: dict) -> str:
    """Classify property as attribute / element / reference."""
    if prop.get("isAttr"):
        return "attr (ref)" if prop.get("isReference") else "attr"
    return "child elem (ref)" if prop.get("isReference") else "child elem"


def card(prop: dict) -> str:
    """0..1 or 0..*."""
    return "0..*" if prop.get("isMany") else "0..1"


def fmt_type(prop: dict) -> str:
    return prop.get("type", "?")


def render_element(name: str, types: dict) -> str:
    """Render one element as a markdown section."""
    if name not in types:
        return f"### {name}\n\n_Not found in bpmn-moddle descriptor._\n\n"

    t = types[name]
    supers = walk_supers(types, name)
    super_chain = " → ".join([name] + supers) if supers else name

    lines = [f"### {name}", ""]

    if t.get("isAbstract"):
        lines.append("_Abstract type._")
        lines.append("")

    lines.append(f"**Type hierarchy:** `{super_chain}`")
    lines.append("")

    own = [p for p in t.get("properties", [])]
    if own:
        lines.append("**Own properties:**")
        lines.append("")
        lines.append("| Name | Type | Kind | Card | Notes |")
        lines.append("|---|---|---|---|---|")
        for p in own:
            notes = []
            if "default" in p:
                notes.append(f"default `{p['default']}`")
            if "replaces" in p:
                notes.append(f"replaces `{p['replaces']}`")
            if p.get("isId"):
                notes.append("id")
            lines.append(
                f"| `{p['name']}` | `{fmt_type(p)}` | {kind(p)} | {card(p)} | "
                f"{'; '.join(notes)} |"
            )
        lines.append("")

    # Inherited properties: list briefly, since they're available on the ancestor's page.
    inherited = aggregate_properties(types, name)
    inherited_only = [(o, p) for (o, p) in inherited if o != "self"]
    if inherited_only:
        lines.append("**Inherited properties** (see ancestor pages for details):")
        lines.append("")
        lines.append("| Name | From | Type | Kind | Card |")
        lines.append("|---|---|---|---|---|")
        for origin, p in inherited_only:
            lines.append(
                f"| `{p['name']}` | `{origin}` | `{fmt_type(p)}` | {kind(p)} | {card(p)} |"
            )
        lines.append("")

    return "\n".join(lines)


def render_group(group: str, names: list, types: dict) -> str:
    title = GROUP_TITLES[group]
    out = [
        f"# {title}",
        "",
        f"_Structural metamodel for **{title}** elements, extracted from bpmn-moddle. "
        f"In-scope per [../conformance.md](../conformance.md)._",
        "",
        "_Kind legend: `attr` = XML attribute, `child elem` = XML child element, "
        "`(ref)` = ID reference to another element rather than embedded._",
        "",
        "---",
        "",
    ]
    for name in names:
        out.append(render_element(name, types))
        out.append("---")
        out.append("")
    return "\n".join(out)


def main() -> int:
    descriptor = load_descriptor()
    types = index_types(descriptor)
    ELEMENTS_DIR.mkdir(parents=True, exist_ok=True)

    # Validate all listed names exist.
    missing = []
    for names in GROUPS.values():
        for n in names:
            if n not in types:
                missing.append(n)
    if missing:
        print(f"ERROR: types not found in descriptor: {missing}", file=sys.stderr)
        return 1

    for group, names in GROUPS.items():
        path = ELEMENTS_DIR / f"{group}.md"
        path.write_text(render_group(group, names, types))
        print(f"wrote {path.relative_to(ROOT)}")

    return 0


if __name__ == "__main__":
    sys.exit(main())
