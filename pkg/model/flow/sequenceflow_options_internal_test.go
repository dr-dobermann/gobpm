package flow

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSflowOptionApply(t *testing.T) {
	var opt sflowOption = func(*sflowConfig) error { return nil }

	// the option func runs against its config.
	require.NoError(t, opt(&sflowConfig{}))
}

func TestSflowConfigValidate(t *testing.T) {
	require.NoError(t, (&sflowConfig{}).Validate())
}
