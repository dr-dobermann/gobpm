package artifacts_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockflow"
	"github.com/dr-dobermann/gobpm/pkg/model/artifacts"
	"github.com/stretchr/testify/require"
)

func TestCategory(t *testing.T) {
	t.Run("new category",
		func(t *testing.T) {
			c := artifacts.NewCategory("normal_category")

			require.NotEmpty(t, c)

			require.Equal(t, "normal_category", c.Name())
			require.Empty(t, c.CategoryValues())
			require.Equal(t, 0, c.RemoveCategoryValues("not_xisted", ""))
		})

	t.Run("new category with empty name", func(t *testing.T) {
		c := artifacts.NewCategory("")
		require.NotNil(t, c)
		require.Equal(t, "UNSPECIFIED_CATEGORY", c.Name())
	})

	t.Run("add category value",
		func(t *testing.T) {
			c := artifacts.NewCategory("empty category")

			require.Equal(t, 2, c.AddCategoryValues(
				artifacts.NewCategoryValue("one"),
				nil,
				artifacts.NewCategoryValue("two"),
			))
			require.Equal(t, 2, len(c.CategoryValues()))
		})

	t.Run("remove category values",
		func(t *testing.T) {
			c := artifacts.NewCategory("test category")
			cv1 := artifacts.NewCategoryValue("value1")
			cv2 := artifacts.NewCategoryValue("value2")

			// Add values first
			c.AddCategoryValues(cv1, cv2)
			require.Equal(t, 2, len(c.CategoryValues()))

			// Remove one value
			removed := c.RemoveCategoryValues("value1")
			require.Equal(t, 1, removed)
			require.Equal(t, 1, len(c.CategoryValues()))
			require.Nil(t, cv1.Category())

			// Try to remove non-existent value
			removed = c.RemoveCategoryValues("non-existent")
			require.Equal(t, 0, removed)
			require.Equal(t, 1, len(c.CategoryValues()))

			// Remove remaining value
			removed = c.RemoveCategoryValues("value2")
			require.Equal(t, 1, removed)
			require.Equal(t, 0, len(c.CategoryValues()))
			require.Nil(t, cv2.Category())
		})
}

func TestCategoryValue(t *testing.T) {
	t.Run("new category value", func(t *testing.T) {
		cv := artifacts.NewCategoryValue("test-value")
		require.NotNil(t, cv)
		require.Equal(t, "test-value", cv.Value)
		require.NotEmpty(t, cv.ID())
		require.Nil(t, cv.Category())
		require.Empty(t, cv.FlowElements())
	})

	t.Run("empty category value", func(t *testing.T) {
		cv := artifacts.NewCategoryValue("")
		require.NotNil(t, cv)
		require.Equal(t, "UNDEFINED_CATEGORY_VALUE", cv.Value)
	})

	t.Run("category binding", func(t *testing.T) {
		c := artifacts.NewCategory("test-category")
		cv := artifacts.NewCategoryValue("test-value")

		require.Nil(t, cv.Category())

		// Add category value to category
		c.AddCategoryValues(cv)
		require.NotNil(t, cv.Category())
		require.Equal(t, c, cv.Category())
	})

	t.Run("flow elements management", func(t *testing.T) {
		cv := artifacts.NewCategoryValue("test-value")

		// Initially empty
		require.Empty(t, cv.FlowElements())

		// Create mock flow elements
		fe1 := mockflow.NewMockElement(t)
		fe1.EXPECT().ID().Return("fe1")

		fe2 := mockflow.NewMockElement(t)
		fe2.EXPECT().ID().Return("fe2")

		// Add flow elements
		added := cv.AddFlowElement(fe1, nil, fe2)
		require.Equal(t, 2, added)

		elements := cv.FlowElements()
		require.Len(t, elements, 2)

		// Check that elements are in the list
		elementIds := make(map[string]bool)
		for _, el := range elements {
			elementIds[el.ID()] = true
		}
		require.True(t, elementIds["fe1"])
		require.True(t, elementIds["fe2"])

		// Remove one element
		removed := cv.RemoveFlowElement("fe1")
		require.Equal(t, 1, removed)
		require.Len(t, cv.FlowElements(), 1)

		// Try to remove non-existent element
		removed = cv.RemoveFlowElement("non-existent", "")
		require.Equal(t, 0, removed)
		require.Len(t, cv.FlowElements(), 1)

		// Remove remaining element
		removed = cv.RemoveFlowElement("fe2")
		require.Equal(t, 1, removed)
		require.Empty(t, cv.FlowElements())
	})

	t.Run("flow elements edge cases", func(t *testing.T) {
		cv := artifacts.NewCategoryValue("test-value")

		// Test adding to nil categorizedElements (should initialize)
		fe1 := mockflow.NewMockElement(t)
		fe1.EXPECT().ID().Return("fe1")

		added := cv.AddFlowElement(fe1)
		require.Equal(t, 1, added)

		// Test removing from empty map
		cv2 := artifacts.NewCategoryValue("empty-value")
		removed := cv2.RemoveFlowElement("nonexistent")
		require.Equal(t, 0, removed)

		// Test FlowElements() on nil map
		require.Empty(t, cv2.FlowElements())
	})
}
