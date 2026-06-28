package flow

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// foreignConfig is an options.Configurator that is not *sflowConfig, used to
// drive sflowOption.Apply down its type-casting error branch.
type foreignConfig struct{}

func (foreignConfig) Validate() error { return nil }

func TestSflowOptionApply(t *testing.T) {
	var opt sflowOption = func(*sflowConfig) error { return nil }

	// the matching configurator runs the closure.
	require.NoError(t, opt.Apply(&sflowConfig{}))

	// a foreign configurator is rejected with a type-casting error.
	require.Error(t, opt.Apply(foreignConfig{}))
}

func TestSflowConfigValidate(t *testing.T) {
	require.NoError(t, (&sflowConfig{}).Validate())
}
