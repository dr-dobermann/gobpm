package artifacts_test

import (
	"testing"

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
}
