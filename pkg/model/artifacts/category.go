package artifacts

import (
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// *****************************************************************************

const (
	unspecifiedCategory    = "UNSPECIFIED_CATEGORY"
	undefinedCategoryValue = "UNDEFINED_CATEGORY_VALUE"
)

// Categories, which have user-defined semantics, can be used for documentation
// or analysis purposes. For example, FlowElements can be categorized as being
// customer oriented vs. support oriented. Furthermore, the cost and time of
// Activities per Category can be calculated.
type Category struct {
	foundation.BaseElement

	// The descriptive name of the element.
	name string

	// The categoryValue attribute specifies one or more values of the Category.
	// For example, the Category is “Region” then this Category could specify
	// values like “North,” “South,” “West,” and “East.”
	categoryValues map[string]*CategoryValue
}

// NewCategory creates a new Category and returns its pointer
func NewCategory(name string, baseOpts ...options.Option) *Category {
	if name == "" {
		name = unspecifiedCategory
	}

	return &Category{
		BaseElement:    *foundation.MustBaseElement(baseOpts...),
		name:           name,
		categoryValues: map[string]*CategoryValue{},
	}
}

// Name returns the Category name.
func (c *Category) Name() string {
	return c.name
}

// AddCategoryValues adds CategoryValues from the list into the Category and
// binds the added CategoryValue to the Category.
// It returns a number of added CategoryValues.
func (c *Category) AddCategoryValues(cvv ...*CategoryValue) int {
	if c.categoryValues == nil {
		c.categoryValues = map[string]*CategoryValue{}
	}

	n := 0
	for _, cv := range cvv {
		if cv == nil {
			continue
		}

		c.categoryValues[cv.Value] = cv
		cv.category = c

		n++
	}

	return n
}

// RemoveCategoryValues removes given CategoryValues from the Category
// and for removed ones clears its binding to Category.
// It returns a number of removed elements.
func (c *Category) RemoveCategoryValues(cvVals ...string) int {
	if c.categoryValues == nil {
		c.categoryValues = map[string]*CategoryValue{}

		return 0
	}

	n := 0

	for _, cvVal := range cvVals {
		if cv, ok := c.categoryValues[cvVal]; ok {
			cv.category = nil
			delete(c.categoryValues, cvVal)

			n++
		}
	}

	return n
}

// CategoryValues returns list of copies of CategoryValues binded to Category.
func (c *Category) CategoryValues() []CategoryValue {
	cvv := []CategoryValue{}

	if c.categoryValues == nil {
		c.categoryValues = map[string]*CategoryValue{}
		return cvv
	}

	for _, cv := range c.categoryValues {
		cvv = append(cvv, *cv)
	}

	return cvv
}

// *****************************************************************************

type CategoryValue struct {
	foundation.BaseElement

	// This attribute provides the value of the CategoryValue element.
	Value string

	// The category attribute specifies the Category representing the Category
	// as such and contains the CategoryValue.
	category *Category

	// The FlowElements attribute identifies all of the elements (e.g., Events,
	// Activities, Gateways, and Artifacts) that are within the boundaries of
	// the Group.
	// Map uses FlowElement Id as a key.
	categorizedElements map[string]*flow.Element
}

// NewCategoryValue creates a new CategoryValue and returns its pointer.
func NewCategoryValue(
	value string,
	baseOpts ...options.Option,
) *CategoryValue {
	if value == "" {
		value = undefinedCategoryValue
	}

	return &CategoryValue{
		BaseElement:         *foundation.MustBaseElement(baseOpts...),
		Value:               value,
		categorizedElements: map[string]*flow.Element{},
	}
}

// Category returns category the CategoryValue binded to.
func (cv *CategoryValue) Category() *Category {
	return cv.category
}

// AddFlowElement adds FlowElements to the CategoryValue.
// It returns a number of added FlowElements
func (cv *CategoryValue) AddFlowElement(fee ...*flow.Element) int {
	if cv.categorizedElements == nil {
		cv.categorizedElements = map[string]*flow.Element{}
	}

	n := 0

	for _, fe := range fee {
		if fe == nil {
			continue
		}

		cv.categorizedElements[fe.Id()] = fe

		n++
	}

	return n
}

// RemoveFlowElement removes FlowElements from the CategoryValue.
func (cv *CategoryValue) RemoveFlowElement(fee ...*flow.Element) int {
	if cv.categorizedElements == nil {
		cv.categorizedElements = map[string]*flow.Element{}

		return 0
	}

	n := 0

	for _, fe := range fee {
		if fe == nil {
			continue
		}

		if _, ok := cv.categorizedElements[fe.Id()]; ok {
			delete(cv.categorizedElements, fe.Id())
			n++
		}
	}

	return n
}

// FlowElements returns a list of categorized FlowElements from CategoryValue.
func (cv *CategoryValue) FlowElements() []*flow.Element {
	fee := []*flow.Element{}

	if cv.categorizedElements == nil {
		cv.categorizedElements = map[string]*flow.Element{}
		return fee
	}

	for _, fe := range cv.categorizedElements {
		fee = append(fee, fe)
	}

	return fee
}
