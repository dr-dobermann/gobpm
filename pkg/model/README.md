#BPMN elements support

goBPM is intended to comply for BPMN Process Execution Conformance. 
That means goBPM process model should support followed BPMN elements and attributes:

## Common Executable Conformance Sub-Class Elements and Attributes

| Element | Attributes |
|---------|------------|
| sequenceFlow (unconditional) | id, (name), sourceRefa, targetRefb |
| sequenceFlow (conditional) | id, name, sourceRef, targetRef, conditionExpressionc |
| sequenceFlow (default) | id, name, sourceRef, targetRef, defaultd |
| subProcess (expanded) | id, name, flowElement, loopCharacteristics, boundaryEventRefs |
| exclusiveGateway | id, name, gatewayDirection (only converging and diverging), default |
| parallelGateway | id, name, gatewayDirection (only converging and diverging) |
| startEvent (None) | id, name |
| endEvent (None) | id, name |
| eventBasedGateway | id, name, gatewayDirection, eventGatewayType |
| userTask | id, name, renderings, implementation, resources, ioSpecification, dataInputAssociations, dataOutputAssociations, loopCharacteristics, boundaryEventRefs |
| serviceTask | id, name, implementation, operationRef, ioSpecification, dataInputAssociations, dataOutputAssociations, loopCharacteristics, boundaryEventRefs |
| callActivity | id, name, calledElement, ioSpecification, dataInputAssociations, dataOutputAssociations, loopCharacteristics, boundaryEventRefs |
| dataObject | id, name, isCollection, itemSubjectRef |
| textAnnotation | id, text |
| dataAssociation | id, name, sourceRef, targetRef, assignment |
| messageStartEvent | id, name, messageEventDefinition (either ref or contained), dataOutput, dataOutputAssociations |
| messageEndEvent | id, name, messageEventDefinition, (either ref or contained), dataInput, dataInputAssociations |
| terminateEndEvent | (Terminating trigger in combination with one of the other end events) |
| Catching message Intermediate Event | id, name, messageEventDefinition (either ref or contained), dataOutput, dataOutputAssociations |
| Throwing message Intermediate Event | id, name, messageEventDefinition (either ref or contained), dataInput, dataInputAssociations |
| Catching timer Intermediate Event | id, name, timerEventDefinition (contained) |
| Boundary error Intermediate Event | id, name, attachedToRef, errorEventDefinition, (contained or referenced), dataOutput, dataOutputAssociations |

## Common Executable Conformance Sub-Class Supporting Classes

| Element | Attributes |
|---------|------------|
| StandardLoopCharacteristics | id, loopCondition |
| MultiInstanceLoopCharacteristics | id, isSequential, loopDataInput, inputDataItem |
| Rendering |
| Resource | id, name |
| ResourceRole | id, resourceRef, resourceAssignmentExpression |
| InputOutputSpecification | id, dataInputs, dataOutputs |
| DataInput | id, name, isCollection, itemSubjectRef |
| DataOutput | id, name, isCollection, itemSubjectRef |
| ItemDefinition | id, structure or import |
| Operation | id, name, inMessageRef, outMessageRef, errorRefs |
| Message | id, name, structureRef |
| Error | id, structureRef |
| Assignment | id, from, to |
| MessageEventDefinition | id, messageRef, operationRef |
| TerminateEventDefinition | id |
| TimerEventDefinition | id, timeDate |

Some attributes may vary for the sake of simplicity, but meaning will be provided.
In a rare occasion the BPMN recommended packages for elements also changed due to impossibility of realization as proposed.
For example Lane and LaneSet elements should be in process package, but it raise circular import error. That's why they moved into common package.

