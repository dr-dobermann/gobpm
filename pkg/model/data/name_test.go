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
		{"underscored name", "order_total", false},
		{"dotted path as a name", "order.items.price", true},
		{"single dot", "a.b", true},
		{"index bracket open", "items[0", true},
		{"index bracket close", "items0]", true},
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
