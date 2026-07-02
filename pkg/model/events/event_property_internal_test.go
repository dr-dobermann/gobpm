package events

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/stretchr/testify/require"
)

// TestPropCollector covers the propCollector newEvent uses to gather the
// properties supplied via data.PropertyOption (FIX-018 3.2.2): a nil property is
// rejected, a valued one is collected, and Validate satisfies the
// options.Configurator contract.
func TestPropCollector(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	pc := propCollector{}
	require.Error(t, pc.AddProperty(nil))
	require.NoError(t, pc.Validate())

	p := data.MustProperty("p",
		data.MustItemDefinition(values.NewVariable(0)), data.ReadyDataState)
	require.NoError(t, pc.AddProperty(p))
	require.Len(t, pc.props, 1)
}

// TestNewEventPropertyOptionError covers newEvent's error branch when a property
// option fails to apply — here WithProperty with an empty name.
func TestNewEventPropertyOptionError(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	_, err := newEvent("e", nil, nil, data.WithProperty("", nil))
	require.Error(t, err)
}

// TestCatchEventUploadDataPropertyError covers catchEvent.UploadData's error
// wrap when a property can't be loaded. A value-less property can't be built
// through the constructor (FIX-018 3.2.4), so a zero-value one is injected to
// exercise the load guard.
func TestCatchEventUploadDataPropertyError(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ce, err := newCatchEvent("catch", nil, nil, false)
	require.NoError(t, err)

	ce.properties = []*data.Property{{}}

	require.Error(t, ce.UploadData(context.Background(), frameFor(t, ce.ID())))
}
