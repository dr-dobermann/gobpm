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
	tagGroup             = "group"
	tagCategory          = "category"
	tagRelationship      = "relationship"
	tagImport            = "import"
	tagAssociation       = "association"
	tagInclusiveGateway  = "inclusiveGateway"
	tagEventBasedGtw     = "eventBasedGateway"
	tagComplexGateway    = "complexGateway"
	tagScriptTask        = "scriptTask"
	tagScript            = "script"
	tagBusinessRuleTask  = "businessRuleTask"
	tagMessage           = "message"
	tagIntermediateCatch = "intermediateCatchEvent"
	tagIntermediateThrow = "intermediateThrowEvent"

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
