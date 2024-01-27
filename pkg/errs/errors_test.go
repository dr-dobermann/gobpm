package errs

import (
	"fmt"
	"testing"
)

func TestApplicationError_Error(t *testing.T) {
	type fields struct {
		Err     error
		Message string
		Class   string
		Details map[string]string
	}
	tests := []struct {
		name   string
		fields fields
		want   string
	}{
		{
			name: "Normal",
			fields: fields{
				Err:     nil,
				Message: "Normal",
				Class:   "TEST",
				Details: map[string]string{
					"detail1": "detail_info1",
					"detail2": "detail_info2",
				},
			},
			want: fmt.Sprintf(
				"%s: %s[%s]: %v",
				"TEST", "Normal", map[string]string{
					"detail1": "detail_info1",
					"detail2": "detail_info2",
				}, nil),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ap := &ApplicationError{
				Err:     tt.fields.Err,
				Message: tt.fields.Message,
				Class:   tt.fields.Class,
				Details: tt.fields.Details,
			}
			if got := ap.Error(); got != tt.want {
				t.Errorf("ApplicationError.Error() = %v, want %v", got, tt.want)
			}
		})
	}
}
