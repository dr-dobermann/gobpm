package artifacts

import (
	"reflect"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

var testCategoryValues = map[string]*CategoryValue{
	"one": NewCategoryValue("one", "first"),
	"two": NewCategoryValue("two", "second"),
}

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
				id:   "NormalId",
				name: "NormalTest",
			},
			want: &Category{
				BaseElement:    *foundation.NewBaseElement("NormalId"),
				Name:           "NormalTest",
				categoryValues: map[string]*CategoryValue{},
			},
		},
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
		{
			name: "Normal",
			c:    NewCategory("NormalId", "NormalTest"),
			args: args{
				cvv: []*CategoryValue{
					testCategoryValues["one"],
					nil,
					testCategoryValues["two"],
					nil,
				},
			},
			want: 2,
		},
		{
			name: "InvalidStorage",
			c:    NewCategory("InvStorId", "InvStorTest"),
			args: args{
				cvv: []*CategoryValue{
					testCategoryValues["one"],
					nil,
					testCategoryValues["two"],
					nil,
				},
			},
			want: 2,
		},
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
	tstCVV := map[string]*CategoryValue{}

	for k, v := range testCategoryValues {
		tstCVV[k] = v
	}

	type args struct {
		cvv []string
	}
	tests := []struct {
		name string
		c    *Category
		args args
		want int
	}{
		{
			name: "Normal",
			c: &Category{
				BaseElement:    *foundation.NewBaseElement("NormalId"),
				Name:           "NormalTest",
				categoryValues: tstCVV,
			},
			args: args{
				cvv: []string{
					"one", "two", "three", "one",
				},
			},
			want: 2,
		},
		{
			name: "Invalid Storage",
			c: &Category{
				BaseElement: *foundation.NewBaseElement("InvStorId"),
				Name:        "InvStorTest",
			},
			args: args{
				cvv: []string{
					"one", "two", "three", "one",
				},
			},
			want: 0,
		},
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
		{
			name: "Normal",
			c: &Category{
				BaseElement:    *foundation.NewBaseElement("NormalId"),
				Name:           "NormalTest",
				categoryValues: testCategoryValues,
			},
			want: []CategoryValue{
				*testCategoryValues["one"],
				*testCategoryValues["two"],
			},
		},
		{
			name: "Invalid Storage",
			c: &Category{
				BaseElement: *foundation.NewBaseElement("InvStorId"),
				Name:        "InvStorTest",
			},
			want: []CategoryValue{},
		},
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
		{
			name: "Normal",
			args: args{
				id:    "NormalId",
				value: "TestValue",
			},
			want: &CategoryValue{
				BaseElement:         *foundation.NewBaseElement("NormalId"),
				Value:               "TestValue",
				categorizedElements: map[string]*flow.Element{},
			},
		},
		{
			name: "No Value",
			args: args{
				id:    "NoValId",
				value: "",
			},
			want: &CategoryValue{
				BaseElement:         *foundation.NewBaseElement("NoValId"),
				Value:               undefinedCategoryValue,
				categorizedElements: map[string]*flow.Element{},
			},
		},
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
	testElements := map[string]*flow.Element{
		"one": flow.NewElement("one", "first"),
		"two": flow.NewElement("two", "second"),
	}

	type args struct {
		fee []*flow.Element
	}

	tests := []struct {
		name string
		cv   *CategoryValue
		args args
		want int
	}{
		{
			name: "Normal",
			args: args{
				fee: []*flow.Element{
					nil,
					testElements["one"],
					nil,
					testElements["two"],
				},
			},
			want: 2,
		},
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
