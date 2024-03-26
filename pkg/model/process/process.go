package process

import (
	"errors"
	"reflect"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

const errorClass = "PROCESS_ERRORS"

// A Process describes a sequence or flow of Activities in an organization with
// the objective of carrying out work. In BPMN a Process is depicted as a graph
// of Flow Elements, which are a set of Activities, Events, Gateways, and
// Sequence Flows that define finite execution semantics. Processes can be
// defined at any level from enterprise-wide Processes to Processes performed
// by a single person. Low-level Processes can be grouped together to achieve a
// common business goal.
type Process struct {
	common.CallableElement
	flow.ElementsContainer

	// An optional Boolean value specifying whether the Process is executable.
	// An executable Process is a private Process that has been modeled for the
	// purpose of being executed according to the semantics of Clause 14. Of
	// course, during the development cycle of the Process, there will be stages
	// where the Process does not have enough detail to be “executable.”
	// A non-executable Process is a private Process that has been modeled for
	// the purpose of documenting Process behavior at a modeler-defined level of
	// detail. Thus, information needed for execution, such as formal condition
	// expressions are typically not included in a non-executable Process.
	// For public Processes, no value has the same semantics as if the value
	// were false. The value MAY not be true for public Processes.
	//
	// DEV_NOTE: goBpm should run only executable processes, so this attribute
	// SHOULD be always true
	// isExecutable bool

	// Modeler-defined properties MAY be added to a Process. These properties
	// are contained within the Process. All Tasks and Sub-Processes SHALL have
	// access to these properties.
	//
	// DEV_NOTE: properties are indexed by the property names.
	properties map[string]*data.Property

	// Defines the resource roles that will perform or will be responsible for
	// the Process. The resource, e.g., a performer, can be specified in the
	// form of a specific individual, a group, an organization role or position,
	// or an organization.
	// Note that the assigned resources of the Process does not determine the
	// assigned resources of the Activities that are contained by the Process.
	roles map[string]*activities.ResourceRole

	// correlationSubscriptions are a feature of context-based correlation.
	// CorrelationSubscriptions are used to correlate incoming Messages against
	// data in the Process context. A Process MAY contain several
	// correlationSubscriptions.
	CorrelationSubscriptions []*common.CorrelationSubscription
}

// NewProcess creates a new Process and returns its pointer.
func NewProcess(
	name string,
	procOpts ...options.Option,
) (*Process, error) {
	pc := processConfig{
		name:     strings.Trim(name, " "),
		props:    map[string]*data.Property{},
		roles:    map[string]*activities.ResourceRole{},
		baseOpts: []options.Option{},
	}

	ee := []error{}

	for _, po := range procOpts {
		switch opt := po.(type) {
		case activities.RoleOption, data.PropertyOption:
			if err := opt.Apply(&pc); err != nil {
				ee = append(ee, err)
			}

		case foundation.BaseOption:
			pc.baseOpts = append(pc.baseOpts, opt)

		default:
			ee = append(ee,
				errs.New(
					errs.M("invalid option type for Process"),
					errs.C(errorClass, errs.BulidingFailed,
						errs.TypeCastingError),
					errs.D("option_type", reflect.TypeOf(opt).String())))
		}
	}

	if len(ee) > 0 {
		return nil, errors.Join(ee...)
	}

	return pc.newProcess()
}
