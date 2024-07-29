package gateways_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/stretchr/testify/require"
)

func TestDirection(t *testing.T) {
	// invalid cases
	for _, ec := range []string{
		"",
		"invalid_direction",
	} {
		require.Error(t, gateways.GDirection(ec).Validate())
	}

	dir := gateways.Unspecified

	require.NoError(t, dir.Validate())
}

func TestNewGateway(t *testing.T) {
	// invalid options
	_, err := gateways.New(activities.WithCompensation())
	require.Error(t, err)

	// valid options
	g, err := gateways.New(foundation.WithId("gate #1"), options.WithName("my gate"))
	require.NoError(t, err)
	require.Equal(t, "gate #1", g.Id())
	require.Equal(t, "my gate", g.Name())
	require.Equal(t, gateways.Unspecified, g.Direction())

	// with new direction
	g, err = gateways.New(gateways.WithDirection(gateways.Mixed))
	require.NoError(t, err)
	require.Equal(t, gateways.Mixed, g.Direction())
}
