package events

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/stretchr/testify/require"
)

// TestDeclarationKeyEmptyDefs covers declarationKey's empty-definitions guard: a
// boundary with no event definition has no Event Declaration, so its key is the
// empty string. The public constructor never builds such a boundary (it requires
// a definition), so this defensive branch is reachable only white-box.
func TestDeclarationKeyEmptyDefs(t *testing.T) {
	require.Equal(t, "", declarationKey(&BoundaryEvent{}))
}

// TestBoundaryCloneErrorBranch (SRD-059 T-1): Clone's error branch is
// construction-guarded — no publicly-constructible boundary carries a property
// that fails to clone (MustProperty rejects a nil-value item, the FIX-017
// hardening). The branch stays as defense in depth for clone()'s error
// contract, so this forge drives it directly: a zero-value data.Property has
// no subject, and ItemAwareElement.Clone rejects it.
func TestBoundaryCloneErrorBranch(t *testing.T) {
	be := &BoundaryEvent{
		catchEvent: catchEvent{
			Event: Event{properties: []*data.Property{{}}},
		},
	}

	_, err := be.Clone()
	require.Error(t, err)
}
