package activities

import (
	"fmt"
	"reflect"
	"slices"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	hi "github.com/dr-dobermann/gobpm/pkg/model/hinteraction"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

type (
	usrTaskConfig struct {
		name string

		// authorization triad (ADR-020 §2.5); up to one Assignment per slot.
		assignee        *hi.Assignment
		candidateUsers  *hi.Assignment
		candidateGroups *hi.Assignment

		renderers []hi.Renderer
		taskOpts  []options.Option
		outputs   []*bpmncommon.ResourceParameter
	}

	// UsrTaskOption represents a configuration option for UserTask
	UsrTaskOption func(cfg *usrTaskConfig) error
)

// newUsrTask tries to create new UserTask from user task config.
func (utc *usrTaskConfig) newUsrTask() (*UserTask, error) {
	if err := utc.Validate(); err != nil {
		return nil, err
	}

	t, err := newTask(utc.name, utc.taskOpts...)
	if err != nil {
		return nil,
			errs.New(
				errs.M("user task building failed"),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(err))
	}

	r, err := bpmncommon.NewResource(utc.name, utc.outputs...)
	if err != nil {
		return nil,
			errs.New(
				errs.M("resource creation failed"),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(err))
	}

	ut := UserTask{
		task:            *t,
		renderers:       append([]hi.Renderer{}, utc.renderers...),
		outputs:         r,
		assignee:        utc.assignee,
		candidateUsers:  utc.candidateUsers,
		candidateGroups: utc.candidateGroups,
	}

	return &ut, nil
}

// setAssignment stores a built Assignment in the config field matching its slot,
// rejecting a slot that is already set (a slot takes one Assignment — static XOR
// expression, once). The parameter validity (non-empty ids / non-nil expression)
// is guaranteed by the hi.New*Assignment constructor that produced a.
func (utc *usrTaskConfig) setAssignment(a *hi.Assignment) error {
	switch a.Slot() {
	case hi.Assignee:
		if utc.assignee != nil {
			return fmt.Errorf("%s is already set", a.Slot())
		}

		utc.assignee = a

	case hi.CandidateUsers:
		if utc.candidateUsers != nil {
			return fmt.Errorf("%s is already set", a.Slot())
		}

		utc.candidateUsers = a

	case hi.CandidateGroups:
		if utc.candidateGroups != nil {
			return fmt.Errorf("%s is already set", a.Slot())
		}

		utc.candidateGroups = a
	}

	return nil
}

// WithAssignee sets the task's assignee (actual owner) to a static user id. When
// set, only that user may read/complete the task (ADR-020 §2.5). Rejects an empty
// id.
func WithAssignee(userID string) UsrTaskOption {
	return func(cfg *usrTaskConfig) error {
		a, err := hi.NewStaticAssignment(hi.Assignee, userID)
		if err != nil {
			return fmt.Errorf("WithAssignee: %w", err)
		}

		return cfg.setAssignment(a)
	}
}

// WithAssigneeExpr sets the task's assignee from a FormalExpression evaluated per
// instance to the owning user id. Rejects a nil expression.
func WithAssigneeExpr(expr data.FormalExpression) UsrTaskOption {
	return func(cfg *usrTaskConfig) error {
		a, err := hi.NewExprAssignment(hi.Assignee, expr)
		if err != nil {
			return fmt.Errorf("WithAssigneeExpr: %w", err)
		}

		return cfg.setAssignment(a)
	}
}

// WithCandidateUsers sets the static user ids eligible to claim/complete the
// task. Rejects an empty list or an empty id.
func WithCandidateUsers(userIDs ...string) UsrTaskOption {
	return func(cfg *usrTaskConfig) error {
		a, err := hi.NewStaticAssignment(hi.CandidateUsers, userIDs...)
		if err != nil {
			return fmt.Errorf("WithCandidateUsers: %w", err)
		}

		return cfg.setAssignment(a)
	}
}

// WithCandidateUsersExpr sets the candidate users from a FormalExpression
// evaluated per instance to a list of user ids. Rejects a nil expression.
func WithCandidateUsersExpr(expr data.FormalExpression) UsrTaskOption {
	return func(cfg *usrTaskConfig) error {
		a, err := hi.NewExprAssignment(hi.CandidateUsers, expr)
		if err != nil {
			return fmt.Errorf("WithCandidateUsersExpr: %w", err)
		}

		return cfg.setAssignment(a)
	}
}

// WithCandidateGroups sets the static group ids whose members may claim/complete
// the task. Rejects an empty list or an empty id.
func WithCandidateGroups(groupIDs ...string) UsrTaskOption {
	return func(cfg *usrTaskConfig) error {
		a, err := hi.NewStaticAssignment(hi.CandidateGroups, groupIDs...)
		if err != nil {
			return fmt.Errorf("WithCandidateGroups: %w", err)
		}

		return cfg.setAssignment(a)
	}
}

// WithCandidateGroupsExpr sets the candidate groups from a FormalExpression
// evaluated per instance to a list of group ids. Rejects a nil expression.
func WithCandidateGroupsExpr(expr data.FormalExpression) UsrTaskOption {
	return func(cfg *usrTaskConfig) error {
		a, err := hi.NewExprAssignment(hi.CandidateGroups, expr)
		if err != nil {
			return fmt.Errorf("WithCandidateGroupsExpr: %w", err)
		}

		return cfg.setAssignment(a)
	}
}

// WithRenderer adds new unique Render to user task config.
func WithRenderer(r hi.Renderer) UsrTaskOption {
	f := func(cfg *usrTaskConfig) error {
		if r == nil {
			return fmt.Errorf("no renderer")
		}

		if slices.ContainsFunc(
			cfg.renderers,
			func(r2c hi.Renderer) bool {
				return r2c.ID() == r.ID() ||
					r2c.Implementation() == r.Implementation()
			}) {
			return fmt.Errorf("duplicate renderer: #%s", r.ID())
		}

		cfg.renderers = append(cfg.renderers, r)

		return nil
	}

	return UsrTaskOption(f)
}

// WithOutput register new output parameter from renderer.
func WithOutput(name, pType string, required bool) UsrTaskOption {
	f := func(cfg *usrTaskConfig) error {
		if slices.ContainsFunc(
			cfg.outputs,
			func(p *bpmncommon.ResourceParameter) bool {
				return p.Name() == name
			}) {
			return fmt.Errorf("duplicate parameter %q", name)
		}

		p, err := bpmncommon.NewResourceParameter(name, pType, required)
		if err != nil {
			return fmt.Errorf("couldn't create a parameter: %w", err)
		}

		cfg.outputs = append(cfg.outputs, p)

		return nil
	}

	return UsrTaskOption(f)
}

// --------------------- options.Option interface ------------------------------

// Apply applies the user task option to the provided configurator.
func (uto UsrTaskOption) Apply(cfg options.Configurator) error {
	if utc, ok := cfg.(*usrTaskConfig); ok {
		return uto(utc)
	}

	return errs.New(
		errs.M("isn't usrTaskConfig"),
		errs.C(errorClass, errs.TypeCastingError),
		errs.D("cfg_type", reflect.TypeOf(cfg).String()))
}

// ------------------ options.Configurator interface ---------------------------

// Validate validates activityConfig fields.
func (utc *usrTaskConfig) Validate() error {
	if err := errs.CheckStr(
		utc.name,
		"UserTask should have a name",
		errorClass,
	); err != nil {
		return err
	}

	return nil
}

// ----------------------------------------------------------------------------
