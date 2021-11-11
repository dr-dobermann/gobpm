# gobpm

BPMN v.2 compliant BPM engine on Go

**gobpm** is a BPM engine aimed to execute BPMN v.2 processes. It should support loading/saving BPMN Processes in standard XML.

**gobpm** should comply for BPMN Process Execution Conformance. That means the gobpm MUST fully support and interpret the operational semantics and Activity life-cycle, MUST fully support and interpret the underlying metamodel and MUST support import of BPMN Process diagram types including its definitional Collaboration.

Process model creates in `model` module and executes in `thresher` module.
