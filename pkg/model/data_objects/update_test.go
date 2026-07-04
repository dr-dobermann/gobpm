package dataobjects_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	dataobjects "github.com/dr-dobermann/gobpm/pkg/model/data_objects"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
)

// TestDataObjectUpdateNotReady covers Update's readiness guard: a DataObject
// with no incoming association keeps its initial (non-Ready) state, so Update
// — which would otherwise flow the value to outgoing associations — refuses
// because the object's value was never made Ready.
func TestDataObjectUpdateNotReady(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	do, err := dataobjects.New(
		"not-ready-do",
		data.MustItemDefinition(values.NewVariable(1), foundation.WithID("nr")),
		data.UndefinedSrcState,
		foundation.WithID("notReadyDO"))
	require.NoError(t, err)

	require.Error(t, do.Update(context.Background()))
}
