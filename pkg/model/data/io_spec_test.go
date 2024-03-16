package data_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/stretchr/testify/require"
)

func TestDataSet(t *testing.T) {
	t.Run("empty set",
		func(t *testing.T) {
			s, err := data.NewDataSet("")
			require.Error(t, err)
			require.Empty(t, s)
		})

}
