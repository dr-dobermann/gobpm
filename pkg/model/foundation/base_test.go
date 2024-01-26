package foundation

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewDoc(t *testing.T) {
	type args struct {
		text   string
		format string
	}

	tests := []struct {
		name string
		args args
		want *Documentation
	}{
		{
			name: "Empty",
			args: args{
				text:   "",
				format: "",
			},
			want: &Documentation{
				text:   "",
				format: defaultDocFormat,
			},
		},
		{
			name: "Filled",
			args: args{
				text:   "Filled",
				format: "text/rtf",
			},
			want: &Documentation{
				text:   "Filled",
				format: "text/rtf",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NewDoc(tt.args.text, tt.args.format); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NewDoc() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDocumentation_Text(t *testing.T) {
	tests := []struct {
		name string
		d    Documentation
		want string
	}{
		{
			name: "Normal",
			d: Documentation{
				text:   "TestDoc",
				format: defaultDocFormat,
			},
			want: "TestDoc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.d.Text(); got != tt.want {
				t.Errorf("Documentation.Text() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDocumentation_Format(t *testing.T) {
	tests := []struct {
		name string
		d    Documentation
		want string
	}{
		{
			name: "Normal",
			d: Documentation{
				text:   "Normal",
				format: "text/rtf",
			},
			want: "text/rtf",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.d.Format(); got != tt.want {
				t.Errorf("Documentation.Format() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNewBaseElement(t *testing.T) {
	type args struct {
		id   string
		docs []*Documentation
	}

	tests := []struct {
		name string
		args args
		want *BaseElement
	}{
		{
			name: "Normal",
			args: args{
				id: "NormalID",
				docs: []*Documentation{
					NewDoc("NormalID", ""),
				},
			},
			want: &BaseElement{
				id: "NormalID",
				docs: []Documentation{
					{
						text:   "NormalID",
						format: defaultDocFormat,
					},
				},
			},
		},
		{
			name: "Empty ID",
			args: args{
				id:   "",
				docs: []*Documentation{},
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			switch {
			case tt.want == nil:
				got := NewBaseElement(tt.args.id, tt.args.docs...)
				require.NotEqual(t, len(got.id), 0)

			default:
				if got := NewBaseElement(tt.args.id, tt.args.docs...); !reflect.DeepEqual(got, tt.want) {
					t.Errorf("NewBaseElement() = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestBaseElement_Id(t *testing.T) {
	tests := []struct {
		name string
		be   BaseElement
		want string
	}{
		{
			name: "Normal",
			be: BaseElement{
				id:   "TestID",
				docs: []Documentation{},
			},
			want: "TestID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.be.Id(); got != tt.want {
				t.Errorf("BaseElement.Id() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBaseElement_Docs(t *testing.T) {
	tests := []struct {
		name string
		be   BaseElement
		want []Documentation
	}{
		{
			name: "Normal",
			be: BaseElement{
				id: "id",
				docs: []Documentation{
					{
						text:   "First",
						format: defaultDocFormat,
					},
					{
						text:   "Second",
						format: "text/rtf",
					},
				},
			},
			want: []Documentation{
				{
					text:   "First",
					format: defaultDocFormat,
				},
				{
					text:   "Second",
					format: "text/rtf",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.be.Docs(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("BaseElement.Docs() = %v, want %v", got, tt.want)
			}
		})
	}
}
