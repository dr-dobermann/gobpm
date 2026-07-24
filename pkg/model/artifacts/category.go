package artifacts

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// *****************************************************************************

const (
	unspecifiedCategory    = "UNSPECIFIED_CATEGORY"
	undefinedCategoryValue = "UNDEFINED_CATEGORY_VALUE"
)

// Category can have user-defined semantics, and can be used for documentation
// or analysis purposes. For example, BaseElements can be categorized as being
// customer oriented vs. support oriented. Furthermore, the cost and time of
// Activities per Category can be calculated.
type Category struct {
	categoryValues map[string]*CategoryValue
	name           string
	foundation.BaseElement
}

// NewCategory creates a new Category and returns its pointer, or an error on
// an invalid option (FIX-026 — caller options are validated, never a
// deferred panic).
func NewCategory(
	name string,
	baseOpts ...options.Option,
) (*Category, error) {
	if name == "" {
		name = unspecifiedCategory
	}

	be, err := foundation.NewBaseElement(baseOpts...)
	if err != nil {
		return nil, err
	}

	return &Category{
		BaseElement:    *be,
		name:           name,
		categoryValues: map[string]*CategoryValue{},
	}, nil
}

// MustCategory is the panic-on-error NewCategory twin for tests and static
// process construction.
func MustCategory(name string, baseOpts ...options.Option) *Category {
	c, err := NewCategory(name, baseOpts...)
	if err != nil {
		errs.Panic(err)

		return nil
	}

	return c
}

// Name returns the Category name.
func (c *Category) Name() string {
	return c.name
}

// MustCategoryValue is the panic-on-error NewCategoryValue twin for tests
// and static process construction.
func MustCategoryValue(
	value string,
	baseOpts ...options.Option,
) *CategoryValue {
	cv, err := NewCategoryValue(value, baseOpts...)
	if err != nil {
		errs.Panic(err)

		return nil
	}

	return cv
}

// AddCategoryValues adds CategoryValues from the list into the Category and
// binds the added CategoryValue to the Category.
// It returns a number of added CategoryValues.
func (c *Category) AddCategoryValues(cvv ...*CategoryValue) int {
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
	cvv := make([]CategoryValue, 0, len(c.categoryValues))

	for _, cv := range c.categoryValues {
		cvv = append(cvv, *cv)
	}

	return cvv
}

// *****************************************************************************

// CategoryValue represents a value within a category.
type CategoryValue struct {
	category            *Category
	categorizedElements map[string]flow.Element
	Value               string
	foundation.BaseElement
}

// NewCategoryValue creates a new CategoryValue and returns its pointer.
func NewCategoryValue(
	value string,
	baseOpts ...options.Option,
) (*CategoryValue, error) {
	if value == "" {
		value = undefinedCategoryValue
	}

	be, err := foundation.NewBaseElement(baseOpts...)
	if err != nil {
		return nil, err
	}

	return &CategoryValue{
		BaseElement:         *be,
		Value:               value,
		categorizedElements: map[string]flow.Element{},
	}, nil
}

// Category returns category the CategoryValue binded to.
func (cv *CategoryValue) Category() *Category {
	return cv.category
}

// AddBaseElement adds BaseElements to the CategoryValue.
// It returns a number of added BaseElements
func (cv *CategoryValue) AddBaseElement(fee ...flow.Element) int {
	n := 0

	for _, fe := range fee {
		if fe == nil {
			continue
		}

		cv.categorizedElements[fe.ID()] = fe

		n++
	}

	return n
}

// RemoveBaseElement removes BaseElements from the CategoryValue.
func (cv *CategoryValue) RemoveBaseElement(feeID ...string) int {
	n := 0

	for _, fe := range feeID {
		fe = strings.TrimSpace(fe)
		if fe == "" {
			continue
		}

		if _, ok := cv.categorizedElements[fe]; ok {
			delete(cv.categorizedElements, fe)
			n++
		}
	}

	return n
}

// BaseElements returns a list of categorized BaseElements from CategoryValue.
func (cv *CategoryValue) BaseElements() []flow.Element {
	fee := make([]flow.Element, 0, len(cv.categorizedElements))

	for _, fe := range cv.categorizedElements {
		fee = append(fee, fe)
	}

	return fee
}
