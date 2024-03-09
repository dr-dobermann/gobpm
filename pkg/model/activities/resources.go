package activities

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// *****************************************************************************
type ResourceRole struct {
	foundation.BaseElement

	name string

	// The Resource that is associated with Activity. Should not be specified
	// when resourceAssignmentExpression is provided.
	resource *common.Resource

	// This defines the Expression used for the Resource assignment. Should
	// not be specified when a resourceRef is provided.
	assignmentExpression *ResourceAssignmentExpression

	// This defines the Parameter bindings used for the Resource assignment.
	// Is only applicable if a resourceRef is specified.
	parameterBindings []ResourceParameterBinding
}

// NewResourceRole creates a new ResourceRole and returns its pointer on
// success or error on failure.
func NewResourceRole(
	name string,
	res *common.Resource,
	assignExpr *ResourceAssignmentExpression,
	pBinding []ResourceParameterBinding,
	baseOpts ...options.Option) (*ResourceRole, error) {
	name = trim(name)
	if err := checkStr(
		name,
		"name should be provided for ResourceRole"); err != nil {
		return nil,
			&errs.ApplicationError{
				Err:     err,
				Message: "ResourceRole creation failed",
				Classes: []string{
					errorClass,
					errs.BulidingFailed}}
	}

	be, err := foundation.NewBaseElement(baseOpts...)
	if err != nil {
		return nil,
			&errs.ApplicationError{
				Err:     err,
				Message: "couldn't create BaseElement for ResourceRole",
				Classes: []string{
					errorClass,
					errs.BulidingFailed}}
	}

	return &ResourceRole{
			BaseElement:          *be,
			name:                 name,
			resource:             res,
			assignmentExpression: assignExpr,
			parameterBindings:    pBinding},
		nil
}

// *****************************************************************************

// Resources can be assigned to an Activity using Expressions. These
// Expressions MUST return Resource entity related data types, like Users or
// Groups. Different Expressions can return multiple Resources. All of them
// are assigned to the respective subclass of the ResourceRole element, for
// example as potential owners. The semantics is defined by the subclass.
type ResourceAssignmentExpression struct {
	foundation.BaseElement

	// The element ResourceAssignmentExpression MUST contain an Expression
	// which is used at runtime to assign resource(s) to a ResourceRole element.
	Expression data.Expression
}

// *****************************************************************************

// Resources support query parameters that are passed to the Resource query at
// runtime. Parameters MAY refer to Task instance data using Expressions.
// During Resource query execution, an infrastructure can decide which of the
// Parameters defined by the Resource are used. It MAY use zero (0) or more
// of the Parameters specified. It MAY also override certain Parameters with
// values defined during Resource deployment. The deployment mechanism for
// Tasks and Resources is out of scope for this document. Resource queries
// are evaluated to determine the set of Resources, e.g., people, assigned to
// the Activity. Failed Resource queries are treated like Resource queries that
// return an empty result set. Resource queries return one Resource or a set
// of Resources.
type ResourceParameterBinding struct {
	foundation.BaseElement

	// Reference to the parameter defined by the Resource.
	Parameter *common.ResourceParameter

	// The Expression that evaluates the value used to bind the
	// ResourceParameter.
	Expression data.Expression
}

// *****************************************************************************
type Performer struct {
	ResourceRole
}
