package bpmn

import "github.com/dr-dobermann/gobpm/pkg/convert"

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
	tagDefinitions      = "definitions"
	tagProcess          = "process"
	tagStartEvent       = "startEvent"
	tagEndEvent         = "endEvent"
	tagTask             = "task"
	tagManualTask       = "manualTask"
	tagUserTask         = "userTask"
	tagServiceTask      = "serviceTask"
	tagExclusiveGateway = "exclusiveGateway"
	tagParallelGateway  = "parallelGateway"
	tagSequenceFlow     = "sequenceFlow"
	tagConditionExpr    = "conditionExpression"
	tagInterface        = "interface"
	tagOperation        = "operation"
	tagInMessageRef     = "inMessageRef"
	tagOutMessageRef    = "outMessageRef"
	tagDocumentation    = "documentation"
	tagExtensionElems   = "extensionElements"
	tagIncoming         = "incoming"
	tagOutgoing         = "outgoing"
)

// isSkippableAnnotation reports BPMN-namespace children that carry no
// executable-core semantics for this slice and are therefore skipped
// silently — the same policy as documentation (package doc, SRD-051 §FR-7
// spirit: only *unmapped flow elements* must surface as
// UnsupportedElementError). extensionElements is nearly universal in
// bpmn.io / Camunda exports; treating it as an error would reject every
// real modeler file even when the flow graph is fully in the MVP subset.
func isSkippableAnnotation(local string) bool {
	switch local {
	case tagDocumentation, tagExtensionElems:
		return true
	default:
		return false
	}
}

func init() { //nolint:gochecknoinits // SRD-051 §FR-4: blank-import self-registration, the image.RegisterFormat idiom (ADR-024 §2.2)
	convert.RegisterImporterAtInit(convert.BPMN, importer{})
	convert.RegisterExporterAtInit(convert.BPMN, exporter{})
}
