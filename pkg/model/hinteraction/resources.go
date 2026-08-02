package hinteraction

import (
	"slices"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// ResourceRole represents a BPMN resource role.
//
// A role names its people in one of two mutually exclusive ways (Table 10.5): a
// resourceRef resolved by a query into an organizational directory, or a
// resourceAssignmentExpression evaluated against the process data. gobpm
// executes the second; the first is refused at registration, since the engine
// has no directory to query (ADR-020 v.3 §2.5.4).
type ResourceRole struct {
	foundation.BaseElement

	name string

	// kind is the level of the Performer chain this role occupies. It decides
	// whether the role contributes eligibility (RoleKind.Authorizes).
	kind RoleKind

	// The Resource that is associated with Activity. Should not be specified
	// when resourceAssignmentExpression is provided.
	resource *bpmncommon.Resource

	// This defines the Expression used for the Resource assignment. Should
	// not be specified when a resourceRef is provided.
	assignmentExpression *ResourceAssignmentExpression

	// This defines the Parameter bindings used for the Resource assignment.
	// Is only applicable if a resourceRef is specified.
	parameterBindings []ResourceParameterBinding
}

// newRole builds a role of the given kind, applying the constraints Table 10.5
// states in its own attribute text. It is the single construction path: every
// exported constructor below delegates here so the checks cannot diverge.
func newRole(
	kind RoleKind,
	name string,
	res *bpmncommon.Resource,
	assignExpr *ResourceAssignmentExpression,
	pBinding []ResourceParameterBinding,
	baseOpts ...options.Option,
) (*ResourceRole, error) {
	name = strings.TrimSpace(name)
	if err := errs.CheckStr(
		name,
		"name should be provided for ResourceRole",
		errorClass,
	); err != nil {
		return nil,
			errs.New(
				errs.M("ResourceRole creation failed"),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(err))
	}

	// Table 10.5: resourceRef "should not be specified when
	// resourceAssignmentExpression is provided", and vice versa.
	if res != nil && assignExpr != nil {
		return nil,
			errs.New(
				errs.M("Resource and Assignment expression shouldn't be set simultaneously"),
				errs.C(errorClass, errs.InvalidParameter),
				errs.D("role", name))
	}

	// Table 10.5: resourceParameterBindings are "only applicable if a
	// resourceRef is specified" — bindings without one bind nothing.
	if len(pBinding) > 0 && res == nil {
		return nil,
			errs.New(
				errs.M("resource parameter bindings need a resource reference"),
				errs.C(errorClass, errs.InvalidParameter),
				errs.D("role", name))
	}

	// An assignment expression with nothing to evaluate could never resolve to
	// anyone, so the role would authorize nobody however it is used.
	if assignExpr != nil && assignExpr.Expression == nil {
		return nil,
			errs.New(
				errs.M("assignment expression of role %q has no expression", name),
				errs.C(errorClass, errs.EmptyNotAllowed),
				errs.D("role", name))
	}

	// An authorizing role must name somebody. With neither a resourceRef nor an
	// assignment expression it resolves to the empty set by construction — a
	// declaration that looks like authorization and grants none, which is the
	// defect the role model exists to remove (ADR-020 v.3 §2.5.4). A declarative
	// kind may carry a name alone: there it is a label, not a broken promise.
	if kind.Authorizes() && res == nil && assignExpr == nil {
		return nil,
			errs.New(
				errs.M("%s %q names nobody: give it a resource reference or "+
					"an assignment expression", kind, name),
				errs.C(errorClass, errs.EmptyNotAllowed),
				errs.D("role", name),
				errs.D("kind", string(kind)))
	}

	be, err := foundation.NewBaseElement(baseOpts...)
	if err != nil {
		return nil,
			errs.New(
				errs.M("ResourceRole %q creation failed", name),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(err))
	}

	return &ResourceRole{
			BaseElement:          *be,
			name:                 name,
			kind:                 kind,
			resource:             res,
			assignmentExpression: assignExpr,
			parameterBindings:    pBinding,
		},
		nil
}

// NewResourceRole creates a new bare ResourceRole (RoleResource) and returns its
// pointer on success or error on failure. A bare role is declarative: it names a
// resource associated with the activity and grants no authorization. For a role
// that decides who may act on a human task, use NewHumanPerformer or
// NewPotentialOwner.
func NewResourceRole(
	name string,
	res *bpmncommon.Resource,
	assignExpr *ResourceAssignmentExpression,
	pBinding []ResourceParameterBinding,
	baseOpts ...options.Option,
) (*ResourceRole, error) {
	return newRole(RoleResource, name, res, assignExpr, pBinding, baseOpts...)
}

// MustResourceRole creates a ResourceRole and returns its pointer on success or
// panics on failure.
func MustResourceRole(
	name string,
	res *bpmncommon.Resource,
	assignExpr *ResourceAssignmentExpression,
	pBinding []ResourceParameterBinding,
	baseOpts ...options.Option,
) *ResourceRole {
	r, err := NewResourceRole(name, res, assignExpr, pBinding, baseOpts...)
	if err != nil {
		errs.Panic(err)
	}

	return r
}

// NewPerformer creates a Performer role — BPMN 1.2's generic performer, the
// resource that performs the activity. It is declarative and grants no
// authorization: the standard does not restrict a Performer to people.
func NewPerformer(
	name string,
	res *bpmncommon.Resource,
	assignExpr *ResourceAssignmentExpression,
	pBinding []ResourceParameterBinding,
	baseOpts ...options.Option,
) (*ResourceRole, error) {
	return newRole(RolePerformer, name, res, assignExpr, pBinding, baseOpts...)
}

// MustPerformer creates a Performer role or panics on failure.
func MustPerformer(
	name string,
	res *bpmncommon.Resource,
	assignExpr *ResourceAssignmentExpression,
	pBinding []ResourceParameterBinding,
	baseOpts ...options.Option,
) *ResourceRole {
	r, err := NewPerformer(name, res, assignExpr, pBinding, baseOpts...)
	if err != nil {
		errs.Panic(err)
	}

	return r
}

// NewHumanPerformer creates a HumanPerformer role — the human specialization of
// Performer. Declared on a UserTask, it contributes to the task's eligible set.
func NewHumanPerformer(
	name string,
	res *bpmncommon.Resource,
	assignExpr *ResourceAssignmentExpression,
	pBinding []ResourceParameterBinding,
	baseOpts ...options.Option,
) (*ResourceRole, error) {
	return newRole(
		RoleHumanPerformer, name, res, assignExpr, pBinding, baseOpts...)
}

// MustHumanPerformer creates a HumanPerformer role or panics on failure.
func MustHumanPerformer(
	name string,
	res *bpmncommon.Resource,
	assignExpr *ResourceAssignmentExpression,
	pBinding []ResourceParameterBinding,
	baseOpts ...options.Option,
) *ResourceRole {
	r, err := NewHumanPerformer(name, res, assignExpr, pBinding, baseOpts...)
	if err != nil {
		errs.Panic(err)
	}

	return r
}

// NewPotentialOwner creates a PotentialOwner role — the persons who can claim
// and work on a User Task. Declared on a UserTask, it contributes to the task's
// eligible set, and a member may claim the task to become its actual owner.
func NewPotentialOwner(
	name string,
	res *bpmncommon.Resource,
	assignExpr *ResourceAssignmentExpression,
	pBinding []ResourceParameterBinding,
	baseOpts ...options.Option,
) (*ResourceRole, error) {
	return newRole(
		RolePotentialOwner, name, res, assignExpr, pBinding, baseOpts...)
}

// MustPotentialOwner creates a PotentialOwner role or panics on failure.
func MustPotentialOwner(
	name string,
	res *bpmncommon.Resource,
	assignExpr *ResourceAssignmentExpression,
	pBinding []ResourceParameterBinding,
	baseOpts ...options.Option,
) *ResourceRole {
	r, err := NewPotentialOwner(name, res, assignExpr, pBinding, baseOpts...)
	if err != nil {
		errs.Panic(err)
	}

	return r
}

// Name returns the ResourceRole name.
func (r *ResourceRole) Name() string {
	return r.name
}

// Kind returns the level of the Performer chain the role occupies.
func (r *ResourceRole) Kind() RoleKind {
	return r.kind
}

// Resource returns the Resource the role refers to, or nil when the role names
// its people through an assignment expression instead.
func (r *ResourceRole) Resource() *bpmncommon.Resource {
	return r.resource
}

// AssignmentExpression returns the role's assignment expression, or nil when the
// role refers to a Resource instead.
func (r *ResourceRole) AssignmentExpression() *ResourceAssignmentExpression {
	return r.assignmentExpression
}

// ParameterBindings returns a copy of the role's resource parameter bindings.
func (r *ResourceRole) ParameterBindings() []ResourceParameterBinding {
	return slices.Clone(r.parameterBindings)
}

// ResourceAssignmentExpression represents a BPMN resource assignment expression.
//
// Resources can be assigned to an Activity using Expressions. These Expressions
// MUST return Resource entity related data types, like Users or Groups.
// Different Expressions can return multiple Resources. All of them are assigned
// to the respective subclass of the ResourceRole element, for example as
// potential owners. The semantics is defined by the subclass.
type ResourceAssignmentExpression struct {
	// Expression is evaluated at runtime to assign resource(s) to a ResourceRole.
	//
	// It is a FormalExpression rather than the natural-language data.Expression:
	// the standard defines the latter as "not executable and considered
	// underspecified", so an engine could never evaluate it. Only the formal
	// variant reaches an expression engine (ADR-020 v.3 §2.5.4).
	//
	// It precedes the embedded BaseElement to satisfy fieldalignment: an
	// interface is two pointer words, and leading with it keeps the struct's
	// pointer-bytes minimal.
	Expression data.FormalExpression

	foundation.BaseElement
}

// NewResourceAssignmentExpression wraps expr into a ResourceAssignmentExpression
// with optional base options. A nil expression is refused: a role built on one
// could never resolve to anybody.
func NewResourceAssignmentExpression(
	expr data.FormalExpression,
	baseOpts ...options.Option,
) (*ResourceAssignmentExpression, error) {
	if expr == nil {
		return nil,
			errs.New(
				errs.M("NewResourceAssignmentExpression: a nil expression isn't allowed"),
				errs.C(errorClass, errs.EmptyNotAllowed))
	}

	be, err := foundation.NewBaseElement(baseOpts...)
	if err != nil {
		return nil,
			errs.New(
				errs.M("ResourceAssignmentExpression creation failed"),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(err))
	}

	return &ResourceAssignmentExpression{
			BaseElement: *be,
			Expression:  expr,
		},
		nil
}

// ResourceParameterBinding represents a BPMN resource parameter binding.
//
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
	Parameter *bpmncommon.ResourceParameter

	// The Expression that evaluates the value used to bind the
	// ResourceParameter.
	Expression data.Expression
}
