package gateways

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/stretchr/testify/require"
)

// These tests live in-package to reach surface the external gateways_test package
// cannot: the no-op eventBasedConfig.Validate, EventBasedOption.Apply's wrong-config
// branch, and NewEventBasedGateway's option-error path (eventBasedConfig is unexported,
// so a failing EventBasedOption can't be built from outside the package).

func TestEventBasedConfigValidate(t *testing.T) {
	require.NoError(t, (&eventBasedConfig{}).Validate())
}

func TestEventBasedOptionApplyWrongConfig(t *testing.T) {
	// Apply against a foreign configurator (complexConfig) must fail the type assertion.
	require.Error(t, WithMixedArms().Apply(&complexConfig{}))
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
