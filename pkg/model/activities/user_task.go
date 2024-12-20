package activities

import (
	"context"
	"reflect"
	"strings"

	"github.com/dr-dobermann/gobpm/internal/exec"
	"github.com/dr-dobermann/gobpm/internal/interactor"
	"github.com/dr-dobermann/gobpm/internal/renv"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	hi "github.com/dr-dobermann/gobpm/pkg/model/hinteraction"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

const (
	unspecifiedImpl = "##unspecified"
)

// User Task
//
// A User Task is a typical “workflow” Task where a human performer performs
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
	task

	// This attribute specifies the technology that will be used to implement
	// the User Task. Valid values are "##unspecified" for leaving the
	// implementation technology open, "##WebService" for the Web service
	// technology or a URI identifying any other technology or coordination
	// protocol. The default technology for this task is unspecified.
	// impl string
	// DEV_NOTE: since there could be more than one renderer, more than one
	// implementation could be returned. "##unspecified" is returned only
	// if there is no renderers.
	// implementation string
	//
	// implementation type is moved to renderers. Every renderer has itsown
	// implementation.

	// This attributes acts as a hook which allows BPMN adopters to specify
	// task rendering attributes by using the BPMN Extension mechanism.
	renderers []hi.Renderer

	// outputs describes the output parameters expected from renderers.
	// every parameter is described by single ResourceParameter of
	// Resource.
	outputs *common.Resource

	resChan chan data.Data
}

// NewUserTask tries to create a new UserTask with name and options.
//
// Available options:
//
//		User Task options:
//		- WithRenderer
//	    - WithOutput
//
//		foundation options:
//		- WithId
//		- WithDoc
//
//		activitiy options:
//		- WithCompensation
//		- WithLoop
//		- WithStartQuantity
//		- WithCompleteQuantity
//		- WithSet
//		- WithoutParams
//		- WithRoles
//
//		data options:
//		- WithProperties
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
		case foundation.BaseOption, activityOption, taskOption:
			utc.taskOpts = append(utc.taskOpts, opt)

		case usrTaskOption:
			if err := opt.Apply(&utc); err != nil {
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

// ---------------------- interactor.Interactor interface
// Renderers returns all renders registered for the UserTask.
func (ut *UserTask) Renderers() []hi.Renderer {
	return append([]hi.Renderer{}, ut.renderers...)
}

// Outputs returns outputs expected from renderers.
func (ut *UserTask) Outputs() []*common.ResourceParameter {
	if ut.outputs == nil {
		errs.Panic("user task has no output defined")
		return nil
	}

	return ut.outputs.Parameters()
}

// ----------------------- flow.Node interface --------------------------------

func (ut *UserTask) Node() flow.Node {
	return ut
}

// ------------------------ flow.Task interface -------------------------------

func (ut *UserTask) TaskType() flow.TaskType {
	return flow.UserTask
}

// ---------------------- exec.NodePrologue interface -------------------------

// Prologue registers UserTask as Interactor in runtime environment and gets
// results' channel from RenderProvider.
func (ut *UserTask) Prologue(
	ctx context.Context,
	re renv.RuntimeEnvironment,
) error {
	rr := re.RenderRegistrator()
	if rr == nil {
		return errs.New(
			errs.M("no RenderProvider for UserTask"),
			errs.C(errorClass, errs.InvalidObject),
			errs.D("task_id", ut.Id()),
			errs.D("task_name", ut.Name()),
			errs.D("instance_id", re.InstanceId()))
	}

	rCh, err := rr.Register(ut)
	if err != nil {
		return errs.New(
			errs.M("interactor registration failed"),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err))
	}

	ut.resChan = rCh

	return nil
}

// ----------------------exec.NodeExecutor interface --------------------------

// Exec waits for results of user interaction.
func (ut *UserTask) Exec(
	ctx context.Context,
	re renv.RuntimeEnvironment,
) ([]*flow.SequenceFlow, error) {
	if ut.resChan == nil {
		return nil,
			errs.New(
				errs.M("no result channel from RenderProvider"),
				errs.C(errorClass, errs.InvalidState))
	}

	dd := []data.Data{}

	for d := range ut.resChan {
		dd = append(dd, d)
	}

	if err := re.AddData(ut, dd...); err != nil {
		return nil,
			errs.New(
				errs.M("interaction result adding error"),
				errs.C(errorClass, errs.OperationFailed),
				errs.E(err))
	}

	return ut.Outgoing(), nil
}

// ----------------------------------------------------------------------------

// interfaces check
var (
	_ flow.Node             = (*UserTask)(nil)
	_ flow.Task             = (*UserTask)(nil)
	_ exec.NodeExecutor     = (*UserTask)(nil)
	_ interactor.Interactor = (*UserTask)(nil)
)
