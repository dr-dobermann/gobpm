package activities

import (
	"fmt"
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

		// taskPriority is the BPMN instance attribute of Table 10.14. The
		// standard defines no scale, direction or default, so the engine assigns
		// it none either (ADR-020 v.3 §2.11).
		taskPriority int
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
		taskPriority:    utc.taskPriority,
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

		// Distinct renderers are deduplicated by identity only (ADR-020 §2.9): two
		// renderers of the same implementation kind (e.g. two HTML forms) are
		// legitimately different renderings and must both survive.
		if slices.ContainsFunc(
			cfg.renderers,
			func(r2c hi.Renderer) bool {
				return r2c.ID() == r.ID()
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

// WithTaskPriority sets the UserTask's priority — the BPMN instance attribute
// of Table 10.14 (§10.3.4.1).
//
// The SETTER is an engine extension, registered as such in SAD-001 §14.2.
// BPMN defines taskPriority as an instance attribute, which no XML definition
// can set; the standard's whole normative text for it is "Returns the priority
// of the User Task" — no scale, no direction, no default, and no behavior in
// §13 that reads it. Camunda invented camunda:priority for the same reason.
//
// The engine therefore assigns the value NO meaning: it does not sort, schedule,
// escalate or route on it, and deliberately does not feed it to an Ad-Hoc Router
// (ADR-020 v.3 §2.11). It is carried and reported for an embedder to order its
// own inbox by. Any value is accepted, including a negative one, because the
// standard supplies no range to validate against — inventing one would be the
// same over-reach as inventing an ordering.
func WithTaskPriority(priority int) UsrTaskOption {
	return func(cfg *usrTaskConfig) error {
		cfg.taskPriority = priority

		return nil
	}
}

// --------------------- options.Option interface ------------------------------

// Option marks UsrTaskOption as an options.Option; newUserTask applies it by
// calling the func directly after its type-switch matches.
func (UsrTaskOption) Option() {}

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
