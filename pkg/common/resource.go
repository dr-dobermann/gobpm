package common

import (
	"github.com/dr-dobermann/gobpm/pkg/foundation"
	"github.com/dr-dobermann/gobpm/pkg/variables"
)

type ResourceParameter struct {
	foundation.BaseElement

	Description  variables.Variable
	IsCollection bool
	IsRequired   bool
}

type Resource struct {
	Name       string
	Parameters []ResourceParameter
}
