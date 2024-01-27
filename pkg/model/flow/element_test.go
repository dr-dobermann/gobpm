package flow

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

var testElements = map[string]*Element{
	"one": NewElement("one", "first"),
	"two": NewElement("two", "second"),
}

func TestNewElement(t *testing.T) {
	type args struct {
		id   string
		name string
		docs []*foundation.Documentation
	}
	tests := []struct {
		name string
		args args
		want *Element
	}{
		{
			name: "Normal",
			args: args{
				id:   "NormalId",
				name: "Test",
			},
			want: &Element{
				BaseElement: *foundation.NewBaseElement("NormalId"),
				name:        "Test",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NewElement(tt.args.id, tt.args.name, tt.args.docs...); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NewElement() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestElement_Name(t *testing.T) {
	tests := []struct {
		name string
		fe   *Element
		want string
	}{
		{
			name: "Normal",
			fe: &Element{
				BaseElement: foundation.BaseElement{},
				name:        "TestName",
				container:   &ElementsContainer{},
			},
			want: "TestName",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.fe.Name(); got != tt.want {
				t.Errorf("Element.Name() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestElement_Container(t *testing.T) {
	cont := &ElementsContainer{}

	tests := []struct {
		name string
		fe   *Element
		want *ElementsContainer
	}{
		{
			name: "Normal",
			fe: &Element{
				BaseElement: foundation.BaseElement{},
				name:        "",
				container:   cont,
			},
			want: cont,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.fe.Container(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Element.Container() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNewContainer(t *testing.T) {
	type args struct {
		id   string
		docs []*foundation.Documentation
	}

	tests := []struct {
		name string
		args args
		want *ElementsContainer
	}{
		{
			name: "Normal",
			args: args{
				id: "Test",
			},
			want: &ElementsContainer{
				BaseElement: *foundation.NewBaseElement("Test"),
				elements:    map[string]*Element{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NewContainer(tt.args.id, tt.args.docs...); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NewContainer() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestElementsContainer_Add(t *testing.T) {
	type args struct {
		fee []*Element
	}

	tests := []struct {
		name string
		fec  *ElementsContainer
		args args
		want int
	}{
		{
			name: "Normal",
			fec:  NewContainer("tstId"),
			args: args{
				fee: []*Element{
					testElements["one"], nil, testElements["two"],
				},
			},
			want: 2,
		},
		{
			name: "Invalid container",
			fec:  &ElementsContainer{},
			args: args{
				fee: []*Element{
					testElements["one"], nil, testElements["two"],
				},
			},
			want: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.fec.Add(tt.args.fee...); got != tt.want {
				t.Errorf("ElementsContainer.Add() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestElementsContainer_Remove(t *testing.T) {
	type args struct {
		idd []string
	}

	tstEls := map[string]*Element{}
	for k, v := range testElements {
		tstEls[k] = v
	}

	tests := []struct {
		name string
		fec  *ElementsContainer
		args args
		want int
	}{
		{
			name: "Invalid container",
			fec:  &ElementsContainer{},
			args: args{
				idd: []string{
					testElements["one"].Id(),
				},
			},

			want: 0,
		},
		{
			name: "Normale",
			fec: &ElementsContainer{
				elements: tstEls,
			},
			args: args{
				idd: []string{
					testElements["one"].Id(),
					"invalid_id",
				},
			},
			want: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.fec.Remove(tt.args.idd...); got != tt.want {
				t.Errorf("ElementsContainer.Remove() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestElementsContainer_Contains(t *testing.T) {
	type args struct {
		elementId string
	}

	tests := []struct {
		name string
		fec  *ElementsContainer
		args args
		want bool
	}{
		{
			name: "Invalid container",
			fec:  &ElementsContainer{},
			args: args{
				elementId: "one",
			},
			want: false,
		},
		{
			name: "Normal",
			fec: &ElementsContainer{
				elements: testElements,
			},
			args: args{
				elementId: "one",
			},
			want: true,
		},
		{
			name: "Non existed element",
			fec:  &ElementsContainer{},
			args: args{
				elementId: "invlaid_id",
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.fec.Contains(tt.args.elementId); got != tt.want {
				t.Errorf("ElementsContainer.Contains() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestElementsContainer_Elements(t *testing.T) {
	tests := []struct {
		name string
		fec  *ElementsContainer
		want []*Element
	}{
		{
			name: "Normal",
			fec: &ElementsContainer{
				elements: testElements,
			},
			want: []*Element{
				testElements["two"],
				testElements["one"],
			},
		},
		{
			name: "Invalid container",
			fec:  &ElementsContainer{},
			want: []*Element{},
		},
	}

	fmt.Println(testElements)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.fec.Elements(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ElementsContainer.Elements() = %v, want %v", got, tt.want)
			}
		})
	}
}
