package gateways

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/stretchr/testify/require"
)

// In-package: reach what the external gateways_test package cannot — the no-op
// eventBasedConfig.Validate and NewEventBasedGateway's option-error path
// (eventBasedConfig is unexported, so a failing EventBasedOption can't be built outside).

func TestEventBasedConfigValidate(t *testing.T) {
	require.NoError(t, (&eventBasedConfig{}).Validate())
}

func TestNewEventBasedGatewayOptionError(t *testing.T) {
	bad := EventBasedOption(func(*eventBasedConfig) error {
		return errs.New(
			errs.M("boom"),
			errs.C(errorClass, errs.InvalidParameter))
	})

	_, err := NewEventBasedGateway(bad)
	require.Error(t, err)
}
