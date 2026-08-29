package bpmn

import (
	"github.com/dr-dobermann/gobpm/pkg/convert"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// nsBPMN is the BPMN 2.0 model namespace (SRD-051 §FR-5).
const nsBPMN = "http://www.omg.org/spec/BPMN/20100524/MODEL"

const errorClass = "BPMN_CONVERT_ERRORS"

// typeBool is the gobpm type name for a boolean. Shared so the condition
// contract (a sequence-flow condition is boolean by definition, BPMN §13.2)
// and the userTask placeholder output cannot drift apart.
const typeBool = "bool"

// Local element names of the SRD-051 §FR-8 MVP subset (and annotations the
// importer skips). Shared by the importer and exporter so tag spelling cannot
// drift between directions.
const (
	tagDefinitions       = "definitions"
	tagProcess           = "process"
	tagStartEvent        = "startEvent"
	tagEndEvent          = "endEvent"
	tagTask              = "task"
	tagManualTask        = "manualTask"
	tagUserTask          = "userTask"
	tagServiceTask       = "serviceTask"
	tagExclusiveGateway  = "exclusiveGateway"
	tagParallelGateway   = "parallelGateway"
	tagSequenceFlow      = "sequenceFlow"
	tagConditionExpr     = "conditionExpression"
	tagInterface         = "interface"
	tagOperation         = "operation"
	tagInMessageRef      = "inMessageRef"
	tagOutMessageRef     = "outMessageRef"
	tagDocumentation     = "documentation"
	tagExtensionElems    = "extensionElements"
	tagIncoming          = "incoming"
	tagOutgoing          = "outgoing"
	tagErrorRef          = "errorRef"
	tagTextAnnotation    = "textAnnotation"
	tagText              = "text"
	tagGroup             = "group"
	tagCategory          = "category"
	tagCategoryValue     = "categoryValue"
	tagRelationship      = "relationship"
	tagImport            = "import"
	tagAssociation       = "association"
	tagSubProcess        = "subProcess"
	tagInclusiveGateway  = "inclusiveGateway"
	tagEventBasedGtw     = "eventBasedGateway"
	tagComplexGateway    = "complexGateway"
	tagScriptTask        = "scriptTask"
	tagScript            = "script"
	tagBusinessRuleTask  = "businessRuleTask"
	tagMessage           = "message"
	tagIntermediateCatch = "intermediateCatchEvent"
	tagIntermediateThrow = "intermediateThrowEvent"
	tagBoundaryEvent     = "boundaryEvent"
	tagSendTask          = "sendTask"
	tagReceiveTask       = "receiveTask"

	// attrAttachedTo is the boundary event's host reference.
	attrAttachedTo = "attachedToRef"

	// attrMessageRef is the message a send or receive task exchanges.
	attrMessageRef = "messageRef"

	// tagTransaction is the Transaction Sub-Process, and the three
	// attributes the two sub-process variants are read from
	// (elements/activities.md:417-426).
	tagTransaction = "transaction"
	// The data association's expression children (§10.4.2 rules 1 and 2).
	tagTransformation     = "transformation"
	tagAssignment         = "assignment"
	tagFrom               = "from"
	tagTo                 = "to"
	attrTriggeredByEvent  = "triggeredByEvent"
	attrTransactionMethod = "method"
	attrTransactionProto  = "protocol"

	// tagCallActivity invokes a callable by key (elements/activities.md:510).
	tagCallActivity   = "callActivity"
	attrCalledElement = "calledElement"

	// tagAdHocSubProcess is entered by a host-supplied Router, so no
	// document can express it (ADR-035 §2.1).
	tagAdHocSubProcess = "adHocSubProcess"

	// attrForCompensation marks an activity that runs only when
	// compensation is thrown (elements/activities.md:19, default False).
	attrForCompensation = "isForCompensation"

	// The document's type vocabulary: an <itemDefinition> names the
	// structure an item-aware element carries, and an <import> says which
	// external schema that structure came from (elements/data.md:9-27,
	// semantics/data.md:29-41).
	tagItemDefinition = "itemDefinition"
	attrStructureRef  = "structureRef"
	attrItemKind      = "itemKind"
	attrIsCollection  = "isCollection"
	attrImportType    = "importType"
	attrLocation      = "location"
	attrNamespace     = "namespace"

	// The item-aware flow elements and the two references they carry.
	// <dataState> is a child rather than an attribute, and is reported
	// wherever it appears (SRD-089.F §4.7).
	tagDataObject      = "dataObject"
	tagDataObjectRef   = "dataObjectReference"
	tagDataState       = "dataState"
	attrItemSubjectRef = "itemSubjectRef"
	attrDataObjectRef  = "dataObjectRef"

	// The store family: a definitions-level <dataStore> declares storage
	// the ENGINE supplies through its registry, so the declaration is
	// reported as a host obligation rather than built; the in-process
	// <dataStoreReference> imports carrying its dataStoreRef verbatim
	// (semantics/data.md §10.4.1 Table 10.55, SRD-089.F §4.5).
	tagDataStore     = "dataStore"
	tagDataStoreRef  = "dataStoreReference"
	attrDataStoreRef = "dataStoreRef"
	attrCapacity     = "capacity"
	attrIsUnlimited  = "isUnlimited"

	// A <property> is data private to the process, activity or event that
	// declares it — the three owners BPMN allows and the model's three
	// PropertyOption takers (§10.4.1 Table 10.57, SRD-089.F FR-6).
	tagProperty = "property"

	// The two loop markers an activity may carry — at most one
	// (elements/activities.md: loopCharacteristics is 0..1) — and their
	// children (§13.3.6, §13.3.7; SRD-089.H).
	tagStandardLoop    = "standardLoopCharacteristics"
	tagMultiInstance   = "multiInstanceLoopCharacteristics"
	tagLoopCondition   = "loopCondition"
	tagLoopCardinality = "loopCardinality"
	tagCompletionCond  = "completionCondition"
	tagLoopDataInRef   = "loopDataInputRef"
	tagLoopDataOutRef  = "loopDataOutputRef"
	tagInputDataItem   = "inputDataItem"
	tagOutputDataItem  = "outputDataItem"
	tagComplexBehavior = "complexBehaviorDefinition"
	attrTestBefore     = "testBefore"
	attrLoopMaximum    = "loopMaximum"
	attrIsSequential   = "isSequential"
	attrBehavior       = "behavior"
	attrNoneBehaviorEv = "noneBehaviorEventRef"
	attrOneBehaviorEv  = "oneBehaviorEventRef"

	// The data-flow family: staged with SRD-089.G (SRD-089.F §4.9). The
	// tags exist here only so their refusals can name that plan.
	tagIOSpecification = "ioSpecification"
	tagDataInput       = "dataInput"
	tagDataOutput      = "dataOutput"
	tagInputSet        = "inputSet"
	tagOutputSet       = "outputSet"
	tagDataInputAssoc  = "dataInputAssociation"
	tagDataOutputAssoc = "dataOutputAssociation"

	// The lane partition, model-only in every container that carries it
	// (conformance.md line 173).
	tagLaneSet      = "laneSet"
	tagLane         = "lane"
	tagFlowNodeRef  = "flowNodeRef"
	tagChildLaneSet = "childLaneSet"

	// The ten event definitions (elements/event-definitions.md), their
	// expression children and the two references they carry as children
	// rather than attributes.
	tagMessageEventDef     = "messageEventDefinition"
	tagTimerEventDef       = "timerEventDefinition"
	tagSignalEventDef      = "signalEventDefinition"
	tagErrorEventDef       = "errorEventDefinition"
	tagEscalationEventDef  = "escalationEventDefinition"
	tagConditionalEventDef = "conditionalEventDefinition"
	tagTerminateEventDef   = "terminateEventDefinition"
	tagCancelEventDef      = "cancelEventDefinition"
	tagCompensateEventDef  = "compensateEventDefinition"
	tagLinkEventDef        = "linkEventDefinition"
	tagTimeDate            = "timeDate"
	tagTimeCycle           = "timeCycle"
	tagTimeDuration        = "timeDuration"
	tagCondition           = "condition"
	tagOperationRef        = "operationRef"
	tagLinkSource          = "source"
	tagLinkTarget          = "target"
)

// Three catalog element names collide by spelling with the observability
// vocabulary, which the repo enforces wherever those spellings appear
// (internal/lintcfg TestNoLiteralAttrKeys). They are NOT the same thing —
// BPMN fixes these element names permanently, a log key may be renamed at
// any time — so TestCatalogTagsMatchTheStandard pins the equality: a
// vocabulary rename fails there instead of silently making the parser
// look for elements no document contains.
const (
	tagSignal     = observability.AttrSignal
	tagError      = observability.AttrError
	tagEscalation = observability.AttrEscalation
)

func init() { //nolint:gochecknoinits // SRD-051 §FR-4: blank-import self-registration, the image.RegisterFormat idiom (ADR-024 §2.2)
	convert.RegisterImporterAtInit(convert.BPMN, importer{})
	convert.RegisterExporterAtInit(convert.BPMN, exporter{})
}
