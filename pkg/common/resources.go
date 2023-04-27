package common

import (
	"github.com/dr-dobermann/gobpm/pkg/expression"
	"github.com/dr-dobermann/gobpm/pkg/foundation"
)

type Resource struct {
	foundation.BaseElement

	Name       string
	Parameters []ResourceParameter
}

type ResourceParameter struct {
	NamedElement

	Name       string
	Item       ItemDefinition
	IsRequired bool
}

type ResourceRole struct {
	foundation.BaseElement

	resource   *Resource
	assignExpr ResourceAssignmentExpression
	bindings   []ResourceParameterBinding
}

type ResourceAssignmentExpression struct {
	expr expression.Expression
}

type ResourceParameterBinding struct {
	parameter ResourceParameter
	expr      expression.Expression
}

type Performer struct {
	ResourceRole
}
