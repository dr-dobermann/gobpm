package model

import (
	"github.com/dr-dobermann/gobpm/pkg/common"
	"github.com/dr-dobermann/gobpm/pkg/expression"
)

type SequenceEnd uint8

const (
	SeSource SequenceEnd = iota
	SeTarget
)

func (se SequenceEnd) String() string {
	return []string{"Source", "Target"}[se]
}

type SequenceFlow struct {
	common.FlowElement
	process *Process
	// Expression determines the possibility of
	// using path over this SequenceFlow.
	// Could be empty. If not, the path
	// couldn't start from Parallel Gate or
	// Event FloatNode
	expr      expression.Expression
	sourceRef Node
	targetRef Node
}

func (sf *SequenceFlow) GetTarget() Node {
	return sf.targetRef
}

func (sf *SequenceFlow) GetSource() Node {
	return sf.sourceRef
}
