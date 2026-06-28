package foundation_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
)

func TestID(t *testing.T) {
	// NewID always yields a non-empty generated identifier.
	require.NotEmpty(t, foundation.NewID().ID())

	// NewIdentifyer keeps a provided identifier verbatim.
	require.Equal(t, "given-id", foundation.NewIdentifyer("given-id").ID())

	// NewIdentifyer generates one when the identifier is blank.
	require.NotEmpty(t, foundation.NewIdentifyer("   ").ID())
}
