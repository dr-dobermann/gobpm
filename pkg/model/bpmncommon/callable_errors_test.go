package bpmncommon_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// TestNewCallableElementRejectsInvalidOption covers FIX-026 §4.1.7.
func TestNewCallableElementRejectsInvalidOption(t *testing.T) {
	require.NotPanics(t, func() {
		_, err := bpmncommon.NewCallableElement("c", foundation.WithID(""))
		require.Error(t, err)
	})

	ce, err := bpmncommon.NewCallableElement("c")
	require.NoError(t, err)
	require.Equal(t, "c", ce.Name)
}
