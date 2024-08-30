package activities

import (
	"reflect"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
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
	impl string

	// This attributes acts as a hook which allows BPMN adopters to specify
	// task rendering attributes by using the BPMN Extension mechanism.
	renders []hi.Renderer
}

func NewUserTask(
	name string,
	userTaskOpts ...options.Option,
) (*UserTask, error) {
	utc := usrTaskConfig{
		impl:     unspecifiedImpl,
		name:     strings.TrimSpace(name),
		renders:  []hi.Renderer{},
		taskOpts: []options.Option{},
	}

	for _, o := range userTaskOpts {
		switch opt := o.(type) {
		case foundation.BaseOption, activityOption, taskOption:
			utc.taskOpts = append(utc.taskOpts, opt)

		case usrTaskOption:
			opt.Apply(&utc)

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

// Implementation returns the UserTask implementation.
func (ut *UserTask) Implementation() string {
	return ut.impl
}

// Renders returns all renders registered for the UserTask.
func (ut *UserTask) Renders() []hi.Renderer {
	return append([]hi.Renderer{}, ut.renders...)
}
