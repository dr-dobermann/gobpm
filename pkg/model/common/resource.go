package common

import (
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// The Resource class is used to specify resources that can be referenced by
// Activities. These Resources can be Human Resources as well as any other
// resource assigned to Activities during Process execution time.
// The definition of a Resource is “abstract,” because it only defines the
// Resource, without detailing how e.g., actual user IDs are associated at
// runtime. Multiple Activities can utilize the same Resource.
//
// Every Resource can define a set of ResourceParameters. These parameters
// can be used at runtime to define query e.g., into an Organizational
// Directory. Every Activity referencing a parameterized Resource can bind
// values available in the scope of the Activity to these parameters.
type Resource struct {
	// This attribute specifies the name of the Resource.
	Name string

	// This model association specifies the definition of the parameters
	// needed at runtime to resolve the Resource.
	Parameters []ResourceParameter
}

type ResourceParameter struct {
	foundation.BaseElement

	// Specifies the name of the query parameter.
	Name string

	// Specifies the type of the query parameter.
	Type data.ItemDefinition

	// Specifies, if a parameter is optional or mandatory.
	IsRequiered bool
}
