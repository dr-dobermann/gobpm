package events

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDeclarationKeyEmptyDefs covers declarationKey's empty-definitions guard: a
// boundary with no event definition has no Event Declaration, so its key is the
// empty string. The public constructor never builds such a boundary (it requires
// a definition), so this defensive branch is reachable only white-box.
func TestDeclarationKeyEmptyDefs(t *testing.T) {
	require.Equal(t, "", declarationKey(&BoundaryEvent{}))
}
