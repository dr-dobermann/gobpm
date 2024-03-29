package common

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
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

// *****************************************************************************
type Resource struct {
	// This attribute specifies the name of the Resource.
	name string

	// This model association specifies the definition of the parameters
	// needed at runtime to resolve the Resource.
	parameters []ResourceParameter
}

// NewResource creates a new Resource and returns its pointer.
func NewResource(name string, params ...*ResourceParameter) *Resource {
	pp := make([]ResourceParameter, 0, len(params))

	for _, p := range params {
		if p != nil {
			pp = append(pp, *p)
		}
	}

	return &Resource{
		name:       name,
		parameters: pp,
	}
}

// *****************************************************************************
type ResourceParameter struct {
	foundation.BaseElement

	// Specifies the name of the query parameter.
	name string

	// Specifies the type of the query parameter.
	// DEV_NOTE: parameter type is a string so there is no necessity to
	// use ItemDefinition for it as Standard demands.
	// paramType data.ItemDefinition
	paramType string

	// Specifies, if a parameter is optional or mandatory.
	isRequiered bool
}

// NewResourceParameter creates a new ResourceParameter and returns its pointer
// on success or error on failure.
func NewResourceParameter(
	name, pType string,
	required bool,
	baseOpts ...options.Option,
) (*ResourceParameter, error) {
	name = Strim(name)
	if err := CheckStr(
		name,
		"ResourceParameter should have a name", errorClass); err != nil {
		return nil, err
	}

	pType = Strim(pType)
	if err := CheckStr(
		pType,
		"Type should be set for ResourceParameter", errorClass); err != nil {
		return nil, err
	}

	be, err := foundation.NewBaseElement(baseOpts...)
	if err != nil {
		return nil, err
	}

	return &ResourceParameter{
			BaseElement: *be,
			name:        name,
			paramType:   pType,
			isRequiered: required},
		nil
}

// MustResourcParameter tries to create a new ResourceParameter on success or
// panics on failure.
func MustResourcParameter(
	name, pType string,
	required bool,
	baseOpts ...options.Option,
) *ResourceParameter {
	rp, err := NewResourceParameter(name, pType, required, baseOpts...)
	if err != nil {
		errs.Panic(err)
	}

	return rp
}
