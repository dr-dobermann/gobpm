package common

import (
	"github.com/dr-dobermann/gobpm/pkg/expression"
)

type SequenceFlow struct {
	FlowElement
	// Expression determines the possibility of
	// using path over this SequenceFlow.
	// Could be empty. If not, the path
	// couldn't start from Parallel Gate or
	// Event FloatNode
	expr      expression.Expression
	sourceRef *FlowNode
	targetRef *FlowNode
}

func (sf *SequenceFlow) GetTarget() *FlowNode {
	return sf.targetRef
}

func (sf *SequenceFlow) GetSource() *FlowNode {
	return sf.sourceRef
}

func (sf *SequenceFlow) GetExpression() expression.Expression {
	return sf.expr
}
