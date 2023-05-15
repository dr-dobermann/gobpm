package artifact

import (
	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

type Category struct {
	foundation.BaseElement

	name           string
	сategoryValues []*CategoryValue
}

type CategoryValue struct {
	value                   string
	category                *Category
	categorizedFlowElements []*common.FlowElement
}

type Group struct {
	foundation.BaseElement

	categoryValueRef *CategoryValue
}
