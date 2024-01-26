package artifacts

import (
	"reflect"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
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
		{
			name: "Normal",
			args: args{
				id:   "NormalID",
				name: "NormalCategory",
				docs: []*foundation.Documentation{
					foundation.NewDoc("NormalDoc", ""),
				},
			},
			want: &Category{
				BaseElement: *foundation.NewBaseElement("NormalID",
					foundation.NewDoc("NormalDoc", "")),
				Name: "NormalCategory",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewCategory(tt.args.id, tt.args.name, tt.args.docs...)
			require.Equal(t, got.Id(), tt.args.id)
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
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.c.AddCategoryValues(tt.args.cvv...)
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
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.c.RemoveCategoryValues(tt.args.cvv...)
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
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.cv.AddFlowElement(tt.args.fee...)
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
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.cv.RemoveFlowElement(tt.args.fee...)
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
