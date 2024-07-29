package options_test

import (
	"errors"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/stretchr/testify/require"
)

type (
	cfg struct{}

	nameCfg struct {
		cfg

		emptyNameAllowed bool
		name             string
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
	configurator := cfg{}
	emptyNameCfg := nameCfg{
		cfg:              cfg{},
		emptyNameAllowed: true,
	}

	nonEmptyNameCfg := nameCfg{
		cfg: cfg{},
	}

	var ae *errs.ApplicationError

	// not a NameConfigurator error
	err := options.WithName("test name").Apply(&configurator)
	require.Error(t, err)
	require.True(t, errors.As(err, &ae))
	require.True(t, ae.HasClass(errs.TypeCastingError))

	// empty name isn't allowed
	require.Error(t, options.WithName("").Apply(&nonEmptyNameCfg))

	// empty name allowed
	require.NoError(t, options.WithName("").Apply(&emptyNameCfg))
	require.Empty(t, emptyNameCfg.name)

	// nonempty name
	require.NoError(t, options.WithName("test name").Apply(&nonEmptyNameCfg))
	require.Equal(t, "test name", nonEmptyNameCfg.name)
}
