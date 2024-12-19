# BPMN v2 compliant BPM engine on Go

![GitHub License](https://img.shields.io/github/license/dr-dobermann/gobpm)
![GitHub Tag](https://img.shields.io/github/v/tag/dr-dobermann/gobpm)
![GitHub go.mod Go version](https://img.shields.io/github/go-mod/go-version/dr-dobermann/gobpm)
[![codecov](https://codecov.io/github/dr-dobermann/gobpm/graph/badge.svg?token=ENKOTEL4VN)](https://codecov.io/github/dr-dobermann/gobpm)

**goBpm** is a BPM engine aimed of BPMN v.2 processes. It should support 
modeling, loading/saving and executing BPMN v2 processes.

**goBpm** should comply for BPMN Process Execution Conformance. That means the 
gobpm MUST fully support and interpret the operational semantics and Activity 
life-cycle, MUST fully support and interpret the underlying metamodel and MUST 
support import of BPMN Process diagram types including its definitional 
Collaboration.

Main goal -- make it functional and easy to use for Gophers not for business 
analytics. 

It is deisgned as a library not as a framework to minimize amount of 
restrictions for developers. 

