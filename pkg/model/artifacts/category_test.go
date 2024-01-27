package artifacts

import (
	"reflect"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

func TestNewCategory(t *testing.T) {
	type args struct {
		id   string
		name string
		docs []*foundation.Documentation
	}
	tests := []struct {
		name string
		args args
		want *Category
	}{
	// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NewCategory(tt.args.id, tt.args.name, tt.args.docs...); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NewCategory() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCategory_AddCategoryValues(t *testing.T) {
	type args struct {
		cvv []*CategoryValue
	}
	tests := []struct {
		name string
		c    *Category
		args args
		want int
	}{
	// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.c.AddCategoryValues(tt.args.cvv...); got != tt.want {
				t.Errorf("Category.AddCategoryValues() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCategory_RemoveCategoryValues(t *testing.T) {
	type args struct {
		cvv []*CategoryValue
	}
	tests := []struct {
		name string
		c    *Category
		args args
		want int
	}{
	// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.c.RemoveCategoryValues(tt.args.cvv...); got != tt.want {
				t.Errorf("Category.RemoveCategoryValues() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCategory_CategoryValues(t *testing.T) {
	tests := []struct {
		name string
		c    *Category
		want []CategoryValue
	}{
	// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.c.CategoryValues(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Category.CategoryValues() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNewCategoryValue(t *testing.T) {
	type args struct {
		id    string
		value string
		docs  []*foundation.Documentation
	}
	tests := []struct {
		name string
		args args
		want *CategoryValue
	}{
	// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NewCategoryValue(tt.args.id, tt.args.value, tt.args.docs...); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NewCategoryValue() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCategoryValue_AddFlowElement(t *testing.T) {
	type args struct {
		fee []*flow.Element
	}
	tests := []struct {
		name string
		cv   *CategoryValue
		args args
		want int
	}{
	// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cv.AddFlowElement(tt.args.fee...); got != tt.want {
				t.Errorf("CategoryValue.AddFlowElement() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCategoryValue_RemoveFlowElement(t *testing.T) {
	type args struct {
		fee []*flow.Element
	}
	tests := []struct {
		name string
		cv   *CategoryValue
		args args
		want int
	}{
	// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cv.RemoveFlowElement(tt.args.fee...); got != tt.want {
				t.Errorf("CategoryValue.RemoveFlowElement() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCategoryValue_FlowElements(t *testing.T) {
	tests := []struct {
		name string
		cv   *CategoryValue
		want []*flow.Element
	}{
	// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cv.FlowElements(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CategoryValue.FlowElements() = %v, want %v", got, tt.want)
			}
		})
	}
}
