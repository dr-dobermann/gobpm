package activities

import "github.com/dr-dobermann/gobpm/pkg/model/common"

type SubProcess struct {
	common.FlowElementContainer
	Activity

	triggeredByEvent bool
}
