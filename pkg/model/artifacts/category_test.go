package artifacts

// var testCategoryValues = map[string]*CategoryValue{
// 	"one": NewCategoryValue("first", foundation.WithId("one")),
// 	"two": NewCategoryValue("second", foundation.WithId("two")),
// }

// func TestNewCategory(t *testing.T) {
// 	type args struct {
// 		id   string
// 		name string
// 		docs []*foundation.Documentation
// 	}

// 	tests := []struct {
// 		name string
// 		args args
// 		want *Category
// 	}{
// 		{
// 			name: "Normal",
// 			args: args{
// 				id:   "NormalId",
// 				name: "NormalTest",
// 			},
// 			want: &Category{
// 				BaseElement:    *foundation.MustBaseElement(foundation.WithId("NormalId")),
// 				Name:           "NormalTest",
// 				categoryValues: map[string]*CategoryValue{},
// 			},
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			if got := NewCategory(tt.args.id, tt.args.name, tt.args.docs...); !reflect.DeepEqual(got, tt.want) {
// 				t.Errorf("NewCategory() = %v, want %v", got, tt.want)
// 			}
// 		})
// 	}
// }

// func TestCategory_AddCategoryValues(t *testing.T) {
// 	type args struct {
// 		cvv []*CategoryValue
// 	}
// 	tests := []struct {
// 		name string
// 		c    *Category
// 		args args
// 		want int
// 	}{
// 		{
// 			name: "Normal",
// 			c:    NewCategory("NormalId", "NormalTest"),
// 			args: args{
// 				cvv: []*CategoryValue{
// 					testCategoryValues["one"],
// 					nil,
// 					testCategoryValues["two"],
// 					nil,
// 				},
// 			},
// 			want: 2,
// 		},
// 		{
// 			name: "InvalidStorage",
// 			c:    NewCategory("InvStorId", "InvStorTest"),
// 			args: args{
// 				cvv: []*CategoryValue{
// 					testCategoryValues["one"],
// 					nil,
// 					testCategoryValues["two"],
// 					nil,
// 				},
// 			},
// 			want: 2,
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			if got := tt.c.AddCategoryValues(tt.args.cvv...); got != tt.want {
// 				t.Errorf("Category.AddCategoryValues() = %v, want %v", got, tt.want)
// 			}
// 		})
// 	}
// }

// func TestCategory_RemoveCategoryValues(t *testing.T) {
// 	tstCVV := map[string]*CategoryValue{}

// 	for k, v := range testCategoryValues {
// 		tstCVV[k] = v
// 	}

// 	type args struct {
// 		cvv []string
// 	}
// 	tests := []struct {
// 		name string
// 		c    *Category
// 		args args
// 		want int
// 	}{
// 		{
// 			name: "Normal",
// 			c: &Category{
// 				BaseElement:    *foundation.NewBaseElement("NormalId"),
// 				Name:           "NormalTest",
// 				categoryValues: tstCVV,
// 			},
// 			args: args{
// 				cvv: []string{
// 					"one", "two", "three", "one",
// 				},
// 			},
// 			want: 2,
// 		},
// 		{
// 			name: "Invalid Storage",
// 			c: &Category{
// 				BaseElement: *foundation.NewBaseElement("InvStorId"),
// 				Name:        "InvStorTest",
// 			},
// 			args: args{
// 				cvv: []string{
// 					"one", "two", "three", "one",
// 				},
// 			},
// 			want: 0,
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			if got := tt.c.RemoveCategoryValues(tt.args.cvv...); got != tt.want {
// 				t.Errorf("Category.RemoveCategoryValues() = %v, want %v", got, tt.want)
// 			}
// 		})
// 	}
// }

// func TestCategory_CategoryValues(t *testing.T) {
// 	tests := []struct {
// 		name string
// 		c    *Category
// 		want []CategoryValue
// 	}{
// 		{
// 			name: "Normal",
// 			c: &Category{
// 				BaseElement:    *foundation.NewBaseElement("NormalId"),
// 				Name:           "NormalTest",
// 				categoryValues: testCategoryValues,
// 			},
// 			want: []CategoryValue{
// 				*testCategoryValues["one"],
// 				*testCategoryValues["two"],
// 			},
// 		},
// 		{
// 			name: "Invalid Storage",
// 			c: &Category{
// 				BaseElement: *foundation.NewBaseElement("InvStorId"),
// 				Name:        "InvStorTest",
// 			},
// 			want: []CategoryValue{},
// 		},
// 	}

// 	for i, tt := range tests {
// 		switch i {
// 		case 0:
// 			t.Run(tt.name, func(t *testing.T) {
// 				got := tt.c.CategoryValues()

// 				for k, v := range testCategoryValues {
// 					found := false

// 					for _, cv := range got {
// 						if cv.Id() == v.Id() {
// 							found = true
// 							break
// 						}
// 					}

// 					if !found {
// 						t.Errorf(
// 							"CategoryValues isn't found %v[%s] in %v", v, k, got)
// 					}
// 				}
// 			})

// 		case 1:
// 			t.Run(tt.name, func(t *testing.T) {
// 				if len(tests[1].c.CategoryValues()) != 0 {
// 					t.Error(
// 						"CategoryValues list should be empty on Invalid Storage")
// 				}
// 			})
// 		}
// 	}
// }

// func TestNewCategoryValue(t *testing.T) {
// 	type args struct {
// 		id    string
// 		value string
// 		docs  []*foundation.Documentation
// 	}
// 	tests := []struct {
// 		name string
// 		args args
// 		want *CategoryValue
// 	}{
// 		{
// 			name: "Normal",
// 			args: args{
// 				id:    "NormalId",
// 				value: "TestValue",
// 			},
// 			want: &CategoryValue{
// 				BaseElement:         *foundation.NewBaseElement("NormalId"),
// 				Value:               "TestValue",
// 				categorizedElements: map[string]*flow.Element{},
// 			},
// 		},
// 		{
// 			name: "No Value",
// 			args: args{
// 				id:    "NoValId",
// 				value: "",
// 			},
// 			want: &CategoryValue{
// 				BaseElement:         *foundation.NewBaseElement("NoValId"),
// 				Value:               undefinedCategoryValue,
// 				categorizedElements: map[string]*flow.Element{},
// 			},
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			if got := NewCategoryValue(tt.args.id, tt.args.value, tt.args.docs...); !reflect.DeepEqual(got, tt.want) {
// 				t.Errorf("NewCategoryValue() = %v, want %v", got, tt.want)
// 			}
// 		})
// 	}
// }

// func TestCategoryValue_Category(t *testing.T) {
// 	testCategory := NewCategory("CetegoryId", "NormalCategory")

// 	tests := []struct {
// 		name string
// 		cv   *CategoryValue
// 		want *Category
// 	}{
// 		{
// 			name: "Normal",
// 			cv:   NewCategoryValue("NormalId", "NormalTest"),
// 			want: testCategory,
// 		},
// 		{
// 			name: "Not binded to Category",
// 			cv:   NewCategoryValue("NoBindId", "NoBindTest"),
// 			want: nil,
// 		},
// 	}

// 	testCategory.AddCategoryValues(tests[0].cv)

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			if got := tt.cv.Category(); got != tt.want {
// 				t.Errorf("CategoryValue.Category() = %v, want %v", got, tt.want)
// 			}
// 		})
// 	}
// }

// func TestCategoryValue_AddFlowElement(t *testing.T) {
// 	testElements := map[string]*flow.Element{
// 		"one": flow.NewElement("one", "first"),
// 		"two": flow.NewElement("two", "second"),
// 	}

// 	type args struct {
// 		fee []*flow.Element
// 	}

// 	tests := []struct {
// 		name string
// 		cv   *CategoryValue
// 		args args
// 		want int
// 	}{
// 		{
// 			name: "Normal",
// 			cv:   NewCategoryValue("NormalId", "NormalTest"),
// 			args: args{
// 				fee: []*flow.Element{
// 					nil,
// 					testElements["one"],
// 					nil,
// 					testElements["two"],
// 				}},
// 			want: 2,
// 		},
// 		{
// 			name: "Invalid Storage",
// 			cv:   NewCategoryValue("InvStorId", "InvStorTest"),
// 			args: args{
// 				fee: []*flow.Element{
// 					nil,
// 					testElements["one"],
// 					nil,
// 					testElements["two"],
// 				}},
// 			want: 2,
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			if got := tt.cv.AddFlowElement(tt.args.fee...); got != tt.want {
// 				t.Errorf("CategoryValue.AddFlowElement() = %v, want %v", got, tt.want)
// 			}
// 		})
// 	}
// }

// func TestCategoryValue_RemoveFlowElement(t *testing.T) {
// 	testElements := map[string]*flow.Element{
// 		"one": flow.NewElement("one", "first"),
// 		"two": flow.NewElement("two", "second"),
// 	}

// 	type args struct {
// 		fee []*flow.Element
// 	}
// 	tests := []struct {
// 		name string
// 		cv   *CategoryValue
// 		args args
// 		want int
// 	}{
// 		{
// 			name: "Normal",
// 			cv:   NewCategoryValue("NormalId", "NormalTest"),
// 			args: args{
// 				fee: []*flow.Element{
// 					nil,
// 					testElements["one"],
// 					testElements["two"],
// 				},
// 			},
// 			want: 1,
// 		},
// 		{
// 			name: "InvalidStorage",
// 			cv: &CategoryValue{
// 				BaseElement: *foundation.NewBaseElement("InvStorId"),
// 				Value:       "",
// 			},
// 			args: args{
// 				fee: []*flow.Element{
// 					nil,
// 					testElements["one"],
// 					testElements["two"],
// 				},
// 			},
// 			want: 0,
// 		},
// 	}

// 	tests[0].cv.AddFlowElement(testElements["one"])

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			if got := tt.cv.RemoveFlowElement(tt.args.fee...); got != tt.want {
// 				t.Errorf("CategoryValue.RemoveFlowElement() = %v, want %v", got, tt.want)
// 			}
// 		})
// 	}
// }

// func TestCategoryValue_FlowElements(t *testing.T) {
// 	testElements := map[string]*flow.Element{
// 		"one": flow.NewElement("one", "first"),
// 		"two": flow.NewElement("two", "second"),
// 	}

// 	tests := []struct {
// 		name string
// 		cv   *CategoryValue
// 		want []*flow.Element
// 	}{
// 		{
// 			name: "Normal",
// 			cv:   NewCategoryValue("NormalId", "NormalTest"),
// 			want: []*flow.Element{
// 				testElements["one"],
// 				testElements["two"],
// 			},
// 		},
// 		{
// 			name: "Invalid Storage",
// 			cv: &CategoryValue{
// 				BaseElement: *foundation.NewBaseElement("InvStorId"),
// 				Value:       "InvStorTest",
// 			},
// 			want: []*flow.Element{},
// 		},
// 	}

// 	tests[0].cv.AddFlowElement(testElements["one"], testElements["two"])

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			if got := tt.cv.FlowElements(); !reflect.DeepEqual(got, tt.want) {
// 				t.Errorf("CategoryValue.FlowElements() = %v, want %v", got, tt.want)
// 			}
// 		})
// 	}
// }
