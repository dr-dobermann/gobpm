package artifacts

import (
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// *****************************************************************************

const (
	unspecifiedCategory = "UNSPECIFIED_CATEGORY"

	undefinedCategoryValue = "UNDEFINED_CATEGORY_VALUE"
)

// Categories, which have user-defined semantics, can be used for documentation
// or analysis purposes. For example, FlowElements can be categorized as being
// customer oriented vs. support oriented. Furthermore, the cost and time of
// Activities per Category can be calculated.
type Category struct {
	foundation.BaseElement

	// The descriptive name of the element.
	Name string

	// The categoryValue attribute specifies one or more values of the Category.
	// For example, the Category is “Region” then this Category could specify
	// values like “North,” “South,” “West,” and “East.”
	categoryValues map[string]*CategoryValue
}

// NewCategory creates a new Category and returns its pointer
func NewCategory(id, name string, docs ...*foundation.Documentation) *Category {
	if name == "" {
		name = unspecifiedCategory
	}

	return &Category{
		BaseElement:    *foundation.NewBaseElement(id, docs...),
		Name:           name,
		categoryValues: map[string]*CategoryValue{},
	}
}

// AddCategoryValues adds CategoryValues from the list into the Category and
// binds the added CategoryValue to the Category.
// Doesn't fire any error or panic in case of duplication.
// It panics if is not properly created.
func (c *Category) AddCategoryValues(cvv ...*CategoryValue) {
	if c.categoryValues == nil {
		panic("Category should be created with artifacts.NewCategory call")
	}

	for _, cv := range cvv {
		if cv == nil {
			panic("couldn't add nil CategoryValue to category " + c.Name)
		}

		c.categoryValues[cv.Value] = cv
		cv.category = c
	}
}

// RemoveCategoryValues removes given CategoryValues from the Category
// and for removed ones clears its binding to Category.
// It doesn't fire any error or panic in case there is no CategoryValue in
// Category.
// It panics if is not properly created.
func (c *Category) RemoveCategoryValues(cvv ...*CategoryValue) {
	if c.categoryValues == nil {
		panic("Category should be created with artifacts.NewCategory call")
	}

	for _, cv := range cvv {
		if cv == nil {
			panic("couldn't remove nil CategoryValue from category " + c.Name)
		}

		if _, ok := c.categoryValues[cv.Value]; ok {
			cv.category = nil
			delete(c.categoryValues, cv.Value)
		}
	}
}

// CategoryValues returns list of copies of CategoryValues binded to Category.
// It panics if is not properly created.
func (c *Category) CategoryValues() []CategoryValue {
	if c.categoryValues == nil {
		panic("Category should be created with artifacts.NewCategory call")
	}

	cvv := []CategoryValue{}

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
	categorizedElements map[string]*flow.Element
}

// NewCategoryValue creates a new CategoryValue and returns its pointer.
func NewCategoryValue(
	id, value string,
	docs ...*foundation.Documentation,
) *CategoryValue {
	if value == "" {
		value = undefinedCategoryValue
	}

	return &CategoryValue{
		BaseElement:         *foundation.NewBaseElement(id, docs...),
		Value:               value,
		categorizedElements: map[string]*flow.Element{},
	}
}

// AddFlowElement adds FlowElements to the CategoryValue.
// Function don't check duplication.
// It panics in case of not properly created CategoryValue or empty
// FlowElement pointer given.
func (cv *CategoryValue) AddFlowElement(fee ...*flow.Element) {
	if cv.categorizedElements == nil {
		panic("CategoryValue should be created by artifacts.NewCategoryValue")
	}

	for _, fe := range fee {
		if fe == nil {
			panic("couldn't add nil FlowElement to CategoryValue " + cv.Value)
		}

		cv.categorizedElements[fe.Id()] = fe
	}
}

// RemoveFlowElement removes FlowElements from the CategoryValue.
// It panics if isn't properly created or nil FlowElement pointer given.
func (cv *CategoryValue) RemoveFlowElement(fee ...*flow.Element) {
	if cv.categorizedElements == nil {
		panic("CategoryValue should be created by artifacts.NewCategoryValue")
	}

	for _, fe := range fee {
		if fe == nil {
			panic("couldn't remove nil FlowElement to CategoryValue " +
				cv.Value)
		}

		delete(cv.categorizedElements, fe.Id())
	}
}

// FlowElements returns a list of categorized FlowElements from CategoryValue.
func (cv *CategoryValue) FlowElements() []*flow.Element {
	fee := []*flow.Element{}

	for _, fe := range cv.categorizedElements {
		fee = append(fee, fe)
	}

	return fee
}
