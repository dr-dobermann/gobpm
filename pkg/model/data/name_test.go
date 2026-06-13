package data_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/stretchr/testify/require"
)

func TestCheckName(t *testing.T) {
	tests := []struct {
		name    string
		dataNm  string
		wantErr bool
	}{
		{"plain name", "order", false},
		{"empty name", "", false},
		{"dotted address (no separator)", "order.items.price", false},
		{"single separator", "a/b", true},
		{"leading separator", "/b", true},
		{"source-qualified name", "RUNTIME/STATE", true},
		{"bare separator", "/", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := data.CheckName(tt.dataNm, "TEST_ERRORS")
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}
