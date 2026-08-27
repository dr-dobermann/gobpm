package activities_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
)

// TestParseTransactionMethod is SRD-094 T-1: the absent attribute and both
// standard spellings read as compensate; anything else is carried as is.
func TestParseTransactionMethod(t *testing.T) {
	tests := map[string]struct {
		in   string
		want activities.TransactionMethod
	}{
		"absent":             {"", activities.TransactionCompensate},
		"blank":              {"   ", activities.TransactionCompensate},
		"metamodel spelling": {"compensate", activities.TransactionCompensate},
		"schema token":       {"##Compensate", activities.TransactionCompensate},
		"schema store":       {"##Store", "##Store"},
		"metamodel image":    {"image", "image"},
		"a URI":              {"urn:acme:tx:saga", "urn:acme:tx:saga"},
		"padding is trimmed": {"  ##Image ", "##Image"},
		"case is not folded": {"Compensate", "Compensate"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.want, activities.ParseTransactionMethod(tc.in))
		})
	}
}

// TestTransactionOptions is SRD-094 T-2: the defaults, the two carried
// values, and the refusals — each naming the option that refused.
func TestTransactionOptions(t *testing.T) {
	t.Run("no options means compensate and no protocol", func(t *testing.T) {
		sp, err := activities.NewSubProcess("tx", activities.WithTransaction())
		require.NoError(t, err)
		tc := sp.Transaction()
		require.NotNil(t, tc)
		require.Equal(t, activities.TransactionCompensate, tc.Method())
		require.Empty(t, tc.Protocol())
	})

	t.Run("method and protocol are carried", func(t *testing.T) {
		sp, err := activities.NewSubProcess("tx", activities.WithTransaction(
			activities.WithTransactionMethod("##Store"),
			activities.WithTransactionProtocol("wsat")))
		require.NoError(t, err)
		require.Equal(t, activities.TransactionMethod("##Store"),
			sp.Transaction().Method())
		require.Equal(t, "wsat", sp.Transaction().Protocol())
	})

	t.Run("a blank method is refused", func(t *testing.T) {
		_, err := activities.NewSubProcess("tx", activities.WithTransaction(
			activities.WithTransactionMethod("  ")))
		require.Error(t, err)
		require.Contains(t, err.Error(), "WithTransactionMethod")
	})

	t.Run("a blank protocol is refused", func(t *testing.T) {
		_, err := activities.NewSubProcess("tx", activities.WithTransaction(
			activities.WithTransactionProtocol("")))
		require.Error(t, err)
		require.Contains(t, err.Error(), "WithTransactionProtocol")
	})

	t.Run("a nil option is refused", func(t *testing.T) {
		_, err := activities.NewSubProcess("tx", activities.WithTransaction(nil))
		require.Error(t, err)
		require.Contains(t, err.Error(), "WithTransaction: a nil")
	})
}
