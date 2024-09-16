package activities

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/dr-dobermann/gobpm/internal/exec"
	"github.com/dr-dobermann/gobpm/internal/renv"
	"github.com/dr-dobermann/gobpm/pkg/errs"
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
	Task

	// This attribute specifies the technology that will be used to implement
	// the User Task. Valid values are "##unspecified" for leaving the
	// implementation technology open, "##WebService" for the Web service
	// technology or a URI identifying any other technology or coordination
	// protocol. The default technology for this task is unspecified.
	// impl string
	// DEV_NOTE: since there could be more than one renderer, more than one
	// implementation could be returned. "##unspecified" is returned only
	// if there is no renderers.

	// This attributes acts as a hook which allows BPMN adopters to specify
	// task rendering attributes by using the BPMN Extension mechanism.
	renderers []hi.Renderer
}

// NewUserTask tries to create a new UserTask with name and options.
//
// Available options:
//
//	User Task options:
//	- WithRenderer
//
//	foundation options:
//	- WithId
//	- WithDoc
//
//	activitiy options:
//	- WithCompensation
//	- WithLoop
//	- WithStartQuantity
//	- WithCompleteQuantity
//	- WithSet
//	- WithoutParams
//	- WithRoles
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

// Renderers returns all renders registered for the UserTask.
func (ut *UserTask) Renderers() []hi.Renderer {
	return append([]hi.Renderer{}, ut.renderers...)
}

// ----------------------- flow.Node interface --------------------------------

func (ut *UserTask) Node() flow.Node {
	return ut
}

// ------------------------ flow.Task interface -------------------------------

func (ut *UserTask) TaskType() flow.TaskType {
	return flow.UserTask
}

// ----------------------exec.NodeExecutor interface --------------------------

func (ut *UserTask) Exec(
	ctx context.Context,
	re renv.RuntimeEnvironment,
) ([]*flow.SequenceFlow, error) {
	return nil, fmt.Errorf("not implemented yet")
}

// ----------------------------------------------------------------------------

// interfaces check
var (
	_ flow.Node         = (*UserTask)(nil)
	_ flow.Task         = (*UserTask)(nil)
	_ exec.NodeExecutor = (*UserTask)(nil)
	_ hi.Interactor     = (*UserTask)(nil)
)
