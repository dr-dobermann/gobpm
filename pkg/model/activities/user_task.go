package activities

import (
	"context"
	"reflect"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/eventproc"
	"github.com/dr-dobermann/gobpm/pkg/exec"
	"github.com/dr-dobermann/gobpm/pkg/interactor"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	hi "github.com/dr-dobermann/gobpm/pkg/model/hinteraction"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/renv"
)

const (
	unspecifiedImpl = "##unspecified"
)

// UserTask is a typical "workflow" Task where a human performer performs
// the Task with the assistance of a software application. The lifecycle of
// the Task is managed by a software component (called task manager) and is
// typically executed in the context of a Process.
//
// The User Task can be implemented using different technologies, specified
// by the implementation attribute. Besides the Web service technology, any
// technology can be used. A User Task for instance can be implemented using
// WSHumanTask by setting the implementation attribute to
// “http://docs.oasis-open.org/ns/bpel4people/ws-humantask/protocol/ 200803.”
//
// The User Task inherits the attributes and model associations of Activity
// (see Table 10.3). Table 10.13 presents the additional attributes and model
// associations of the User Task. If implementations extend these attributes
// (e.g., to introduce subjects or descriptions with presentation parameters),
// they SHOULD use attributes defined by the OASIS WSHumanTask specification.
type UserTask struct {
	outputs   *bpmncommon.Resource
	renderers []hi.Renderer

	// The authorization triad (ADR-020 §2.5): up to one Assignment per slot,
	// each either static identifiers or a FormalExpression. The single source of
	// truth for who may read/complete the task; coexists with the generic
	// activity Roles(). Read by Authorize (user_task_authz.go), never mutated
	// after construction, so shared by reference on Clone.
	assignee        *hi.Assignment
	candidateUsers  *hi.Assignment
	candidateGroups *hi.Assignment

	// completedOutputs holds the outputs a completion delivered (set by
	// ProcessEvent on the track goroutine, read by Exec on the same goroutine
	// right after). Runtime-only — nil until completion, not copied on Clone.
	completedOutputs []data.Data

	task
}

// NewUserTask tries to create a new UserTask with name and options.
//
// Available options:
//
//	User Task options:
//	- WithRenderer
//	- WithOutput
//	- WithAssignee / WithAssigneeExpr
//	- WithCandidateUsers / WithCandidateUsersExpr
//	- WithCandidateGroups / WithCandidateGroupsExpr
//
//	foundation options:
//	- WithID
//	- WithDoc
//
//	activity options:
//	- WithMultyInstance
//	- WithCompensation
//	- WithLoop
//	- WithStartQuantity
//	- WithCompletionQuantity
//	- WithParameters
//	- WithoutParams
//
//	data options:
//	- WithProperties
func NewUserTask(
	name string,
	userTaskOpts ...options.Option,
) (*UserTask, error) {
	utc := usrTaskConfig{
		name:      strings.TrimSpace(name),
		renderers: []hi.Renderer{},
		taskOpts:  []options.Option{},
	}

	for _, o := range userTaskOpts {
		switch opt := o.(type) {
		case foundation.BaseOption, ActivityOption, taskOption,
			data.PropertyOption:
			utc.taskOpts = append(utc.taskOpts, opt)

		case UsrTaskOption:
			if err := opt(&utc); err != nil {
				return nil,
					errs.New(
						errs.M("UserTask option applying failed"),
						errs.C(errorClass, errs.BulidingFailed),
						errs.E(err))
			}

		default:
			return nil,
				errs.New(
					errs.M("invalid option type"),
					errs.C(errorClass, errs.TypeCastingError),
					errs.D("option_type", reflect.TypeOf(opt).String()))
		}
	}

	return utc.newUsrTask()
}

// Implementation returns the UserTask implementations.
func (ut *UserTask) Implementation() []string {
	if len(ut.renderers) == 0 {
		return []string{unspecifiedImpl}
	}

	imps := make([]string, len(ut.renderers))
	for i, r := range ut.renderers {
		imps[i] = r.Implementation()
	}

	return imps
}

// Renderers returns all renders registered for the UserTask.
func (ut *UserTask) Renderers() []hi.Renderer {
	return append([]hi.Renderer{}, ut.renderers...)
}

// Outputs returns outputs expected from renderers.
func (ut *UserTask) Outputs() []*bpmncommon.ResourceParameter {
	if ut.outputs == nil {
		errs.Panic("user task has no output defined")
		return nil
	}

	return ut.outputs.Parameters()
}

// ----------------------- flow.Node interface --------------------------------

// Node returns the UserTask as a flow node.
func (ut *UserTask) Node() flow.Node {
	return ut
}

// Clone returns a per-instance copy of the UserTask. The embedded task is cloned
// (config shared by reference, fresh activity shell); the outputs resource and
// renderers are shared by reference as immutable configuration.
func (ut *UserTask) Clone() (flow.Node, error) {
	t, err := ut.clone()
	if err != nil {
		return nil, err
	}

	return &UserTask{
		task:            t,
		outputs:         ut.outputs,
		renderers:       ut.renderers,
		assignee:        ut.assignee,
		candidateUsers:  ut.candidateUsers,
		candidateGroups: ut.candidateGroups,
	}, nil
}

// ------------------------ flow.Task interface -------------------------------

// TaskType returns the task type for UserTask.
func (ut *UserTask) TaskType() flow.TaskType {
	return flow.UserTask
}

// ----------------------exec.NodeExecutor interface --------------------------

// Exec binds the outputs a completed UserTask delivered — stored by ProcessEvent
// when the completion event reached the parked track — into the execution frame,
// then advances onto the outgoing flow(s). A UserTask is a wait node: it parked
// (checkNodeType marks it a human task), was announced to the TaskDistributor,
// and resumed only on an authorized, validated Complete (ADR-020 §2.1, §2.4). So
// Exec is reached exactly once, after acceptance; it never blocks.
func (ut *UserTask) Exec(
	ctx context.Context,
	re renv.RuntimeEnvironment,
) ([]*flow.SequenceFlow, error) {
	if len(ut.completedOutputs) > 0 {
		if err := re.Put(ut.completedOutputs...); err != nil {
			return nil,
				errs.New(
					errs.M("user task completion output binding failed"),
					errs.C(errorClass, errs.OperationFailed),
					errs.D("task_id", ut.ID()),
					errs.E(err))
		}
	}

	return ut.selectOutgoing(ctx, re)
}

// ------------------ eventproc.EventProcessor interface ----------------------

// ProcessEvent receives the synthetic completion event the instance loop delivers
// to the parked track and stores its outputs for Exec to bind. It runs on the
// track goroutine (via deliver); the outputs were already authorized and
// validated by the loop (ADR-020 §2.4), so it only records them.
func (ut *UserTask) ProcessEvent(
	_ context.Context,
	eDef flow.EventDefinition,
) error {
	tc, ok := eDef.(*interactor.TaskCompletion)
	if !ok {
		return errs.New(
			errs.M("user task %q expects a task-completion event", ut.ID()),
			errs.C(errorClass, errs.TypeCastingError),
			errs.D("task_id", ut.ID()),
			errs.D("event_type", string(eDef.Type())))
	}

	ut.completedOutputs = tc.Outputs()

	return nil
}

// ----------------------------------------------------------------------------

// interfaces check
var (
	_ flow.Node                = (*UserTask)(nil)
	_ flow.Task                = (*UserTask)(nil)
	_ exec.NodeExecutor        = (*UserTask)(nil)
	_ interactor.HumanTask     = (*UserTask)(nil)
	_ eventproc.EventProcessor = (*UserTask)(nil)
)
