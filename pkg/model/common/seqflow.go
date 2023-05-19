package common

import (
	"github.com/dr-dobermann/gobpm/pkg/model/expression"
)

type SequenceFlow struct {
	FlowElement

	// isImmediate bool

	// Condition determines the possibility of
	// using path over this SequenceFlow.
	// Could be empty. If not, the path
	// couldn't start from Parallel Gate or
	// Event FloatNode
	condition expression.Condition

	sourceRef *FlowNode
	targetRef *FlowNode
}

func (sf *SequenceFlow) GetTarget() *FlowNode {

	return sf.targetRef
}

func (sf *SequenceFlow) GetSource() *FlowNode {

	return sf.sourceRef
}

func (sf *SequenceFlow) GetCondiiton() expression.Condition {

	return sf.condition
}
