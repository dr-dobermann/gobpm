package activities

import "github.com/dr-dobermann/gobpm/pkg/common"

type SubProcess struct {
	common.FlowElementContainer
	Activity

	triggeredByEvent bool
}
