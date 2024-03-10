package flow_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/stretchr/testify/require"
)

func TestSequenceFlow(t *testing.T) {
	t.Run("invalid params",
		func(t *testing.T) {
			sf, err := flow.NewSequenceFlow("test sFlow", nil, nil, nil, false)
			require.Error(t, err)
			require.Empty(t, sf)
		})
}
