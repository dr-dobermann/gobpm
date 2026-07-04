package options_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/stretchr/testify/require"
)

type (
	cfg struct{}

	nameCfg struct {
		cfg
		name             string
		emptyNameAllowed bool
	}
)

func (c *cfg) Validate() error {
	return nil
}

func (nc *nameCfg) SetName(name string) error {
	if !nc.emptyNameAllowed && name == "" {
		return errs.New(
			errs.M("name couldn't be empty"),
			errs.C(errs.EmptyNotAllowed))
	}
	nc.name = name

	return nil
}

func TestNameOption(t *testing.T) {
	emptyNameCfg := nameCfg{
		cfg:              cfg{},
		emptyNameAllowed: true,
	}

	nonEmptyNameCfg := nameCfg{
		cfg: cfg{},
	}

	// empty name isn't allowed
	require.Error(t, options.WithName("")(&nonEmptyNameCfg))

	// empty name allowed
	require.NoError(t, options.WithName("")(&emptyNameCfg))
	require.Empty(t, emptyNameCfg.name)

	// nonempty name
	require.NoError(t, options.WithName("test name")(&nonEmptyNameCfg))
	require.Equal(t, "test name", nonEmptyNameCfg.name)
}
