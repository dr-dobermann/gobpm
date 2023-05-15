package data

import (
	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

type DataState struct {
	foundation.BaseElement

	StateValue string
}

// ItemAwareElement creates a link to a single value or a
// collection of the values
type ItemAwareElement struct {
	foundation.BaseElement

	ItemSubject common.ItemDefinition
	State       *DataState
}
